# 企微 WebSocket Stream 工具态收敛优化

## 背景

在企业微信 WebSocket 长连接场景中，`[display].mode = "stream"` 时，ACP 类 agent 会同时流出两类事件：

- `agent_message_chunk`：正常文本流
- `tool_call` / `tool_call_update`：工具调用与工具参数逐步拼接过程

ACP 的 `tool_call_update.rawInput` 会以极高频率逐字增长，例如：

- `wc`
- `wc -m`
- `wc -m /Users/...`
- `# 统计 README.md 行数、词数、字符数\nwc -m -w -l ...`

如果这些中间态直接进入企微 stream 预览，就会出现两个问题：

1. 工具中间态会被频繁推送，用户看到大量无意义的半成品命令。
2. 工具块和 `agent_message_chunk` 文本会交错，最终表现为“正文像是落进了工具框里”。

这类问题主要发生在：

- 平台：`wecom` WebSocket
- 展示模式：`stream`
- Agent：ACP / CodeBuddy / 其他会持续发 `tool_call_update` 的适配器

## 本次优化目标

本次优化不是改变所有平台的行为，而是精准收敛到一个很小的范围：

- 只影响 `wecom + websocket + stream` 场景
- 其他平台行为不变
- `full / compact / quiet` 等 display mode 不变

目标语义如下：

1. 文本仍然保持流式推送。
2. 工具的中间态不立即推送到企微。
3. 同一个工具如果输入不断变化，只保留“最后一个工具状态”。
4. 当后续正文文本到来时，把“最后一个工具状态 + 当前正文”一起更新到同一个 stream 消息。
5. 如果一个 turn 最后停在工具态结束，也要把最终工具态冲刷出去，不能丢。

## 问题根因分析

### 1. ACP 侧并没有把工具中间态暴露成聊天事件

ACP 映射层已经对 `tool_call_update` 的 `in_progress` 状态做了抑制：

- 文件：[agent/acp/mapping.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/agent/acp/mapping.go)

`mapToolCallUpdate()` 中，`in_progress` / `pending` 直接返回 `nil`，不会作为 IM 可见事件发出。

所以问题不在 ACP 映射层“又把中间态发给聊天”。

### 2. stream 模式原本会把工具消息直接插入 preview 文本

原始引擎逻辑里，`displayModeStream` 且 `ToolMessages=true` 时：

- `EventToolUse` 会立即拼接成 `🔧 Tool #...` 文本
- `EventToolResult` 也会立即拼接进 stream preview

位置：

- [core/engine.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/core/engine.go)

这意味着只要 adapter 发出了工具相关事件，默认 stream 预览就会“立刻展示”。

### 3. 企微 WebSocket stream 是“整段内容全量替换”语义

企微 WebSocket 的 `aibot_respond_msg` 不是 append 流，而是 full-replacement 流：

- 每次发的 `stream.content` 都会替换当前内容

位置：

- [platform/wecom/websocket.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/platform/wecom/websocket.go)

因此如果 preview 层持续把工具态文本插进去，企微端就会稳定展示这些“半成品内容”。

## 方案设计

### 总体策略

本次方案分两层：

1. `core` 层增加一个可选的 stream-preview 语义扩展点。
2. `wecom` 平台声明自己使用 `tool_hold` 语义，并在平台 stream 队列里实现“工具暂存 + 文本触发冲刷”。

这样做的原因：

- 不污染其他平台默认行为
- 平台可以声明“自己要怎样理解 stream preview”
- 逻辑边界清晰，后续如果别的平台也要类似语义，可以复用同一个扩展点

### 方案语义

对于 `wecom` 的 `tool_hold` stream 语义：

1. 纯文本流正常推送。
2. 纯工具块先 hold，不发到企微。
3. 同一阶段如果工具块反复更新，只覆盖 hold 的最后一个版本。
4. 一旦后续有正文文本到来，就把：

   `之前已发送文本 + 最后一个工具块 + 新正文`

   聚合成一个新的 stream 内容推送。

5. 如果 turn 结束时仍然只有工具块，也会在 finalize 阶段把 hold 的工具块刷出去。

## 实现明细

### 1. core 增加平台级 stream preview mode 扩展点

新增接口：

- 文件：[core/interfaces.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/core/interfaces.go)

```go
type StreamPreviewModeProvider interface {
    StreamPreviewMode() string
}
```

目前约定：

- 空字符串 / 未实现：默认行为
- `tool_hold`：工具中间态不直接出现在 stream preview 中

### 2. streamPreview 记录平台的 preview mode

位置：

- [core/streaming.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/core/streaming.go)

新增字段和方法：

- `streamPreview.mode`
- `previewMode()`

`newStreamPreview()` 会读取平台是否实现 `StreamPreviewModeProvider`。

### 3. wecom 平台声明使用 `tool_hold`

位置：

- [platform/wecom/websocket.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/platform/wecom/websocket.go)

新增：

```go
func (p *WSPlatform) StreamPreviewMode() string { return "tool_hold" }
```

这保证了只有企微 WebSocket 走这套语义。

### 4. engine 在 stream 模式下跳过企微工具中间态的直接插入

位置：

- [core/engine.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/core/engine.go)

在 `processInteractiveEvents()` 中新增判断：

- `streamPreviewToolHold := sp.previewMode() == "tool_hold" && e.display.Mode == displayModeStream`

然后在：

- `EventToolUse`
- `EventToolResult`

的 stream 分支中直接 `continue`，不再把工具文本立刻 append 到 stream preview。

这样做的意义是：

- 文本事件继续流式
- 工具事件不再走旧的“即时插入 preview 文本”路径

### 5. wecom WebSocket 队列实现工具态暂存与冲刷

位置：

- [platform/wecom/websocket.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/platform/wecom/websocket.go)

新增结构：

- `wsContentAggregator`
- `wsStreamState.heldTool`

关键语义：

#### `heldTool`

- 保存“最近一次未发送的工具块”
- 后来的工具块会覆盖之前的工具块

#### `wsContentAggregator`

负责生成最终可发送的 stream 内容：

- `plainSegments`：已经确认要展示的正文段
- `pendingTool`：即将随下一段正文一起显示的工具块

关键方法：

- `ingest(content)`
- `finalize(content)`
- `render()`

### 6. stream 队列按语义分流

`enqueueLatestStreamSend()` 和 `runStreamQueue()` 做了两个关键改动：

#### 6.1 工具块先 hold

当收到的是“纯工具块”且是非 finish 更新时：

- 不立即发 websocket frame
- 只更新 `heldTool`

因此你现在看到的“完全没有任何推送”其实说明：

- 旧逻辑的工具中间态已经成功被拦住了
- 但正文触发冲刷的路径还需要依赖后续文本事件正确到达

#### 6.2 正文到来时把 hold 的工具块并入

当后续正文到来时：

- 如果有 `heldTool`
- 会先把它转成 aggregator 的 `pendingTool`
- 再把正文 `ingest()` 进去
- 最终得到：`旧正文 + 工具块 + 新正文`

#### 6.3 finalize 时兜底冲刷

如果 turn 结束时 still held：

- `finalize()` 会把工具块输出到最终 stream 内容里

避免“最后只停在工具态，结果完全看不到工具”的问题。

## 为什么你看到“没有任何变化”

从你提供的日志看：

- `tool_call_update` 缓存还在正常发生
- `acp: session/prompt done` 和 `turn complete` 也正常
- 但企微端“没有任何推送”

这符合当前修复中途阶段的一个典型现象：

- 工具中间态已经被正确 hold 住了
- 但正文流进入 preview 更新路径时，没有把 held tool 正确合并发送

这个阶段的现象不是“修复无效”，而是“只做对了一半”：

- 抑制中间态：成功
- 正文触发最终聚合发送：还需要继续打通

也正因如此，后续增加了更严格的单测来覆盖：

1. 工具-only 更新阶段不应发任何 frame
2. 正文到来时应只发一条聚合后的 frame
3. 文本先流式、工具 hold、后续文本再聚合的路径必须成立

## 测试覆盖

主要新增 / 维护的测试在：

- [platform/wecom/websocket_test.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/platform/wecom/websocket_test.go)

重点覆盖：

1. `TestWSContentAggregator_KeepsOnlyLatestPendingTool`
   验证同一个工具多次变化时，只保留最后一个工具状态。

2. `TestWSContentAggregator_FinalizeFlushesPendingTool`
   验证 turn 结束时工具态能被刷出去。

3. `TestSendStreamFrameAndWaitAck_AggregatesPendingToolPreview`
   验证纯工具更新阶段不发 frame，正文到来时一次性发最终工具块 + 正文。

4. `TestSendStreamFrameAndWaitAck_TextStillStreamsWhileToolIsHeld`
   验证“文本仍然流式”这一点没有丢。

同时也回归验证了原有不该受影响的场景：

- `TestWSPreviewStartAndUpdate_ReuseSameStreamID`
- `TestSendStreamFrameAndWaitAck_SerializesConcurrentUpdates`
- `TestSendStreamFrameAndWaitAck_LatestWinsPendingPreview`

## 当前验证命令

已通过的相关验证命令：

```bash
GOCACHE=$(pwd)/.gocache go test ./platform/wecom ./agent/acp
```

补充回归：

```bash
GOCACHE=$(pwd)/.gocache go test ./agent/acp ./core -run 'TestGetOrCreateInteractiveStateWith_ACPNewSessionDoesNotReuseStaleCurrentID|TestHandleMessage_AutoResetOnIdle_DoesNotRotateFreshSession|TestHandleMessage_AutoResetOnIdle_DoesNotTriggerForSlashCommand'
```

说明：

- `core` 的全量测试里有与本次改动无关的既有失败项，因此本次没有把它作为交付门槛。
- 与本次企微 stream 工具态优化直接相关的模块测试已通过。

## 后续排查建议

## 本次追加修复

从实际日志看，除了 stream 工具态问题，还暴露出一个 ACP session 绑定问题：

- `/new` 后 session 的 `AgentSessionID` 已经清空；
- 但新建的 `acpSession` 在构造时会先把 `acpSessID` 设成传入的 `resumeSessionID`；
- 当旧值残留在内存态 / 事件缓冲中时，`CurrentSessionID()` 可能在新 turn 早期返回过期 ID；
- engine 因此误判当前 interactive state 与 active session 不一致，触发 recycle；
- recycle 后又把旧的 buffered events drain 掉，于是你看到：
  - `drained stale events from previous turn`
  - `interactive session mismatch, recycling`
  - 企微侧没有新内容落出去

对应修复：

1. `newACPSession()` 中不再把 `ContinueSession` 哨兵或空 resume 值直接灌进 `acpSessID`。
2. 增加 `core` 回归测试，验证 ACP 新 session 场景下不会因为 stale current ID 误复用 / 误回收。

相关文件：

- [agent/acp/session.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/agent/acp/session.go)
- [core/engine_test.go](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/core/engine_test.go)

如果线上仍有“企微没有推送”的现象，优先看三类信号：

1. 是否进入了 `stream` 模式。
2. 是否确实有 `agent_message_chunk` 文本事件到来。
3. `wecom-ws` 最终是否发出了 `aibot_respond_msg` frame。

建议结合下面的固定日志脚本进行回放和排查。
