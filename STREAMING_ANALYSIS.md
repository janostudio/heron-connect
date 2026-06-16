# CC-Connect 流式功能探索分析报告

生成时间: 2026-06-01
探索深度: Very Thorough

---

## 执行摘要

**当前状态**: ✅ 已具备**部分端到端流式输出能力**

该项目实现了**流式消息预览** (Streaming Preview) 系统，支持消息的实时增量更新。但这**不是 HTTP 流式响应** (SSE/chunked)，而是**应用层消息编辑能力**。

### 核心发现
- ✅ **流式文本预览**: 完整实现，支持节流的增量消息更新
- ✅ **平台级消息编辑**: 多平台支持 (Feishu、Discord、Telegram 等)
- ✅ **流式卡片**: DingTalk AI Card 具备完整的流式更新能力
- ⚠️ **部分能力**: 某些平台不支持消息编辑，降级为普通消息
- ❌ **HTTP 流式**: 无 SSE/WebSocket streaming 响应
- ❌ **增量事件**: 协议层不支持部分 (partial) 响应

---

## 详细分析

### 1. Bridge 包流式能力

#### 1.1 WebSocket 协议

**文件**: `bridge/bridge.go`

```go
// 支持的消息类型
type bridgeMsg struct {
    Type string `json:"type"` // message, card_action, preview_ack 等
}

type bridgePreviewAck struct {
    Type          string `json:"type"`     // "preview_ack"
    RefID         string `json:"ref_id"`   // 预览请求 ID
    PreviewHandle string `json:"preview_handle"` // 平台特定的消息句柄
}
```

**流式相关消息类型:**
- `"preview_start"` - 启动流式预览
- `"update_message"` - 更新现有消息
- `"delete_message"` - 删除预览消息
- `"typing_start"` / `"typing_stop"` - 打字指示器

**特性:**
- ✅ 双向 WebSocket 连接
- ✅ 预览消息请求/应答模式 (Request/ACK)
- ✅ 消息编辑能力声明 (capability: "update_message", "preview")
- ❌ 无原生 HTTP 流式响应 (SSE/chunked)

#### 1.2 BridgePlatform 实现

**关键方法** (`bridge/bridge.go` L554-631):

```go
// 1. 消息编辑
func (bp *BridgePlatform) UpdateMessage(ctx context.Context, replyCtx any, content string) error {
    // 检查平台是否支持 "update_message" capability
    // 发送 update_message 消息给适配器
}

// 2. 流式预览启动
func (bp *BridgePlatform) SendPreviewStart(ctx context.Context, replyCtx any, content string) (previewHandle any, err error) {
    // 1. 生成唯一的 ref_id
    // 2. 创建响应通道等待 preview_ack
    // 3. 发送 preview_start 消息
    // 4. 等待 ACK 超时 10s
    // 5. 返回 newBridgeReplyCtx 作为后续编辑的句柄
}

// 3. 删除预览
func (bp *BridgePlatform) DeletePreviewMessage(ctx context.Context, previewHandle any) error {
    // 检查 "delete_message" capability
    // 发送删除消息
}
```

**能力检测:**
```go
func bridgeProgressStyleForAdapter(a *bridgeAdapter) string {
    // 根据 adapter capabilities 推导出 progress_style:
    // - 如果支持 preview + update_message + card → "card"
    // - 如果支持 preview + update_message → "compact"
    // - 否则 → "legacy"
}
```

---

### 2. Core 包流式设计

#### 2.1 StreamPreviewCfg - 流式配置

**文件**: `core/streaming.go` L12-29

```go
type StreamPreviewCfg struct {
    Enabled           bool     // 全局开关
    DisabledPlatforms []string // 禁用列表 (如 "feishu")
    IntervalMs        int      // 最小更新间隔 (默认 1500ms)
    MinDeltaChars     int      // 最小新字符阈值 (默认 30)
    MaxChars          int      // 预览最大长度 (默认 2000)
}
```

#### 2.2 streamPreview - 节流状态机

**文件**: `core/streaming.go` L34-527

这是核心的流式实现！

```go
type streamPreview struct {
    mu sync.Mutex
    
    // 配置
    cfg       StreamPreviewCfg
    platform  Platform
    replyCtx  any
    ctx       context.Context
    transform func(string) string // 内容转换函数 (如路径替换)
    
    // 状态追踪
    fullText          string        // 累积的完整文本
    lastSentText      string        // 上次发送的文本
    lastSentAt        time.Time     // 上次发送时间
    lastSentViaUpdate bool          // 是否通过 UpdateMessage 发送
    previewMsgID      any           // 平台特定的消息 ID
    degraded          bool          // 降级标志（平台不支持时）
    
    // 定时器管理
    timer     *time.Timer          // 节流定时器
    timerStop chan struct{}         // 停止信号
    
    // 卡片状态
    pendingStatus CardStatus        // 待应用的卡片状态
    mode          string            // 预览模式
}
```

**核心方法 - 文本追加和节流:**

```go
// 1. 追加文本（节流）
func (sp *streamPreview) appendText(text string) {
    sp.mu.Lock()
    sp.fullText += text
    
    // 计算 delta
    delta := len([]rune(displayText)) - len([]rune(sp.lastSentText))
    elapsed := time.Since(sp.lastSentAt)
    interval := time.Duration(sp.cfg.IntervalMs) * time.Millisecond
    
    if delta < sp.cfg.MinDeltaChars && !sp.lastSentAt.IsZero() {
        sp.scheduleFlushLocked(interval)  // 延迟发送
        return
    }
    if elapsed < interval && !sp.lastSentAt.IsZero() {
        sp.scheduleFlushLocked(remaining)
        return
    }
    
    sp.flushLocked(displayText)  // 立即发送
}

// 2. 立即发送（用于工具调用开始等高优先级事件）
func (sp *streamPreview) appendTextNow(text string) {
    sp.mu.Lock()
    sp.fullText += text
    sp.cancelTimerLocked()
    sp.flushLocked(displayText)
}

// 3. 实际发送逻辑
func (sp *streamPreview) flushLocked(text string) {
    if sp.previewMsgID == nil {
        // 第一次：使用 SendPreviewStart
        if starter, ok := sp.platform.(PreviewStarter); ok {
            handle, err := starter.SendPreviewStart(sp.ctx, sp.replyCtx, text)
            sp.previewMsgID = handle
        }
    } else {
        // 后续：使用 UpdateMessage
        updater, ok := sp.platform.(MessageUpdater)
        updater.UpdateMessage(sp.ctx, sp.previewMsgID, text)
    }
}

// 4. 完成时的处理
func (sp *streamPreview) finish(finalText string) bool {
    // 检查是否需要 PreviewFinalizer (不同的编辑 API)
    if finalizer, ok := sp.platform.(PreviewFinalizer); ok {
        finalizer.FinalizePreviewMessage(sp.ctx, sp.previewMsgID, finalText)
        return true
    }
    
    // 否则用 UpdateMessage
    updater.UpdateMessage(sp.ctx, sp.previewMsgID, finalText)
    return true
}
```

**平台接口需求:**
```go
// 必选
type PreviewStarter interface {
    SendPreviewStart(ctx context.Context, replyCtx any, content string) (previewHandle any, err error)
}

// 必选
type MessageUpdater interface {
    UpdateMessage(ctx context.Context, replyCtx any, content string) error
}

// 可选
type PreviewCleaner interface {
    DeletePreviewMessage(ctx context.Context, previewHandle any) error
}

// 可选
type PreviewFinalizer interface {
    FinalizePreviewMessage(ctx context.Context, preplyHandle any, content string) error
}

// 可选
type PreviewStatusUpdater interface {
    SetPreviewStatus(previewHandle any, status CardStatus)
}
```

#### 2.3 Event 事件流

**文件**: `core/message.go` L161-200

```go
type EventType string

const (
    EventText              EventType = "text"               // ✓ 流式文本
    EventToolUse           EventType = "tool_use"           
    EventToolResult        EventType = "tool_result"        
    EventResult            EventType = "result"             
    EventError             EventType = "error"              
    EventPermissionRequest EventType = "permission_request" 
    EventThinking          EventType = "thinking"           
)

type Event struct {
    Type         EventType
    Content      string  // 对于 EventText，这是增量文本片段
    // 其他字段...
}
```

**流式特点:**
- ❌ 无 "delta" 或 "partial" 字段来标记增量
- ✅ EventText 的 Content 字段通常是增量片段 (Agent 按块发送)
- 核心 Engine 累积这些片段并推送到 streamPreview

#### 2.4 Engine 中的流式处理

**文件**: `core/engine_turn.go` L217, L413, L827-890

```go
// L413: 为每个 turn 创建 streamPreview
sp := newStreamPreview(e.streamPreview, state.platform, state.replyCtx, e.ctx, workspaceRenderer)

// L827-890: EventText 处理
case EventText:
    if event.Content != "" && !isEllipsisOnly(event.Content) {
        if streamCard != nil && !streamCard.Failed() {
            // DingTalk 流式卡片路径
            cardAnswerText.WriteString(event.Content)
            _ = streamCard.Update(e.ctx, buildCardContent(...))
        } else {
            // 普通流式预览路径
            textParts = append(textParts, event.Content)
            partialText += event.Content
            
            // 检查是否应该发送预览
            if sp.canPreview() {
                sp.appendText(event.Content)  // 节流添加
            }
        }
    }
```

**两条流式路径:**
1. **StreamingCardPlatform 路径** (DingTalk):
   - 为每个 turn 创建一张流式卡片
   - 聚合所有事件内容到单条可编辑消息
   
2. **streamPreview 路径** (其他平台):
   - 逐个 EventText 累积
   - 使用节流定时器定期更新

---

### 3. 具体平台流式实现

#### 3.1 Feishu (飞书)

**文件**: `platform/feishu/feishu.go` L3443-3610

```go
// SendPreviewStart: 发送初始卡片消息
func (p *Platform) SendPreviewStart(ctx context.Context, rctx any, content string) (any, error) {
    // 1. 如果是卡片 JSON，直接使用；否则构建预览卡片
    var cardJSON string
    if isCardJSON(content) {
        cardJSON = content
    } else {
        cardJSON = buildPreviewCardJSON(content)
    }
    
    // 2. 使用 Reply API 或 Create API 发送消息
    resp, err := client.Im.Message.Reply(ctx, req, options...)
    // 或
    resp, err := client.Im.Message.Create(ctx, req, options...)
    
    // 3. 返回消息 ID 作为句柄
    return &feishuPreviewHandle{messageID: msgID, chatID: chatID}, nil
}

// UpdateMessage: 编辑现有卡片 (使用 PATCH API)
func (p *Platform) UpdateMessage(ctx context.Context, previewHandle any, content string) error {
    h := previewHandle.(*feishuPreviewHandle)
    
    // 使用 PATCH API 更新卡片内容
    req := larkim.NewPatchMessageReqBuilder().
        MessageId(h.messageID).
        Body(larkim.NewPatchMessageReqBodyBuilder().
            Content(content).
            Build()).
        Build()
    
    return client.Im.Message.Patch(ctx, req)
}

// DeletePreviewMessage: 删除预览消息
func (p *Platform) DeletePreviewMessage(ctx context.Context, previewHandle any) error {
    h := previewHandle.(*feishuPreviewHandle)
    req := larkim.NewDeleteMessageReqBuilder().
        MessageId(h.messageID).
        Build()
    return client.Im.Message.Delete(ctx, req)
}
```

**支持情况**: ✅ 完整支持

#### 3.2 Discord

**文件**: `platform/discord/discord.go` L1078-1119

```go
// SendPreviewStart
func (p *Platform) SendPreviewStart(ctx context.Context, rctx any, content string) (any, error) {
    msg := buildDiscordPreviewMessage(content)
    sent, err := p.session.ChannelMessageSendComplex(channelID, msg)
    return &discordPreviewHandle{channelID: channelID, messageID: sent.ID}, nil
}

// UpdateMessage
func (p *Platform) UpdateMessage(ctx context.Context, previewHandle any, content string) error {
    h := previewHandle.(*discordPreviewHandle)
    _, err := p.session.ChannelMessageEditComplex(buildDiscordPreviewEdit(h.channelID, h.messageID, content))
    return err
}

// DeletePreviewMessage
func (p *Platform) DeletePreviewMessage(ctx context.Context, previewHandle any) error {
    h := previewHandle.(*discordPreviewHandle)
    return p.session.ChannelMessageDelete(h.channelID, h.messageID)
}
```

**支持情况**: ✅ 完整支持

#### 3.3 DingTalk (钉钉) - 流式卡片

**文件**: `platform/dingtalk/card.go`

这是**最高级**的流式实现！

```go
// aiCard 实现 core.StreamingCard 接口
type aiCard struct {
    cardInstanceId string
    outTrackId     string
    platform       *Platform
    
    mu              sync.Mutex
    state           string  // "processing" | "finished" | "failed"
    pendingContent  string
    timer           *time.Timer
    inFlight        bool    // 单次飞行控制
}

// Update: 使用最新胜利 (latest-wins) 语义的节流
func (c *aiCard) Update(ctx context.Context, content string) error {
    c.mu.Lock()
    
    c.pendingContent = content
    
    if c.inFlight {
        // 请求进行中，记录待处理内容
        c.scheduleFlushLocked()
        c.mu.Unlock()
        return nil
    }
    
    if time.Since(c.lastSentAt) >= time.Duration(c.throttleMs)*time.Millisecond {
        // 已等待足够长时间，立即发送
        c.mu.Unlock()
        c.flush(ctx)
        return nil
    }
    
    // 否则调度定时器
    c.scheduleFlushLocked()
    c.mu.Unlock()
    return nil
}

// doStream: 调用 DingTalk streaming API
func (c *aiCard) doStream(ctx context.Context, content string, isFinalize bool) error {
    payload := map[string]any{
        "outTrackId": c.outTrackId,
        "key":        c.templateKey,      // 卡片模板变量名
        "content":    content,
        "isFull":     true,               // 完整更新 (非增量)
        "isFinalize": isFinalize,
        "isError":    false,
        "guid":       generateGUID(),
    }
    
    // PUT https://api.dingtalk.com/v1.0/card/streaming
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("x-acs-dingtalk-access-token", token)
    
    resp, err := p.httpClient.Do(req)
    return err
}

// Finalize: 标记完成并发送最终内容
func (c *aiCard) Finalize(ctx context.Context, content string) error {
    c.mu.Lock()
    // ... 发送最终内容并设置 isFinalize=true
    return c.doStream(ctx, content, true)
}
```

**DingTalk 流式特点**:
- ✅ **流式卡片**: 从创建到完成始终编辑同一条消息
- ✅ **节流**: 单次飞行控制 + latest-wins 语义
- ✅ **最新胜利**: 进行中时接收的新内容不会丢失，会在当前请求完成后发送
- ❌ **增量更新**: API 要求 "isFull": true，不支持增量 (delta)
- 配置: `card_template_id`, `card_template_key` (默认 "content"), `card_throttle_ms` (默认 1000)

**创建 AI Card**:
```go
func (p *Platform) CreateStreamingCard(ctx context.Context, replyCtx any) (core.StreamingCard, error) {
    // 检查 card_template_id 是否配置
    // 调用 /v1.0/card/instances/createAndDeliver 创建卡片
    // 返回 *aiCard 实例
    
    payload := map[string]any{
        "cardTemplateId": p.cardTemplateID,
        "outTrackId":     outTrackId,
        "cardData": map[string]any{
            "cardParamMap": cardParamMap,
        },
        "callbackType": "STREAM",  // 关键：标记为流式卡片
        "imGroupOpenSpaceModel": {...},
        "openSpaceId": fmt.Sprintf("dtv1.card//IM_GROUP.%s", rc.conversationId),
    }
}
```

**支持情况**: ✅ 完整的流式卡片支持

---

### 4. 已实现能力总结

#### ✅ 已有流式能力

| 能力 | 实现情况 | 平台 | 备注 |
|------|---------|------|------|
| **消息编辑预览** | ✅ 完整 | Feishu, Discord, Telegram, Slack | streamPreview 状态机 |
| **节流管理** | ✅ 完整 | 所有 | 最小间隔 + 最小字符阈值 |
| **流式卡片** | ✅ 完整 | DingTalk (需配置 card_template_id) | 从创建到完成编辑同一消息 |
| **打字指示器** | ✅ 完整 | Bridge, Discord, Telegram 等 | TypingIndicator 接口 |
| **完成反应** | ✅ 完整 | Feishu, Bridge 等 | TypingIndicatorDone 接口 |
| **消息删除** | ✅ 完整 | Feishu, Discord, Bridge | PreviewCleaner 接口 |
| **进度卡片** | ✅ 完整 | 多数平台 | RichCard + MessageUpdater |
| **Bridge WebSocket** | ✅ 完整 | Bridge 平台 | preview_start, update_message, delete_message |
| **节流 + 最新胜利** | ✅ 完整 | DingTalk | aiCard 实现 |

#### ⚠️ 部分能力

| 能力 | 状态 | 说明 |
|------|------|------|
| **平台支持差异** | ⚠️ 降级处理 | 某些平台不支持消息编辑，自动降级为普通消息 |
| **流式配置** | ⚠️ 有配置 | 但某些平台在禁用列表中 (如 Feishu 默认不用流式预览?) |

#### ❌ 缺失能力

| 能力 | 原因 | 可能的实现方向 |
|------|------|---|
| **HTTP SSE** | 设计决策 | 不是 REST 流式，而是应用层编辑 |
| **Chunked Transfer** | 设计决策 | 同上 |
| **增量响应** | API 限制 | 平台大多要求全量更新，如 DingTalk "isFull": true |
| **流式工具输出** | 部分支持 | EventText 是增量的，但 EventToolResult 通常是整体 |
| **流式思考** | 部分支持 | EventThinking 存在，但输出方式和 EventText 一样 |

---

### 5. 关键设计细节

#### 5.1 节流策略

```go
IntervalMs = 1500        // 最小 1.5 秒间隔
MinDeltaChars = 30       // 最少 30 个新字符
MaxChars = 2000          // 预览最长 2000 字符
```

**触发条件** (任一满足):
- 距上次发送已过 1.5 秒 AND 有新字符
- 新增字符 >= 30 且已等待至少部分间隔

#### 5.2 降级机制

```go
// 如果平台不支持 UpdateMessage
if !a.capabilities["update_message"] {
    return core.ErrNotSupported
}

// streamPreview 检测
if !sp.canPreview() {
    // 直接发送消息，不使用预览
    return false
}
```

#### 5.3 多路径设计

```
Agent Output Events (EventText, EventThinking, EventToolUse, ...)
    ↓
┌──────────────────────────────────────┐
│     Engine: processTurnEvents()      │
└──────────────────────────────────────┘
    ↓                           ↓
    ├─ StreamingCardPlatform   ├─ 其他平台
    │  (DingTalk AI Card)       │
    │  ↓                        │ ↓
    │  streamingCard.Update()   │ streamPreview.appendText()
    │                           │     (节流)
    │                           │
    │  ↓ (on finish)            │ ↓ (最后)
    │  streamingCard.Finalize() │ streamPreview.finish()
    │                           │     ↓
    └──────────────────────────┴─ platform.UpdateMessage()
                                   └─ platform.SendPreviewStart()
```

#### 5.4 进度样式推导

```go
if adapter.capabilities["preview"] && adapter.capabilities["update_message"] {
    if adapter.capabilities["card"] {
        progressStyle = "card"        // 使用卡片式进度
    } else {
        progressStyle = "compact"     // 使用紧凑式
    }
} else {
    progressStyle = "legacy"          // 降级为传统模式
}
```

---

### 6. 代码质量评估

#### 优点
- ✅ 接口驱动设计，易于扩展新平台
- ✅ 节流逻辑清晰，防止消息炸弹
- ✅ 降级处理完善，提高可靠性
- ✅ 单元测试覆盖完整 (streaming_test.go)
- ✅ 并发安全 (sync.Mutex, atomic ops)

#### 可改进之处
- ⚠️ 预览模式 (`streamPreviewMode`, `tool_hold`) 文档不足
- ⚠️ DingTalk 流式卡片配置复杂 (需要 template_id)
- ⚠️ 缺少全链路流式 (HTTP 层面) 实现
- ⚠️ 某些平台的 streaming 能力声明可能不完整

---

### 7. 配置示例

```toml
# 全局流式预览配置
[stream_preview]
enabled = true            # 启用流式预览
interval_ms = 1500        # 更新最小间隔
min_delta_chars = 30      # 最小新字符
max_chars = 2000          # 预览最大长度

# 禁用特定平台的流式预览
# disabled_platforms = ["feishu"]

# DingTalk 流式卡片 (需配置)
[[projects.platforms]]
type = "dingtalk"

[projects.platforms.options]
card_template_id = "your-template-id"  # 必需
card_template_key = "content"           # 卡片变量名
card_throttle_ms = 1000                 # 卡片更新间隔

# Feishu
[[projects.platforms]]
type = "feishu"
# 自动支持流式预览，需平台配置 message update 权限

# Discord
[[projects.platforms]]
type = "discord"
# 自动支持流式预览
```

---

## 整体评估

### 流式能力总分: 7/10

#### 已实现方面 (得分高)
- **应用层流式**: 通过消息编辑实现的流式预览非常完善
- **多平台支持**: 覆盖主流平台 (Feishu、Discord、Telegram、DingTalk)
- **节流设计**: 防止消息轰炸的策略周密
- **可靠性**: 完善的降级机制确保在不支持的环境下仍能工作

#### 缺失方面 (得分减低)
- **HTTP 流式**: 无 SSE/chunked 实现，无端到端的流式字节响应
- **增量协议**: 平台 API 通常要求全量更新，难以传达 delta
- **完整文档**: 流式能力的配置和使用文档不够详细
- **灵活性**: 流式更新间隔和阈值是全局配置，无法按消息类型灵活调整

### 适用场景

✅ **适合**:
- 长文本响应场景 (解决方案、代码审查等)
- 工具调用进度展示
- 需要实时反馈的多步骤任务

❌ **不适合**:
- 需要原始 HTTP SSE 流的应用
- 需要客户端流式解析的场景
- 要求字节级增量响应的系统

---

## 建议和改进方向

### 短期改进
1. **完善文档**: 详细说明 `stream_preview` 和 `streamPreviewMode` 配置
2. **DingTalk 简化**: 提供默认的 card_template_id，降低配置复杂度
3. **监控指标**: 增加流式更新的计数和延迟监控

### 中期改进
1. **按消息类型的节流**: 允许工具消息和文本消息有不同的节流策略
2. **客户端流式**:  为某些高级用户提供 WebSocket 客户端 API 来接收流式事件
3. **状态机可视化**: 提供调试工具显示预览的生命周期

### 长期改进
1. **HTTP 2.0 服务器推送**: 利用 HTTP/2 为特定平台提供原生流式
2. **QUIC 优化**: 对延迟敏感的场景
3. **边缘流式**: 支持消息分片和边缘计算场景

---

## 文件清单

### 核心流式实现文件
- `core/streaming.go` - streamPreview 状态机 (主要)
- `core/streaming_test.go` - 单元测试
- `core/interfaces.go` - 平台接口定义
- `core/engine_turn.go` - 事件处理和流式触发

### 平台实现
- `platform/feishu/feishu.go` - Feishu 消息编辑
- `platform/discord/discord.go` - Discord 消息编辑
- `platform/dingtalk/card.go` - DingTalk 流式卡片 (aiCard)
- `bridge/bridge.go` - Bridge WebSocket 协议

### 配置
- `config.example.toml` - 配置示例和文档

---

## 结论

cc-connect 项目**已具备端到端的应用层流式输出能力**，但这是通过**消息编辑** (in-place updates) 而非**HTTP 流式响应** (SSE/chunked) 实现的。

- ✅ **流式文本预览**: 完整、可靠、经过充分测试
- ✅ **流式卡片**: DingTalk 支持高级流式卡片
- ⚠️ **平台依赖**: 能力取决于平台 API 对消息编辑的支持
- ❌ **HTTP 流式**: 如需真正的 HTTP 流式响应，需要另外实现

**核心优点**: 设计灵活、降级完善、易于扩展
**主要局限**: 受平台 API 限制、增量能力不足

