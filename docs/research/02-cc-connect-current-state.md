# CC-Connect-QHN 现状分析

> 基于对 `projects/cc-connect-qhn` 源码的完整调研  
> 调研时间：2026-05-28

## 一、代码规模

```
cc-connect-qhn/
├── cmd/cc-connect/          ~2,000 行  ← CLI 入口、插件注册
├── config/                  ~1,500 行  ← TOML 配置解析
├── core/                   59,847 行  ← 核心引擎（含测试）
│   ├── engine.go           13,820 行  ← ⚠️ 单体 God Object
│   ├── engine_test.go      12,688 行
│   ├── i18n.go              3,948 行
│   ├── management.go        1,950 行
│   ├── management_test.go   2,617 行
│   ├── bridge.go            1,398 行
│   ├── streaming.go           526 行  ← 流式预览
│   ├── interfaces.go          571 行  ← 接口定义
│   ├── message.go             ~200 行  ← 消息/事件类型
│   ├── markdown.go             54 行  ← ⚠️ 极简 Markdown 处理
│   ├── markdown_html.go       495 行  ← HTML 转换（用于部分平台）
│   ├── markdown_slack.go      131 行
│   └── ...（90+ 文件）
├── platform/               31,069 行
│   ├── feishu/feishu.go     4,234 行
│   ├── telegram/            1,568 行
│   ├── qqbot/               1,564 行
│   ├── discord/             1,350 行
│   ├── dingtalk/            1,256 行
│   ├── wecom/                 937 行
│   ├── weixin/                731 行
│   ├── slack/                 ~600 行
│   ├── qq/                    ~500 行
│   ├── line/                  ~400 行
│   └── weibo/                 ~300 行
└── agent/                  26,006 行
    ├── claudecode/          ~2,700 行
    ├── codex/               ~3,400 行
    ├── opencode/            ~1,700 行
    ├── gemini/              ~1,300 行
    ├── acp/                 ~1,400 行
    └── ...（11 种 agent）
```

总计约 **120,000+ 行**（含测试代码）。

## 二、架构设计（优点）

### 2.1 依赖方向严格

```
cmd/ → config/, core/, agent/*, platform/*
agent/*   → core/   (never other agents or platforms)
platform/* → core/  (never other platforms or agents)
core/     → stdlib only
```

这一设计彻底避免了循环依赖，是非常好的架构约束。

### 2.2 接口隔离（Interface Segregation）

`core/interfaces.go` 定义了 **50+ 个细粒度可选接口**，平台/Agent 按需实现：

**平台接口（部分）：**
```go
type Platform interface {          // 基础（必须实现）
    Name() string
    Start(handler MessageHandler) error
    Reply(ctx context.Context, replyCtx any, content string) error
    Send(ctx context.Context, replyCtx any, content string) error
    Stop() error
}

// 可选接口（按需实现）
type MessageUpdater interface { ... }          // 消息原地更新
type PreviewStarter interface { ... }         // 流式预览启动
type PreviewCleaner interface { ... }         // 预览消息清理
type PreviewFinalizer interface { ... }       // 预览最终化
type PreviewStatusUpdater interface { ... }   // 预览状态更新
type StreamingCardPlatform interface { ... }  // 流式卡片（DingTalk 专用）
type StreamingCard interface { ... }          // 流式卡片句柄
type RichCardSupporter interface { ... }      // 富格式卡片构建
type CardSender interface { ... }             // 卡片发送
type InlineButtonSender interface { ... }     // 内联按钮
type ImageSender interface { ... }            // 图片发送
type FileSender interface { ... }             // 文件发送
type TypingIndicator interface { ... }        // 输入中指示器
type TypingIndicatorDone interface { ... }    // 完成反应
type MarkdownTableSplitter interface { ... }  // 表格分割
type ProgressStyleProvider interface { ... }  // 进度显示风格
```

这种设计非常灵活，添加新能力不需要修改已有平台。

### 2.3 插件注册机制

通过 `init()` 函数 + 构建标签实现编译时选择：

```go
// platform/feishu/feishu.go
func init() {
    core.RegisterPlatform("feishu", func(opts map[string]any) (core.Platform, error) {
        return NewPlatform(opts)
    })
}

// cmd/cc-connect/plugin_platform_feishu.go
//go:build !no_feishu
package main
import _ "cc-connect/platform/feishu"
```

### 2.4 已有流式基础设施

`core/streaming.go` 已经实现了相当完善的 `streamPreview`：

```go
type streamPreview struct {
    cfg       StreamPreviewCfg     // 全局配置
    platform  Platform             // 目标平台
    replyCtx  any                  // 回复上下文
    
    fullText          string       // 累积的完整文本
    lastSentText      string       // 上次发送的文本
    lastSentAt        time.Time    // 上次发送时间
    lastSentViaUpdate bool         // 是否通过 UpdateMessage 发送
    previewMsgID      any          // 预览消息 ID
    degraded          bool         // 是否已降级（停止预览）
    
    timer     *time.Timer          // 节流定时器
    timerStop chan struct{}
}
```

**关键方法：**
- `appendText()` — 追加文本 + 节流刷新
- `appendTextNow()` — 立即刷新（高优先级事件用）
- `freeze()` — 冻结预览（权限请求时）
- `discard()` — 丢弃预览（工具调用后发新消息）
- `finish(finalText)` — 最终化预览，返回是否已通过预览完成

### 2.5 显示模式系统

`DisplayCfg.Mode` 支持四种模式：

| 模式 | 含义 |
|------|------|
| `full` | 显示所有中间消息（thinking、tool use、tool result） |
| `compact` | 显示压缩版进度，tool use 摘要 |
| `quiet` | 只显示最终答案，过程静默 |
| `stream` | 流式预览模式，边生成边更新 |

### 2.6 高级功能（OpenClaw 没有）

CC-Connect-QHN 有许多 OpenClaw 没有的高级功能：
- **Cron 调度**：`/cron add` 定时任务
- **Bot-to-Bot Relay**：`cc-connect relay send` 跨 Agent 通信
- **多工作区**：一个 bot 管理多个项目工作区
- **WebSocket Bridge**：`core/bridge.go`，支持 Web 前端
- **多模型/Provider**：`/provider` 命令切换 API provider
- **TTS**：文字转语音（Feishu 等）
- **权限系统**：用户角色、管理员权限
- **观察模式**：`--observe` 监听 Claude Code 本地会话
- **/skills**：项目级别技能注册

## 三、核心问题分析

### 3.1 ⚠️ Engine God Object（最严重）

`engine.go` 是 13,820 行的单体文件，包含 **200+ 个方法**：

```
命令处理（/help, /list, /model, /provider, /cron, /alias...）
├── cmdCron, cmdCronAdd, cmdCronList, cmdCronDel, cmdCronToggle...
├── cmdProvider, cmdModel, cmdAlias, cmdDelete, cmdBind...
├── cmdMemory, cmdConfig, cmdSkills, cmdDoctor, cmdVersion...
消息路由
├── handleMessage (核心分发)
├── processInteractiveSession (事件循环)
卡片渲染
├── renderListCard, renderDirCard, renderProviderCard...
├── renderCronCard, renderCommandsCard, renderAliasCard...
工作区管理
├── resolveWorkspace, handleWorkspaceInitFlow...
Cron 执行
├── ExecuteCronJob, executeCronShell...
Bot-to-Bot Relay
├── HandleRelay, drainRelaySession...
```

**后果：**
- 几乎无法在不熟悉 13k 行代码的情况下添加新功能
- 测试极难（engine_test.go 也有 12k 行）
- 代码不可复用，所有逻辑耦合在 `*Engine` 上
- git blame/diff 时噪音极大

### 3.2 ⚠️ 流式机制与显示模式不统一

`displayModeStream` 和 `streamPreview` 是两套独立机制，存在复杂的交互：

```go
// engine.go 中的耦合示例
streamPreviewToolHold := sp.previewMode() == "tool_hold" && e.display.Mode == displayModeStream

// 各处的条件判断：
if e.display.Mode == displayModeQuiet || e.display.Mode == displayModeStream {
    // ...
}
if e.display.Mode == displayModeStream && e.display.ToolMessages {
    // ...
}
if streamPreviewToolHold && streamToolHoldNeedsAnswerSeparator && len(textParts) > 0 {
    // ...
}
```

这种状态组合导致：
- 行为难以预测（Mode × previewMode 的组合空间很大）
- 平台间体验不一致
- 新增平台时不知道该如何正确接入流式

### 3.3 ⚠️ Markdown 处理粗放

```go
// core/markdown.go（全部 54 行）
const maxPlatformMessageLen = 4000  // ← engine.go 中定义

// 只有 StripMarkdown 函数，用于不支持 Markdown 的平台
func StripMarkdown(s string) string {
    // 简单正则替换，无结构感知
}
```

**问题：**
- 没有 Markdown 感知分块（代码块会被截断）
- `maxPlatformMessageLen = 4000` 是全局常量，不区分平台
- 长消息按字符数硬截断，破坏代码块/表格
- Telegram 特殊处理在 `markdown_html.go` 中但逻辑散乱

### 3.4 ⚠️ 入站消息上下文单薄

```go
// core/message.go 中的 Message 结构体
type Message struct {
    SessionKey   string
    Platform     string
    UserID       string
    UserName     string
    Content      string
    Images       []ImageAttachment
    Files        []FileAttachment
    ReplyCtx     any
    ModeOverride string
    // ... 约 20 个字段
}
```

**对比 OpenClaw 的 `InboundEventContext`：**
- 没有频道历史（需要 AI 有更好的上下文感知）
- 没有平台能力描述（AI 不知道当前平台支持什么格式）
- 没有用户角色信息（AI 不知道用户是管理员还是普通用户）
- 没有消息引用展开（引用消息只有 ID，AI 看不到内容）

### 3.5 平台支持矩阵不完整

各平台对高级能力的支持存在明显差异，且文档化不足：

| 平台 | 流式预览 | 原生卡片 | 图片 | 文件 | Thread |
|------|----------|----------|------|------|--------|
| Feishu | ✅ (RichCard) | ✅ | ✅ | ✅ | ✅ |
| Telegram | ✅ (UpdateMsg) | ❌ | ✅ | ✅ | ❌ |
| Discord | ✅ (UpdateMsg) | ❌ | ✅ | ✅ | ✅ |
| DingTalk | ✅ (StreamCard) | ✅ (AI Card) | ✅ | ✅ | ❌ |
| Slack | ⚠️ 有限 | ❌ | ✅ | ✅ | ✅ |
| WeChat Work | ❌ | ❌ | ✅ | ✅ | ❌ |
| LINE | ❌ | ❌ | ✅ | ❌ | ❌ |
| QQBot | ❌ | ❌ | ✅ | ❌ | ❌ |

这个矩阵没有被系统化地记录，Engine 需要通过运行时类型断言来探测，容易出 bug。

## 四、现有优势总结

尽管存在问题，CC-Connect-QHN 在以下方面有扎实基础：

1. **接口设计成熟**：`core/interfaces.go` 的 Interface Segregation 设计非常灵活
2. **流式基础设施**：`streaming.go` 的 `streamPreview` 实现了节流、降级、状态管理
3. **高级功能完整**：cron、relay、workspace、TTS、provider 等 OpenClaw 没有的功能
4. **安全性**：用户角色、allow_from、token 脱敏等
5. **测试基础**：大量单元测试（虽然 engine_test.go 也是 god object）
6. **多 Agent 支持**：11 种 agent，OpenClaw 通常只支持 2-3 种
7. **Go 性能**：资源占用远低于 Node.js/TypeScript 实现
