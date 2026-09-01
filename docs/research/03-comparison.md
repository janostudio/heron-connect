# 架构对比：OpenClaw vs Heron Connect-QHN

> 两个项目在相同目标下采用了截然不同的设计路径

## 一、总体对比矩阵

| 维度 | OpenClaw | Heron Connect-QHN | 优劣 |
|------|----------|----------------|------|
| **语言** | TypeScript/Bun | Go | CC 性能更好 |
| **平台数量** | 23+ | 12 | OC 更多 |
| **Agent 数量** | ~3 | 11 | CC 更多 |
| **插件契约** | 契约优先（capabilities声明） | 接口隔离（运行时类型断言） | OC 更清晰 |
| **流式模式** | 4 种明确模式，统一循环 | 4 种 DisplayMode + streamPreview 交叉 | OC 更简洁 |
| **Markdown 分块** | 感知代码块/表格 | 按字符数硬截断 | OC 更好 |
| **入站上下文** | 丰富（历史/角色/能力） | 基础（用户/内容/平台名） | OC 更好 |
| **平台原生格式** | 每插件独立 renderMessage | 部分平台有 markdown_html.go | OC 更好 |
| **Engine 复杂度** | 多个协调者，按职责拆分 | 单体 God Object 13k 行 | OC 更好 |
| **高级功能** | 无 | cron/relay/workspace/TTS | CC 更好 |
| **多账号** | 支持 | 不支持 | OC 更好 |
| **测试难度** | 插件独立测试 | engine_test.go 12k 行 | OC 更好 |

## 二、插件契约模型对比

### OpenClaw：契约优先（Contract-First）

```typescript
// 能力声明是 ChannelPlugin 的一部分
const feishuPlugin: ChannelPlugin = {
  id: 'feishu',
  capabilities: {
    streamingMode: 'block',
    supportsNativeCards: true,
    supportsThreads: true,
    supportsMarkdown: true,
    markdownDialect: 'lark',
    maxMessageLength: 4000,
    // ... 完整能力声明
  },
  // 方法实现
}

// Engine 查询能力（无类型断言）
if (plugin.capabilities.streamingMode !== 'off') {
  const handle = await plugin.startStreaming(ctx)
  // ...
}
```

### Heron Connect-QHN：接口隔离（Interface Segregation）

```go
// 运行时类型断言探测能力
if updater, ok := p.(MessageUpdater); ok {
    updater.UpdateMessage(ctx, replyCtx, content)
}

if starter, ok := p.(PreviewStarter); ok {
    handle, err := starter.SendPreviewStart(ctx, replyCtx, content)
}

if scp, ok := p.(StreamingCardPlatform); ok {
    sc, err := scp.CreateStreamingCard(ctx, replyCtx)
}
```

**分析：**
- OpenClaw 方案：能力在编译/初始化时已知，代码更清晰，但失去 Go 的类型安全
- Heron Connect-QHN 方案：Go interface 类型安全，但 engine.go 中充满 `if _, ok := p.(X); ok` 判断
- CC 方案的隐患：当平台忘记实现某个接口，或接口行为不一致时，很难发现

### 建议：能力注册表（兼顾两者）

```go
// 重构后的方案：每个平台注册能力声明，引擎查询
type PlatformCapabilities struct {
    StreamingMode      StreamingMode // "off" | "partial" | "block" | "progress"
    SupportsNativeCard bool
    SupportsThread     bool
    SupportsFileAttach bool
    MaxMessageLength   int
    MarkdownDialect    string        // "standard" | "lark" | "html" | "mrkdwn" | "plain"
}

type CapabilityDeclarer interface {
    Capabilities() PlatformCapabilities
}
```

## 三、流式传输架构对比

### OpenClaw：统一流式循环

```
AgentEventStream
        │
        ▼
draft-stream-loop.ts
        │
        ├─ mode == 'off'       → 缓存所有内容 → 一次性发送
        ├─ mode == 'partial'   → 每次文本事件 UpdateMessage
        ├─ mode == 'block'     → chunkMarkdownText → 逐块发送
        └─ mode == 'progress'  → 更新进度卡片（thinking/tool/text）
```

**特点：**
- 每条 AgentEvent 进入同一个入口
- 根据平台的 `streamingMode` 选择策略
- 每种策略有独立的状态机
- 完全感知 Markdown 结构（不截断代码块）

### Heron Connect-QHN：多系统交叉

```
AgentEventStream
        │
        ▼
engine.go (processInteractiveSession)
        │
        ├─ StreamingCardPlatform? → streamCard.Update()     (DingTalk 路径)
        │
        ├─ DisplayMode == "stream"
        │   ├─ streamPreviewToolHold? → sp.appendTextNow()
        │   └─ sp.appendText()
        │
        ├─ DisplayMode == "quiet" → 累积到 textParts[]
        │
        ├─ DisplayMode == "compact" → compactProgressWriter
        │
        └─ DisplayMode == "full" → 每事件发一条消息
```

**问题：**
- 4 种 DisplayMode × 各种平台能力 = 极复杂的组合空间
- `streamPreview` 与 `StreamingCard` 是两套独立机制
- `streamPreviewToolHold` 这样的特殊标志表明已经在打补丁
- 不同平台的降级行为不一致（`degraded` 标志）

### 对比视图

```
OpenClaw                          Heron Connect-QHN
─────────────────                 ─────────────────────────
AgentEvent                        AgentEvent
    │                                 │
    ▼                                 ▼
策略选择（1处）                   engine.go（>200个分支）
    │                                 │
    ▼                                 ├── if StreamingCard
具体策略（4种）                       ├── if displayModeStream
    │                                 │   ├── if toolHold
    ▼                                 │   └── sp.appendText
发送                                  ├── if displayModeQuiet
                                      ├── if displayModeCompact
                                      └── else (full)
```

## 四、消息分块对比

### OpenClaw：`chunkMarkdownText`

```
输入: "# 标题\n\n代码:\n```python\ndef foo():\n    return 'bar'\n```\n\n后续文本..."
                                                
分块策略（优先级）：
  1. 段落边界（\n\n）
  2. 列表项边界  
  3. 句子边界
  4. 字符数截断（最后手段）

约束（始终保持）：
  - 不在代码块内截断
  - 不在表格行内截断
  - 不在标题后立即截断（保持与后续内容的关联）

输出: ["# 标题\n\n代码:\n```python\ndef foo():\n    return 'bar'\n```", "后续文本..."]
```

### Heron Connect-QHN：字符数截断

```go
// engine.go
const maxPlatformMessageLen = 4000

// 只有简单的长度检查（Feishu 卡片内部）
if len([]rune(text)) > maxPlatformMessageLen {
    text = string([]rune(text)[:maxPlatformMessageLen]) + "…"
}
```

**问题场景：**
```markdown
# 一个长响应...（3900字符）

```python
def very_long_function():
    # 这里是代码（200行，8000字符）
    pass
```

更多解释...
```

Heron Connect-QHN 会在第 4000 个字符处截断，无论是否在代码块中间。

## 五、入站上下文对比

### OpenClaw 注入的上下文

```
给 AI 的系统提示中包含：
  [Platform Info]
  Platform: Feishu
  Markdown dialect: lark
  Max message length: 4000
  Rich cards: supported
  Threads: supported
  
  [Channel Info]
  Channel: 产品团队
  Channel type: group
  
  [User Info]
  User: Alice Wang (alice@company.com)
  Role: admin
  
  [Recent History]
  Alice: 帮我看一下这个代码
  Bot: 好的，我来分析...
  Alice: 还有这个bug
  
  [Current Message]
  Alice: 修一下这个函数
  [attached: file.py, 156 lines]
```

### Heron Connect-QHN 注入的上下文

```
给 AI 的系统提示中包含：
  (来自 AgentSystemPrompt())
  - 可用工具说明（heron-connect send、cron add 等）
  
  (来自 FormattingInstructionProvider 接口，如果平台实现了的话)
  - 格式化指令（部分平台）
  
  (当前消息)
  用户消息内容
```

**差距：** AI 不知道当前平台是什么、支持什么格式、用户是谁、之前聊了什么。

## 六、多账号支持对比

### OpenClaw

```json
{
  "channels": [
    { "plugin": "telegram", "id": "team-bot", "token": "..." },
    { "plugin": "telegram", "id": "personal-bot", "token": "..." },
    { "plugin": "feishu", "id": "company-feishu", "app_id": "..." }
  ]
}
```

每个 `id` 独立维护会话上下文，互不干扰。

### Heron Connect-QHN

```toml
# config.toml
[[platform]]
type = "feishu"
# 只能配置一个飞书账号
```

目前每种平台类型只能配置一个实例（多工作区功能通过 workspace 机制实现，但不是真正的多账号）。

## 七、核心差距总结

| 领域 | 差距描述 | 影响 |
|------|----------|------|
| **流式一致性** | 4种Mode × 各种接口组合，无统一策略 | 各平台体验不一致 |
| **Markdown 分块** | 硬截断 vs 感知截断 | 代码/表格被破坏 |
| **入站上下文** | 仅基础字段 vs 丰富上下文 | AI 回复质量差 |
| **平台原生渲染** | 部分支持 vs 系统性支持 | 格式化效果差 |
| **可维护性** | 13k 行 God Object | 迭代速度慢 |
| **能力可见性** | 运行时探测 vs 声明式 | 文档/调试困难 |

## 八、2026-08-31 复核增量（针对 IM 交互，重读最新源码）

> 以下结论基于对 openclaw 当前 main（`0757cad`）源码的重新阅读，是对 §二~§七 的补充与加深，聚焦「IM 交互」这个切口。旧结论仍成立，这里只列新证据或新角度。

### 8.1 显式消息流转状态机 + admission 决策（最值得学）

openclaw 把入站消息处理收口成 `runChannelTurn`（`src/channels/turn/kernel.ts:620`）驱动的 9 阶段管线：

```
ingest → classify → preflight → resolve → authorize → assemble → record → dispatch → finalize
```

关键决策点是 `ChannelTurnAdmission`（`turn/types.ts:35`）四选一：`dispatch | observeOnly | handled | drop`。每个阶段 `emit()` 一条 `ChannelTurnLogEvent`（带 stage + event + sessionKey），全链路可观测。

heron-connect 对应物是各平台 `MessageHandler` 回调里散落的路由/允许/命令判断，没有显式 stage、没有 admission 枚举、没有统一日志。**这是 heron-connect 最值得引入的一处**：把「接收→准入→记录→分发」抽成显式管线。

### 8.2 进度草稿的行级就地更新（progress_compact.go 直接升级方向）

openclaw 用判别联合事件 `ChannelProgressDraftLineInput`（`streaming.ts:214`，含 tool/item/plan/approval/command-output/patch）+ 稳定行 `id` + `correlationKey`，靠 `mergeChannelProgressDraftLine`（`streaming.ts:1116`）**就地更新已有行**而非追加新行。

heron-connect 的 `compactProgressWriter`（`core/progress_compact.go:208`）是「追加 entries + 截断到 maxEntries」，**没有行级更新/合并**，只能整体重发。openclaw 还拆了四层正交模块（节流 draft-stream-loop / 分块 chunking / 渲染 streaming / 组合 compositor），heron-connect 是单文件全职责。

### 8.3 会话绑定生命周期（与 web idle 误切事故同源）

openclaw 用 `thread-bindings-policy.ts` 显式管理「IM 会话↔agent 会话」绑定的 idle timeout / maxAge / 回收 / spawn 策略（account > channel > global 三级覆盖），binding id 是 `accountId:conversationId` 前缀。

heron-connect 靠 session_key 字符串前缀约定，回收靠各平台自己或 idle override——之前修过的「web 后台被项目级 720 idle 误切会话」事故正源于缺统一回收策略。

### 8.4 cron 是一等子系统

openclaw `src/cron/` 是完整服务：三种调度（`at`/`every`/`cron`，`schedule.ts:55`）+ durable delivery（`delivery.ts:46`，失败通知）+ isolated agent 会话 + heartbeat 双轨（`auto-reply/heartbeat.ts`，`notify=false` 静默）。

heron-connect 的 cron（`core/cron.go`）是 engine 的辅助能力，只有标准 cron 表达式 + `ReplyContextReconstructor` 重建 replyCtx。两者「从会话重建主动推送目标」思路一致，但 openclaw 泛化成了 outbound session 上下文构建。

### 8.5 结论收敛（若只挑三样引入）

1. **显式消息准入/记录/分发管线**（替代散落 handler）；
2. **投递前能力矩阵降级**（替代运行时 `p.(Xxx)` 断言——§二已提，此处重申）；
3. **进度草稿的行级就地更新 + 事件类型化**（progress_compact.go 升级）。

其余（多账号、config schema 插件化、cron 一等化）作为更远期方向，不急于引入。
