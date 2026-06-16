# 大重构路线图

> 渐进式重构策略：在保持功能完整和业务连续的前提下，系统性改善架构和用户体验

## 重构原则

1. **不推倒重建**：每个 Phase 独立可交付，完成后系统仍可正常运行
2. **先测试后重构**：每次拆分前补充测试，确保行为一致
3. **接口向后兼容**：`core/interfaces.go` 的现有接口不做破坏性修改
4. **保留 Go 优势**：性能、类型安全、低资源消耗
5. **学习 OpenClaw 的 UX 设计**，而不是抄代码

---

## Phase 1：Engine 拆分（最高优先级，解锁其他所有改进）

### 目标

将 `core/engine.go`（13,820 行）按职责拆分为多个内聚模块，同时保持所有现有功能。

### 拆分方案

#### 1.1 命令路由层（CommandRouter）

```go
// core/command_router.go（新建）
// 负责：识别 /command 并分发给对应 Handler

type CommandRouter struct {
    handlers map[string]CommandHandler
}

type CommandHandler interface {
    Handle(ctx *CommandContext) error
}

type CommandContext struct {
    Platform    Platform
    Message     *Message
    Engine      *Engine  // 必要时访问 Engine 状态
    I18n        *I18n
}
```

**迁移的方法（engine.go 中的命令处理）：**
```
cmdHelp, cmdList, cmdCurrent, cmdHistory      → core/cmd_session.go
cmdModel, cmdProvider, cmdAlias              → core/cmd_agent.go
cmdMemory, cmdConfig, cmdDoctor, cmdVersion  → core/cmd_config.go
cmdCron, cmdCronAdd, cmdCronList, cmdCronDel → core/cmd_cron.go
cmdBind, cmdBindStatus, cmdBindSetup         → core/cmd_workspace.go
cmdSkills, cmdCommands, cmdAlias             → core/cmd_command.go
cmdDelete, cmdWhoami, cmdUpgrade             → core/cmd_misc.go
```

每个 `cmd_*.go` 文件约 300-500 行，总量和原来一样，但按职责分组。

#### 1.2 卡片渲染层（CardRenderer）

```go
// core/card_renderer.go（新建）
// 负责：构建各种 *Card 对象

type CardRenderer struct {
    engine *Engine
    i18n   *I18n
}

func (r *CardRenderer) RenderListCard(sessionKey string, page int) (*Card, error)
func (r *CardRenderer) RenderDirCard(sessionKey string, page int) (*Card, error)
func (r *CardRenderer) RenderProviderCard() *Card
func (r *CardRenderer) RenderCronCard(sessionKey, userID string) *Card
// ... 15+ 个 render 方法
```

**迁移的方法（engine.go 中的 render*）：**
```
renderListCard, renderDirCard, renderCurrentCard, renderHistoryCard
renderProviderCard, renderProviderAddCard
renderCronCard, renderCommandsCard, renderAliasCard
renderConfigCard, renderSkillsCard, renderDoctorCard
renderVersionCard, renderUpgradeCard, renderWhoamiCard
renderHeartbeatCard
```

#### 1.3 事件处理循环（TurnProcessor）

这是最核心也最复杂的部分——agent 事件处理逻辑。

```go
// core/turn_processor.go（新建）
// 负责：处理一个完整的 Agent 回合（从用户发消息到 Agent 完成响应）

type TurnProcessor struct {
    platform      Platform
    replyCtx      any
    agentSession  AgentSession
    display       DisplayCfg
    streamPreview *streamPreview
    streamCard    StreamingCard
    i18n          *I18n
    
    // 状态
    textParts     []string
    toolSteps     []ToolStep
    turnStart     time.Time
}

func (t *TurnProcessor) Run(ctx context.Context, events <-chan Event) error
func (t *TurnProcessor) handleText(event Event) error
func (t *TurnProcessor) handleToolUse(event Event) error
func (t *TurnProcessor) handleToolResult(event Event) error
func (t *TurnProcessor) handleThinking(event Event) error
func (t *TurnProcessor) handlePermission(event Event) error
func (t *TurnProcessor) handleError(event Event) error
func (t *TurnProcessor) finalize() error
```

**迁移的代码：** engine.go 中的 `processInteractiveSession` 函数（约 800-1000 行，是当前最复杂的函数）。

#### 1.4 保留 Engine 核心

重构后的 `Engine` 结构体仍然存在，但只负责：
- 保存配置（`DisplayCfg`, `StreamPreviewCfg` 等）
- 管理平台列表 + 会话管理器
- 接收入站消息（`handleMessage`）并分发
- 协调 `CommandRouter`、`TurnProcessor`、`CardRenderer`

目标：`engine.go` 从 13,820 行降至 **2,000-3,000 行**。

### 执行步骤

```
Week 1: 为 engine.go 中的命令处理方法添加测试
Week 2: 提取 CardRenderer，验证所有卡片渲染测试通过
Week 3: 提取 CommandRouter + cmd_*.go 系列文件
Week 4: 提取 TurnProcessor，这是最难的一步
Week 5: 清理 Engine struct，移除迁移后不再需要的字段
Week 6: 性能测试 + 集成测试，确保功能完整
```

### 成功标准
- `go test ./...` 全部通过
- `engine.go` < 3,000 行
- 每个新文件 < 600 行
- 命令添加时间从"2小时理解上下文"降至"30分钟"

---

## Phase 2：Markdown 感知分块（快速收益）

### 目标

实现 `ChunkMarkdownText`，在代码块/表格边界处分块，替代现有的字符数硬截断。

### 实现方案

```go
// core/markdown_chunk.go（新建）

// ChunkOptions 控制分块行为
type ChunkOptions struct {
    MaxLength        int  // 单块最大字符数（字节/Rune 任选）
    RespectCodeBlock bool // 不在代码块内截断（默认 true）
    RespectTable     bool // 不在表格行内截断（默认 true）
}

// ChunkMarkdownText 将 Markdown 文本按语义边界分块。
// 保证不在代码块或表格内部截断。
func ChunkMarkdownText(text string, opts ChunkOptions) []string {
    if opts.MaxLength <= 0 || len([]rune(text)) <= opts.MaxLength {
        return []string{text}
    }
    
    chunks := []string{}
    
    // 1. 找出代码块范围（不可分割区域）
    codeRanges := findCodeBlockRanges(text)
    tableRanges := findTableRanges(text)
    
    // 2. 找出所有候选分割点（段落边界 > 列表项 > 句子 > 字符数）
    splitPoints := findSplitPoints(text)
    
    // 3. 过滤掉落在禁区内的分割点
    validPoints := filterSplitPoints(splitPoints, codeRanges, tableRanges)
    
    // 4. 贪心分块
    return greedyChunk(text, validPoints, opts.MaxLength)
}

// 场景1：平台消息长度限制的分块
func (e *Engine) sendChunked(p Platform, replyCtx any, text string) error {
    maxLen := platformMaxLen(p) // 查询平台最大长度
    chunks := ChunkMarkdownText(text, ChunkOptions{
        MaxLength:        maxLen,
        RespectCodeBlock: true,
        RespectTable:     true,
    })
    for _, chunk := range chunks {
        if err := p.Reply(e.ctx, replyCtx, chunk); err != nil {
            return err
        }
    }
    return nil
}
```

### 与平台能力的集成

引入 `MaxMessageLengthProvider` 接口（可选）：
```go
type MaxMessageLengthProvider interface {
    MaxMessageLength() int
}
```

已实现此接口的平台返回精确限制；未实现的平台使用全局默认值 4000。

### 执行步骤

```
Day 1-2: 实现 findCodeBlockRanges, findTableRanges
Day 3-4: 实现 findSplitPoints + greedyChunk
Day 5: 单元测试（重点：代码块不截断、表格不截断、Unicode 正确处理）
Day 6: 集成到 engine.go 的 sendChunked / final text 发送路径
Day 7: E2E 测试
```

### 成功标准
- 代码块/表格在各平台发送时不被截断
- 单元测试覆盖率 > 90%（这是纯函数，易测）

---

## Phase 3：统一流式传输层（Draft Stream）

### 目标

借鉴 OpenClaw 的 `draft-stream-loop.ts`，在 Go 中实现统一的流式处理层，替代当前 engine.go 中分散的流式逻辑。

### 架构设计

```go
// core/draft_stream.go（新建）

// StreamingMode 描述平台的流式能力
type StreamingMode string

const (
    StreamingModeOff      StreamingMode = "off"      // 不支持流式，等全量完成
    StreamingModePartial  StreamingMode = "partial"  // 支持原地更新（UpdateMessage）
    StreamingModeBlock    StreamingMode = "block"    // 分块发送（不能编辑时）
    StreamingModeProgress StreamingMode = "progress" // 进度卡片（DingTalk AI Card）
)

// DraftStreamProcessor 处理一个 Agent 回合的流式输出
type DraftStreamProcessor struct {
    platform Platform
    replyCtx any
    mode     StreamingMode
    opts     DraftStreamOptions
}

type DraftStreamOptions struct {
    Display       DisplayCfg
    StreamPreview StreamPreviewCfg
    I18n          *I18n
    Transform     func(string) string
}

func NewDraftStreamProcessor(p Platform, replyCtx any, opts DraftStreamOptions) *DraftStreamProcessor {
    mode := detectStreamingMode(p, opts.Display)
    return &DraftStreamProcessor{
        platform: p,
        replyCtx: replyCtx,
        mode:     mode,
        opts:     opts,
    }
}

// 根据平台能力 + 显示配置选择流式模式
func detectStreamingMode(p Platform, display DisplayCfg) StreamingMode {
    // StreamingCardPlatform → progress
    if _, ok := p.(StreamingCardPlatform); ok {
        return StreamingModeProgress
    }
    // display.Mode == "stream" + MessageUpdater → partial
    if display.Mode == "stream" {
        if _, ok := p.(MessageUpdater); ok {
            return StreamingModePartial
        }
        // stream mode 但不支持 UpdateMessage → block
        return StreamingModeBlock
    }
    // 其他模式 → off（按 DisplayMode 在 TurnProcessor 中处理）
    return StreamingModeOff
}

func (d *DraftStreamProcessor) OnThinking(content string)
func (d *DraftStreamProcessor) OnToolUse(name, input string)
func (d *DraftStreamProcessor) OnToolResult(name, result string)
func (d *DraftStreamProcessor) OnText(content string)
func (d *DraftStreamProcessor) Finalize(finalText string) (bool, error)
// 返回值: (是否已通过流式完成发送, error)
```

### 模式行为定义

```
StreamingModeOff:
  - OnText: 追加到 buffer
  - OnToolUse: 不输出（quiet/compact/full 由 TurnProcessor 决定）
  - Finalize: 发送完整 buffer

StreamingModePartial:
  - OnText: 追加到 buffer，节流更新预览消息
  - OnToolUse: freeze 预览，freeze 后不再更新
  - Finalize: 最终 UpdateMessage（如果内容变化）
  返回 true 表示"预览已处理最终消息，TurnProcessor 不需要再发"

StreamingModeBlock:
  - OnText: 追加到 buffer，当 buffer 超过阈值时发一块
  - Finalize: 发送剩余 buffer

StreamingModeProgress:
  - OnThinking: 更新进度卡片的 thinking 区域
  - OnToolUse: 添加工具步骤行
  - OnToolResult: 更新工具步骤状态
  - OnText: 更新进度卡片的文本区域
  - Finalize: 最终化卡片状态
```

### 与现有代码的关系

- `streamPreview`（`streaming.go`）继续存在，被 `StreamingModePartial` 内部使用
- `StreamingCard`（DingTalk 的 `StreamingCardPlatform`）被 `StreamingModeProgress` 使用
- `TurnProcessor`（Phase 1 提取的）使用 `DraftStreamProcessor`

### 执行步骤

```
Week 1: 定义接口 + 实现 StreamingModeOff（最简单，用于验证架构）
Week 2: 实现 StreamingModePartial（重构现有 streamPreview 集成）
Week 3: 实现 StreamingModeProgress（DingTalk AI Card 路径）
Week 4: 实现 StreamingModeBlock（分块发送 + ChunkMarkdownText 集成）
Week 5: 迁移 TurnProcessor 使用 DraftStreamProcessor
Week 6: 清理 engine.go 中的旧流式逻辑
```

---

## Phase 4：入站上下文管道增强

### 目标

借鉴 OpenClaw 的 `buildChannelInboundEventContext()`，为每条消息构建更丰富的上下文，注入到 AI 系统提示中。

### 架构设计

```go
// core/inbound_context.go（新建）

// InboundContext 包含处理一条消息所需的全部上下文
type InboundContext struct {
    Message     *Message
    
    // 平台上下文
    Platform    Platform
    PlatformCap PlatformCapabilities  // 平台能力摘要
    
    // 用户上下文（如果平台支持）
    UserInfo    *UserInfo  // nil 表示平台不支持
    
    // 频道上下文
    ChannelName string     // 频道/群组名称（如果可解析）
    
    // 历史（如果平台支持 + 用户配置）
    History     []HistoryEntry  // 最近 N 条消息
}

// UserInfo 描述消息发送者
type UserInfo struct {
    ID       string
    Name     string
    Email    string
    IsAdmin  bool
}

// BuildInboundContext 为消息构建完整上下文
func BuildInboundContext(p Platform, msg *Message, cfg InboundContextCfg) *InboundContext {
    ctx := &InboundContext{
        Message:  msg,
        Platform: p,
    }
    
    // 平台能力（来自 PlatformCapabilityDeclarer 接口，或静态探测）
    ctx.PlatformCap = detectCapabilities(p)
    
    // 频道名称（如果平台实现了 ChannelNameResolver）
    if resolver, ok := p.(ChannelNameResolver); ok {
        ctx.ChannelName, _ = resolver.ResolveChannelName(msg.ChannelID)
    }
    
    return ctx
}

// BuildSystemPromptFragment 基于 InboundContext 生成格式化指令片段
func BuildSystemPromptFragment(ctx *InboundContext) string {
    var sb strings.Builder
    
    sb.WriteString(fmt.Sprintf("Platform: %s\n", ctx.Platform.Name()))
    
    cap := ctx.PlatformCap
    if cap.MarkdownDialect != "" {
        sb.WriteString(fmt.Sprintf("Markdown format: %s\n", cap.MarkdownDialect))
    }
    if !cap.SupportsMarkdown {
        sb.WriteString("IMPORTANT: This platform does NOT render Markdown. Use plain text.\n")
    }
    if cap.MaxMessageLength > 0 {
        sb.WriteString(fmt.Sprintf("Max response length: ~%d characters per message\n", cap.MaxMessageLength))
    }
    
    return sb.String()
}
```

### 新增接口

```go
// core/interfaces.go 新增

// PlatformCapabilityDeclarer 是可选接口，平台通过此接口声明其能力。
// 未实现此接口的平台，引擎通过运行时类型断言自动推断能力。
type PlatformCapabilityDeclarer interface {
    PlatformCapabilities() PlatformCapabilities
}

// PlatformCapabilities 是平台能力的声明式摘要。
type PlatformCapabilities struct {
    MarkdownDialect    string // "standard" | "lark" | "html" | "mrkdwn" | "plain"
    SupportsMarkdown   bool
    SupportsNativeCard bool
    SupportsThread     bool
    SupportsReaction   bool
    SupportsStreaming   bool
    StreamingMode      StreamingMode
    MaxMessageLength   int
}
```

### 执行步骤

```
Week 1: 定义 InboundContext + PlatformCapabilities
Week 2: 实现 detectCapabilities（基于现有接口自动推断，作为 PlatformCapabilityDeclarer 的后备）
Week 3: 为主要平台实现 PlatformCapabilityDeclarer
Week 4: BuildSystemPromptFragment 集成到消息处理流程
Week 5: 测试：验证各平台的格式化指令是否正确
```

---

## Phase 5：平台原生富格式统一（按需推进）

### 目标

为支持原生富格式的平台（Feishu、DingTalk、Slack Block Kit）提供更完整的集成。

### 飞书增强

```go
// platform/feishu/feishu.go 增强

// FeishuMarkdownFormatter 将标准 Markdown 转换为 Lark Markdown
type FeishuMarkdownFormatter struct{}

func (f *FeishuMarkdownFormatter) Format(md string) string {
    // 标准 Markdown → Lark Markdown 的精确转换
    // - **bold** → **bold**（相同）
    // - `code` → `code`（相同）
    // - ```python → ```python（相同）
    // - [text](url) → [text](url)（相同）
    // - 表格 → Lark 表格格式
    return convertToLarkMarkdown(md)
}

// 飞书实现 PlatformCapabilityDeclarer
func (p *Platform) PlatformCapabilities() core.PlatformCapabilities {
    return core.PlatformCapabilities{
        MarkdownDialect:    "lark",
        SupportsMarkdown:   true,
        SupportsNativeCard: true,
        SupportsThread:     true,
        SupportsReaction:   true,
        SupportsStreaming:   true,
        StreamingMode:      core.StreamingModePartial,
        MaxMessageLength:   4000,
    }
}
```

### Slack Block Kit

```go
// platform/slack/slack.go 增强

// 当工具步骤较多时，使用 Block Kit 展示进度
// 而不是纯文本的 "🔧 Running Bash..."
func (p *Platform) buildProgressBlocks(steps []core.ToolStep) []slack.Block {
    // 每个 ToolStep → 一个 Section Block
}
```

### 执行步骤

这个 Phase 可以按平台并行推进，不依赖其他 Phase。但建议在 Phase 1（Engine 拆分）之后开始，因为平台代码的修改在新架构下更容易测试。

---

## 里程碑时间线

```
Month 1: Phase 1 — Engine 拆分
  - Week 1-2: 补充测试 + CardRenderer 提取
  - Week 3-4: CommandRouter + cmd_*.go 提取
  - Week 5-6: TurnProcessor 提取 + Engine 瘦身

Month 2: Phase 2 + 3 并行
  - Phase 2（Markdown 分块）：Week 1-2 完成（独立模块，快速）
  - Phase 3（Draft Stream）：Week 1-6，在新的 TurnProcessor 上构建

Month 3: Phase 4 — 入站上下文
  - Week 1-3: 实现 + 集成
  - Week 4-6: 各平台的 PlatformCapabilities 声明

Month 4+: Phase 5 — 各平台原生格式（按优先级逐平台推进）
```

## 风险和注意事项

### 高风险项

1. **TurnProcessor 提取**：这是 engine.go 中最复杂的部分，有大量共享状态（`streamPreviewToolHold`, `streamToolHoldNeedsAnswerSeparator` 等），提取时需要非常小心。
   - 缓解：在提取前写足够的集成测试（行为快照测试）

2. **流式模式迁移**：将现有的 stream/quiet/compact/full 逻辑迁移到 `DraftStreamProcessor` 时，可能会遗漏边界情况。
   - 缓解：保留旧逻辑作为 fallback，逐步切换

3. **平台兼容性**：Markdown 分块改变了发送行为，某些平台可能对多消息有不同处理。
   - 缓解：先在测试环境验证每个平台的行为

### 不应该做的事

- **不要**在未添加测试的情况下重构关键路径
- **不要**在单次 PR 中合并多个 Phase 的改动
- **不要**为了"架构上更美观"而改变用户可见的行为
- **不要**打破现有的平台接口（`core/interfaces.go` 只增不改）

## 成功指标

重构完成后的目标状态：

| 指标 | 当前 | 目标 |
|------|------|------|
| engine.go 行数 | 13,820 | < 3,000 |
| 最大单文件行数 | 13,820 | < 800 |
| 代码块截断 bug | 存在 | 消除 |
| 流式体验一致性 | 各平台不同 | 统一策略 |
| 添加命令时间 | 2 小时 | 30 分钟 |
| 添加平台时间 | 1 周 | 2-3 天 |
| 测试覆盖率（core/） | ~40% | > 70% |
