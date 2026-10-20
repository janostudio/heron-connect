# Heron AI 过程事件流接入设计（修订稿 v2）

**状态：** 待评审
**范围：** Heron AI JSON-RPC（`heron --json-rpc`）与 heron-connect Heron adapter
**修订说明：** v2 按"不做 token 级流式，只透出每一轮 flow/team/call/tool 的完整数据"重写。v1 的目标偏窄（只透工具事件），且事件投影沿用了内部 Go 模型的形状。v2 改为**以 heron-connect 的 `core.Event` 语义为对外契约**（字段命名同义，adapter 只做字段搬运），内部事件只是数据源。**不追求跨 CLI 的结构兼容**：claudecode / codex exec / codex appserver / acp 四套线格式互不相同，heron-connect 本就靠各自的 adapter 映射到同一个 `core.Event`，向任何一家靠拢都不会更通用，只会多引入一套私有结构。

---

## 1. 背景与现状

heron-connect（`agent/heron/session.go`）通过 stdio 启动 `heron --json-rpc`，只发 `turn` 请求；heron-ai（`cmd/server/jsonrpc.go`）只实现了 `turn` 与 `approve` 两个方法，且**从不发送任何通知**（grep 全仓无 progress 发送代码）。adapter 虽在 `handleNotification` 里处理 `method=progress`，但只会读 `params.content` 并发 `EventThinking`。

由此产生的三个现状问题：

1. **过程不可见**：一次 turn 期间只有终态，多个 team/call/tool 的执行过程完全丢失。
2. **等待态被误判为失败**：`handleResponse`（session.go:238-278）不读 `status` 与 `pending_approvals`，会话停在 `waiting_approval` 且 reply 为空时直接发 `EventError("model returned an empty result")`。
3. **无过程帧**：`heron --output-format stream-json`（`cmd/server/streamjson.go`）名不副实 —— 它是 HTTP 客户端模式（必须另起 `--serve`），一次输入只吐一帧 `{"type":"result",...}`，没有任何过程事件。注意这不意味着要去模拟某家 CLI 的 stream-json 结构，见 §4.1。

heron-ai 内部已把 Flow / Team / Agent 三层 `SessionEvent` 持久化到三份 jsonl，`seq` 在会话内跨层全局单调，`Subscribe(afterSeq)` 带历史回放（`internal/storage/session.go:135-186`）。事件内容本就覆盖团队、调用、工具、模型回复四个粒度，**不需要新增采集点，只需要一个投影层**。

## 2. 目标与非目标

### 2.1 目标

- 一个 turn 执行期间，按发生顺序透出四类**粒度完整**的数据：flow turn 边界、team turn 边界、call（agent/command/webhook）开始与结束、tool 开始与结束，外加每轮模型输出的文本与 usage。
- 对外事件字段与 `core.Event` 同义（`type` 取值、snake_case 字段、`content` 块数组、`usage`），adapter 侧只做字段搬运，不做语义翻译。
- 终态响应不变；不消费通知的旧调用方行为完全不变。
- 修正等待态误判：adapter 按 `status` 分流，不再把 `waiting_approval` 的会话报成失败。

### 2.2 非目标

- **不做 token 级流式**：不发模型逐字输出、不发工具 stdout/stderr 增量、不发 reasoning/thinking 逐段内容。每轮模型输出以**整段快照**发送一次。
- 不暴露完整内部 `SessionEvent`（不传 prompt 全文、不传 context blocks、不传 checkpoint）。
- 不改 `turn`/`approve` 请求与响应结构（只做加法）。
- 不做审批交互（`EventPermissionRequest` / `RespondPermission`）；本期只做等待态**上报**。
- 不把 heron-connect 改为直连 HTTP SSE。

## 3. 粒度决策：以"轮"为最小单位，不拆 token

内部 `agent.model_response` 事件在每次模型往返后**完整**落盘一次（`internal/agent/runtime.go:613-620`：`text` / `tool_calls` / `usage` / `finish_reason` / `model` 一次性写入）。因此"每轮数据完整返回"天然成立，无需改动 agent 主循环。这是 API 自身的能力边界（一次 Chat 调用返回完整响应），不是协议选择的结果。

由此确定的边界：

| 粒度 | 数据来源 | 是否本期 |
|---|---|---|
| flow turn 开始/结束 | `flow_turn.started/completed/waiting_*` | 是 |
| team turn 开始/结束 | `team_turn.started/completed/waiting_*` | 是 |
| call 开始/结束（含 usage、错误） | `agent_turn.* / command_turn.* / webhook_turn.*` | 是 |
| 模型每轮输出（text + tool_calls 声明 + usage） | `agent.model_response` | 是（整段） |
| 工具开始/结束 | `tool_call.started/completed/failed` | 是 |
| shared record 发布 | `shared_record.published` | 是（只发名称与摘要） |
| 模型逐 token、工具 stdout 增量 | — | 否 |

## 4. 传输与线程模型

### 4.1 传输选择：继续用 JSON-RPC `progress` 通知

复用现有 stdio JSON-RPC 通道，不新增连接。理由：adapter 已管理子进程生命周期与 stdin 写入互斥（`agent/heron/session.go:160-176`），接入零配置。

**对齐目标只有 `core.Event`，不锁任何一家 CLI 的结构。** 传输壳是 `{"jsonrpc":"2.0","method":"progress","params":{...}}`，`params` 内用扁平字段（`type` / snake_case / `content` 块数组 / `usage`）。

不采用 claudecode 的 `{"type":"assistant","message":{"content":[{"type":"tool_use",...}]}}` 嵌套结构，原因有三：其一，codex exec（`item.completed` + 下划线 item 类型）、codex appserver（`item/started` 通知）、acp（`session/update`）结构各不相同，heron-connect 内部本就是四套映射，对齐其中任何一套都不产生通用性；其二，heron 内部的 `agent.model_response` 与 `tool_call.started` 是两个并列事件，硬合成 assistant 消息容器需要在投影层维护额外状态，把简单映射变成重排；其三，那种结构会让 adapter 必须遵守 `assistant 里的 tool_use 块不发事件` 这类反直觉规则（否则出双卡）。扁平 + 独立工具事件与 codex 的 `item.*` 模型同构，语义也更真实。

### 4.2 写入串行化

现状：`jsonRPCServer.Serve` 是单 goroutine 串行循环，`s.write` 直接写 `bufio.Writer`，**无锁**（`cmd/server/jsonrpc.go:150-215, 498-512`）。加入通知后首次出现"turn 执行中"与"响应写出"两个写者，必须改造：

- `jsonRPCServer` 增加 `writeMu sync.Mutex`，`write`/`writeNotification` 全部持锁，写完整行后 `Flush()`。
- 事件泵是**唯一**的通知写者；`Serve` 循环是唯一的响应写者；两者互斥，保证行不交错。
- 禁止任何 `fmt.Print` 到 stdout（日志一律 stderr / 文件），否则破坏 JSONL 帧。

### 4.3 事件泵

每个 turn 一个泵，生命周期严格包住一次 `executeFlowTurn`：

```text
handleTurn
  ├─ sessionID 已知? ──否──> 新会话路径（见 4.4）
  │                  是──> replay.LastSeq → Subscribe(ctx, sid, lastSeq)
  ├─ 启动泵 goroutine：读 channel → 过滤 → 投影 → writeNotification
  ├─ 同步执行 executeFlowTurn（现有逻辑不动）
  ├─ 记录 result.Turn.ID 与本轮最后已知 seq
  ├─ 关闭订阅 ctx，等待泵退出（带超时，见 4.5）
  └─ writeResult（现有逻辑不动）
```

## 5. 会话与订阅时序

### 5.1 已知 session（续聊）

`Replay` 取 `LastSeq` 作为 `afterSeq` 调 `Subscribe`。`Subscribe` 在锁内先补发 `Seq > afterSeq` 的历史、再注册实时监听（`internal/storage/session.go:143-176`），两步之间不会丢事件。

### 5.2 新 session（首个 turn）

现状：`Runtime.Start` 内部生成 `session.ID`（`internal/runtime/flow/runtime.go:152`），调用方要等整个 turn 返回才拿到 ID；FlowRuntime 无创建回调、无 `Sessions()` 访问器。

**选定方案：控制面快照，不做实时订阅。**

1. 在 `handleTurn` 分叉：`sessionID == ""` 走新会话路径，否则走 5.1。
2. 新会话路径**照常阻塞执行** `executeFlowTurn`（`Start`），执行期间不产生通知。
3. `Start` 返回后拿到 `result.Session.ID`，调用 `Replay` 取全量事件，按 5.3 的过滤规则把 `flow_turn_id == result.Turn.ID` 的事件按 `seq` 顺序**一次性补发**，随后写终态响应。

代价与收益：首个 turn 的过程不是实时的（在结果返回前收到，作为快照成组到达），但**数据完整、顺序正确、无丢事件风险**。为实时化而改造 FlowRuntime 的创建回调属于为单场景改核心接口，本期不做。

> 验收口径相应调整：首个 turn 断言"快照事件组出现在终态响应之前且完整"，而不是"turn 执行完成前逐条到达"。第二个 turn 起为真实时。

### 5.3 事件过滤规则（三条，必须同时满足）

1. `event.FlowTurnID == 本轮 flow_turn_id`；
2. `seq > afterSeq`（续聊场景由 Subscribe 保证）；
3. `event.Type` 在 §6 的白名单内。

新会话快照路径的 `flow_turn_id` 取自 `result.Turn.ID`，规则 1 自然成立。

## 6. 对外事件协议

### 6.1 信封

```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_1a2b3c4d",
  "flow_turn_id":"ft_9f8e7d6c",
  "seq":17,
  "event_id":"fs_1a2b3c4d-17",
  "type":"tool_use",
  "team_id":"review-team",
  "call_id":"reviewer",
  "call_turn_id":"ct_5a6b7c8d",
  "attempt":1,
  "block":{...按 type 展开...}
}}
```

**必填：** `session_id`、`flow_turn_id`、`seq`、`event_id`、`type`。
`seq` 为正整数（沿用 SessionEvent 序号，会话内跨层单调），`event_id` 沿用 `SessionEvent.EventID`（格式 `<session_id>-<seq>`）。
**可选：** `team_id`、`call_id`、`call_turn_id`、`attempt`。

**各类型字段扁平展开在 `params` 顶层**（不嵌套 `block` / `message` 容器），上例的 `block` 仅为示意分组。事件类型不认识的客户端忽略整条即可。

### 6.2 事件类型总表

| `type` | 触发源 | 关键字段 | 语义 |
|---|---|---|---|
| `flow_turn_started` | `flow_turn.started` | `attempt` | 本轮开始 |
| `team_turn_started` | `team_turn.started` | `team_id`、`team_session_id`、`attempt` | 某 team 开始 |
| `call_started` | `agent_turn.started` / `command_turn.started` / `webhook_turn.started` | `call_id`、`call_turn_id`、`call_type`、`agent_id` | 某个 call 开始执行 |
| `assistant` | `agent.model_response` | `content`（块数组）、`usage`、`finish_reason`、`model`、`round` | 一轮模型完整输出 |
| `tool_use` | `tool_call.started` | `tool_use_id`、`name`、`input` | 工具开始 |
| `tool_result` | `tool_call.completed` / `tool_call.failed` | `tool_use_id`、`name`、`content`、`is_error`、`status`、`exit_code` | 工具结束 |
| `record_published` | `shared_record.published` | `record_name`、`kind`、`status`、`revision`、`summary` | 共享记录发布 |
| `call_completed` | `agent_turn.completed` / `command_turn.completed` / `webhook_turn.completed` | `call_id`、`call_turn_id`、`call_type`、`status`、`usage`、`tool_calls`、`error` | call 结束 |
| `team_turn_completed` | `team_turn.completed` | `team_id`、`status`、`usage`、`error` | team 结束 |
| `flow_turn_completed` | `flow_turn.completed` | `status`、`usage`、`error` | 本轮结束（终态响应紧随其后） |
| `status` | `flow_turn.waiting_input/waiting_tool/waiting_approval` | `status`、`pending_approvals` | 会话挂起，等调用方动作 |

**命名说明：** `type` 值刻意采用 `tool_use` / `tool_result` / `assistant` / `content` / `is_error` / `usage`，与 `core.EventType` 及 `core.Event` 字段同名同义，adapter 映射只做字段搬运。`flow_turn_*` / `team_turn_*` / `call_*` 是 Heron 的分层信息，`core.Event` 没有对应物，作为**加值类型**存在，adapter 直接丢弃。

### 6.3 各类 payload

`assistant`
```json
{"type":"assistant","round":2,"model":"<model name>",
 "content":[{"type":"text","text":"我先看一下目录结构。"},
            {"type":"tool_use","id":"tc_001","name":"Bash","input":{"command":"ls"}}],
 "usage":{"prompt_tokens":1200,"completion_tokens":48,"total_tokens":1248},
 "finish_reason":"tool_calls"}
```
- `content` 是块数组，块类型只用 `text` 和 `tool_use`；模型无文本时 `content` 只含 tool_use 块；两者都空则**不发**该事件。
- 只发 `resp.Text`，**不发 `resp.Reasoning`**（对应非目标）。
- `usage` 取该次往返的 `resp.Usage`（`types.TokenUsage`，字段 snake_case）。

`tool_use`
```json
{"type":"tool_use","tool_use_id":"ct_5a6b7c8d/1/tc_001","name":"Bash","input":{"command":"ls"}}
```

`tool_result`
```json
{"type":"tool_result","tool_use_id":"ct_5a6b7c8d/1/tc_001","name":"Bash",
 "content":"main.go\nREADME.md","is_error":false,"status":"completed","exit_code":0}
```
`is_error` 由源事件决定：`tool_call.failed` → `true`，`status:"failed"`；`tool_call.completed` → `false`，`status:"completed"`。`exit_code` 仅当 payload 的 `Metadata` 里有数值型 `exit_code` 时携带，否则省略（不下发 `Metadata` 的其他键）。

`call_completed`
```json
{"type":"call_completed","call_id":"reviewer","call_turn_id":"ct_5a6b7c8d",
 "call_type":"agent","status":"completed","usage":{...},"tool_calls":3}
```

`status`（挂起）
```json
{"type":"status","status":"waiting_approval",
 "pending_approvals":[{"request_id":"ap_1","call_id":"reviewer","tool_name":"Bash","reason":"..."}]}
```
本期仅供展示，**不宣称可交互**（见 §10.2）。

### 6.4 工具调用关联 ID

`tool_use_id` 固定为 `<call_turn_id>/<attempt>/<model_tool_call_id>`。

现状缺口（已核实）：`tool_call.started` payload 只有 `{round, tool_name, arguments}`，**没有** `tool_call_id`（`internal/agent/runtime.go:1451-1455, 1500-1504`）；completed/failed 才有 `call.ID`（:1468-1476, 1520-1527）。且 executor 前置拒绝分支（`tool.Name == ""`、hook 失败、`toolExecutor == nil`）**直接 return，不产生任何 started/completed 事件**（:1442-1450, 1483-1491）。

必须补齐：
1. `tool_call.started` payload 增加 `tool_call_id: call.ID`；`call.ID` 为空时由执行器在开始前生成一次（`<call_turn_id>/<attempt>/<round>-<index>`），整条生命周期复用。
2. 上述三个前置拒绝分支也发 `tool_call.started` + `tool_call.failed`（或只发 failed，见下条），保证"开始必有唯一终态"。
3. `attempt` 取自事件 header（`internal/agent/runtime.go:180`），不放进 payload。

## 7. 大小限制与内容安全

| 字段 | 上限（紧凑 JSON UTF-8 字节） | 超限处理 |
|---|---|---|
| `tool_use.input` | 8 KiB | 替换为 `{"_truncated":true,"preview":"<安全截断片段>"}` |
| `tool_result.content` | 32 KiB | 按 UTF-8 边界截断，尾部追加 `\n…[truncated]` |
| `assistant.content` 单个 text 块 | 32 KiB | 同上 |
| 单条完整通知 | 64 KiB | 依次删除可选字段（`model`、`finish_reason`、`usage`），再截断长文本；最终在 `params.truncated:true` 标记 |

上限值刻意压低：目标是 IM 平台卡片可展示，而不是传输容器能装多少。adapter 侧另有展示裁剪（§8.3）。

**不下发：** 模型 prompt/上下文、`Metadata` 中除 `exit_code` 外的键、`Checkpoint`、`ContextBlocks`、`ModelRequestStats` 内部字段。工具参数与结果仍可能含密钥，adapter 侧不落盘、只进展示链路；如需更强保护，后续在投影层加正则脱敏（本期不做，避免误伤）。

## 8. heron-connect adapter 改造

### 8.1 解码

`agent/heron/session.go`：
- 扩展 `handleNotification`：`method != "progress"` 仍忽略；`progress` 先按 `params.type` 分派，**保留旧形式**（无 `type` 只有 `content` → `EventThinking`，见 `session.go:296-308`）。
- 新增 `heronProgress` 结构体（`session_id/flow_turn_id/seq/event_id/type/team_id/call_id/call_turn_id/attempt` + 各类型可选字段，用 `omitempty` 兼容）。

### 8.2 映射表（以实测帧为准）

下表每一行都能在 §8.6 的真实帧里找到对应写法和字段名。**实现时以 §8.6 的帧为唯一事实源**，本节只描述意图。

| heron `type` | core.Event | 字段映射 |
|---|---|---|
| `assistant` | `EventText`（每个 text 块一条） | `content[].text` → `Content`；`content[].type == "tool_use"` 的块**跳过**，见下方"双份 tool_use" |
| `tool_use` | `EventToolUse` | `tool_use_id`→`ToolID`、`name`→`ToolName`、`input`→`ToolInputRaw`，再用 `summarizeInput`(§8.3) 生成 `ToolInput` |
| `tool_result` | `EventToolResult` | `tool_use_id`→`ToolID`、`name`→`ToolName`、`content`→`ToolResult`、`status`→`ToolStatus`、`!is_error`→`ToolSuccess`、`exit_code`（有则）→`ToolExitCode` |
| `call_completed` | 不上屏 | 仅 Debug 日志，记录 `call_id`/`status`/`usage`/`tool_calls`（§8.4） |
| `record_published` | 不上屏 | 仅 Debug 日志，记录 `record_name`/`status`/`revision` |
| `flow_turn_started` / `flow_turn_completed` / `team_turn_started` / `team_turn_completed` / `call_started` | 不产生事件 | 分层信息，`core.Event` 无对应物，直接丢弃 |
| `status` | 不上屏（写 adapter 状态） | 记录 `status` 与 `pending_approvals`，供终态响应分流（§8.5） |
| `assistant` 的 `usage` | 不产生事件 | token 统计以终态响应为准，避免重复计数 |
| 无 `type` 的旧 `progress`（只有 `content`） | `EventThinking` | 存量兼容，行为不变 |

**双份 tool_use（必须用测试守住）**：`assistant` 的 `content` 里本来就带 `{"type":"tool_use","id":...}` 块，紧接着会有一条独立的 `tool_use` 事件，两者是同一个调用。`EventToolUse` **只从独立的 `tool_use` 事件发**，否则进度卡会出现两张一模一样的工具卡。`assistant` 里的 tool_use 块只用于理解"这一轮模型要调什么"，不上屏。

**`tool_use_id` 的形态**：`<call_turn_id>/<attempt>/<model_tool_call_id>`。实测 `call_turn_id` 形如 `tt_6c07a2ff4ade6a27:answer`（团队轮次 id + 冒号 + call 名），`attempt` 从 1 开始。**不要解析它的内部结构**，只做相等性比较 —— 组成方式随时可能变。

**`seq` 语义**：会话内跨 layer 单调递增（flow/team/agent 三份 jsonl 共用一个计数器），但**不连续** —— 中间有被过滤掉的内部事件吃掉序号（例如 §8.6 里 3→5→7→8）。所以 `seq` 只能判"重复/乱序"，不能用来判"有没有丢"。

**`round` 字段**：只出现在 `assistant` 和 `tool_use` 上，是 agent 内部模型往返序号，从 0 开始。它**不是** turn 序号。

### 8.3 展示摘要

`ToolInput` 不能直接塞紧凑 JSON（引擎会当代码块包装，IM 上很难看）。复用 claudecode 的 `summarizeInput`（`agent/claudecode/claudecode.go:1155-1185`）：`Bash`→`command`、`Read/Edit/Write`→`file_path`、`Grep/Glob`→`pattern`，未命中才回退紧凑 JSON。该函数目前是 claudecode 包内私有，需要提到共享位置或复制 —— 建议复制到 `agent/heron` 内（不跨包重构，避免影响其他 adapter）。`ToolInputRaw` 保留原始 map 供将来审批使用。

### 8.4 call / record / 分层事件不上屏（本期）

这几类事件已经在协议里发出（见 §8.6 实测帧），但**都不产生 `core.Event`**：

| 事件 | 处理 | 理由 |
|---|---|---|
| `call_completed` | Debug 日志：`call_id`、`call_type`、`status`、`tool_calls`、`usage` | 进度卡只有 `tool_use`/`tool_result`/`thinking` 三类条目。伪造一个叫 `Agent` 的工具卡会与真实工具调用混淆，且 `core.Event` 里没有稳定的 call→父工具 id 映射 |
| `record_published` | Debug 日志：`record_name`、`kind`、`status`、`revision` | `summary` 基本就是最终回答，和 turn 响应的 `reply` 重复，上屏会重复一遍 |
| `flow_turn_started/completed`、`team_turn_started/completed`、`call_started` | 直接丢弃 | 纯分层信息，`core.Event` 无对应物 |
| `status`（`waiting_input`） | 丢弃 | 和紧随其后的 turn 响应重复 |
| `status`（`waiting_approval`/`waiting_tool`） | 写 adapter 状态 | 供 §8.5 的终态分流用，不上屏 |

call 层的可见性由 `assistant`（每轮模型文本）和 `tool_use/tool_result`（工具）间接提供。
若产品后续需要"子 Agent 卡片"，按现有 `IsSubagent`/`ParentToolID` 通道另立设计 ——
需要先定义 `call_id` 到父工具 id 的映射，协议侧已预留字段，但语义未定。

### 8.5 终态响应修正（现状 bug）

`handleResponse`（`agent/heron/session.go:238-278`）改为：
- 解析 `status` 与 `pending_approvals`（当前 `heronTurnResult` 结构体缺这两项，需补）。
- `status == waiting_approval`：发 `EventThinking`（内容为"等待审批：<tool_name>"，含 `parentToolID` 为空），**不发** `EventResult`/`EventError`，并置 adapter 状态；同时记录 `pending_approvals[0].request_id` 供后续交互（本期只记录）。
- `status == waiting_input` 且 reply 非空：照旧发 `EventResult(Done:true)`。
- `status == waiting_input` 且 reply 为空：仍发 `EventError("model returned an empty result")`（现状保留，这是真失败）。
- `status == waiting_tool`：发 `EventThinking`（"等待工具任务完成"），不发终态。
- 其余 + `error` 非空 → `EventError`（现状）。

### 8.6 实测帧样本（实现与测试的基准）

以下是一次真实 turn 的完整输出，来自 `examples/simple-qa` 下
`heron --json-rpc` 的一轮"读一下 project/README.md 的头部"。
值被保留原样（只省略了长文本中间部分），**字段名与嵌套形状一字不改**。

#### 8.6.1 一条 turn 的完整序列

```text
seq  3  flow_turn_started
seq  5  team_turn_started
seq  7  call_started
seq  8  assistant            ← 只有 tool_use 块（模型还没说话）
seq  9  tool_use
seq 10  tool_result
seq 11  assistant            ← 只有 text 块（带最终回答）
seq 12  call_completed
seq 14  record_published
seq 15  team_turn_completed
seq 17  status               ← waiting_input
seq  --  turn 响应（JSON-RPC result）
```

注意序号**跳号**（3→5→7→8、12→14、15→17）：seq 2/4/6/13/16 是被过滤掉的
内部事件（`*_session.updated`、`agent_session.created`）。这不是丢包。

#### 8.6.2 逐帧

**flow_turn_started**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":3,"event_id":"fs_d785f7561c08032b-3","type":"flow_turn_started","attempt":1}}
```

**team_turn_started**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":5,"event_id":"fs_d785f7561c08032b-5","type":"team_turn_started","attempt":1,
  "team_id":"default","team_session_id":"fs_d785f7561c08032b:default"}}
```

**call_started**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":7,"event_id":"fs_d785f7561c08032b-7","type":"call_started","attempt":1,
  "team_id":"default","team_session_id":"fs_d785f7561c08032b:default",
  "call_id":"answer","call_turn_id":"tt_6c07a2ff4ade6a27:answer","call_type":"agent"}}
```
> `call_type`，取值 `agent` / `command` / `webhook`。`agent_id` 只在 agent 类型出现。

**assistant（只有 tool_use，模型尚未产出文本）**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":8,"event_id":"fs_d785f7561c08032b-8","type":"assistant","attempt":1,
  "team_id":"default","call_id":"answer","call_turn_id":"tt_6c07a2ff4ade6a27:answer",
  "round":0,"model":"hy3-ioa","finish_reason":"tool_calls",
  "content":[{"type":"tool_use","id":"chatcmpl-tool-b73df397cb281091",
              "name":"Read","input":{"file":"project/README.md","line_end":20}}],
  "usage":{"prompt_tokens":2203,"completion_tokens":59,"total_tokens":2262}}}
```

**tool_use**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":9,"event_id":"fs_d785f7561c08032b-9","type":"tool_use","attempt":1,
  "team_id":"default","call_id":"answer","call_turn_id":"tt_6c07a2ff4ade6a27:answer",
  "tool_use_id":"tt_6c07a2ff4ade6a27:answer/1/chatcmpl-tool-b73df397cb281091",
  "name":"Read","round":0,"input":{"file":"project/README.md","line_end":20}}}
```

**tool_result**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":10,"event_id":"fs_d785f7561c08032b-10","type":"tool_result","attempt":1,
  "team_id":"default","call_id":"answer","call_turn_id":"tt_6c07a2ff4ade6a27:answer",
  "tool_use_id":"tt_6c07a2ff4ade6a27:answer/1/chatcmpl-tool-b73df397cb281091",
  "name":"Read","content":"# Simple QA Project Fixture\n\n...",
  "is_error":false,"status":"completed"}}
```
> 失败时：`is_error:true`、`status:"failed"`，`content` 装错误信息（不是 `error` 字段）。
> `exit_code` 只在工具 metadata 里有该值时出现（Bash 类工具），没有就省略这个键。

**assistant（只有文本）**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":11,"event_id":"fs_d785f7561c08032b-11","type":"assistant","attempt":1,
  "team_id":"default","call_id":"answer","call_turn_id":"tt_6c07a2ff4ade6a27:answer",
  "round":1,"model":"hy3-ioa","finish_reason":"stop",
  "content":[{"type":"text","text":"`project/README.md` 头部内容（前 20 行）：\n\n..."}],
  "usage":{"prompt_tokens":2427,"completion_tokens":238,"total_tokens":2665}}}
```

**call_completed**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":12,"event_id":"fs_d785f7561c08032b-12","type":"call_completed","attempt":1,
  "team_id":"default","team_session_id":"fs_d785f7561c08032b:default",
  "call_id":"answer","call_turn_id":"tt_6c07a2ff4ade6a27:answer","call_type":"agent",
  "status":"completed","tool_calls":1,
  "usage":{"prompt_tokens":4630,"completion_tokens":297,"total_tokens":4927}}}
```
> `usage` 是本 call 累计（含所有 round），不是最后一轮。失败时带 `error`。

**record_published**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":14,"event_id":"fs_d785f7561c08032b-14","type":"record_published",
  "team_id":"default","team_session_id":"fs_d785f7561c08032b:default",
  "record_name":"Answer","kind":"agent_result","status":"active","revision":1,
  "summary":"`project/README.md` 头部内容（前 20 行）：\n\n..."}}
```
> `summary` 通常就是最终回答正文，和 turn 响应的 `reply` 重复。**不要拿它当回复源**，否则会重复上屏。

**team_turn_completed**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":15,"event_id":"fs_d785f7561c08032b-15","type":"team_turn_completed","attempt":1,
  "team_id":"default","team_session_id":"fs_d785f7561c08032b:default",
  "status":"completed",
  "usage":{"prompt_tokens":4630,"completion_tokens":297,"total_tokens":4927}}}
```

**status**
```json
{"jsonrpc":"2.0","method":"progress","params":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "seq":17,"event_id":"fs_d785f7561c08032b-17","type":"status","status":"waiting_input"}}
```
> `waiting_input` 是正常收尾，**和紧随其后的 turn 响应重复**，adapter 应忽略。
> 只有 `waiting_approval` / `waiting_tool` 值得处理（后者带 `pending_approvals`）。

**turn 响应（最后一行）**
```json
{"jsonrpc":"2.0","id":1,"result":{
  "session_id":"fs_d785f7561c08032b","flow_turn_id":"ft_63e1d5552f54e14b",
  "status":"waiting_input","reply":"`project/README.md` 头部内容（前 20 行）：\n\n...",
  "records":[...],"usage":{"prompt_tokens":4630,"completion_tokens":297,"total_tokens":4927}}}
```

#### 8.6.3 首轮（新会话）的差别

新会话时，上述帧**在 turn 响应返回之后才一次性到达的时序不成立**：实际是在
`Start` 返回后、响应写出前，按 seq 顺序**成组发出**。帧内容与 §8.6.2 完全一致，
只是到达时间集中在末尾。adapter 不需要区分，按序处理即可；
但要注意**别依赖"收到 assistant 时进度卡已经存在"** —— 首轮所有帧会背靠背到达。

#### 8.6.4 可直接落成 fixture 的最小断言

```text
1. 过滤后的事件类型序列 ==
   [flow_turn_started, team_turn_started, call_started, assistant, tool_use,
    tool_result, assistant, call_completed, record_published,
    team_turn_completed, status]
2. 每个 tool_use 都有同 tool_use_id 的 tool_result（数量相等）
3. assistant 的 tool_use 块数 == 独立 tool_use 事件数，且 EventToolUse 只发后者
4. 所有 seq 严格递增；允许跳号（用 > 判断，不用 == +1）
5. 最终 EventText 拼接结果 == turn 响应 reply（可以不等，但若不等，
   必须有一处被判定为不应该上屏 —— 当前实现两者相同，故应相等）
6. 一个 seq 只处理一次：重复 seq 被丢弃（用于重订阅补发去重）
```

#### 8.6.5 如何自己抓一份新样本

heron-ai 侧已可运行。在配置目录下（cwd 必须含 `.agents/`）：

```bash
cd heron-ai/examples/simple-qa
go build -C ../.. -o bin/heron ./cmd/server   # 或仓库根构建

printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"turn","params":{"input":"读一下 project/README.md 的头部"}}' \
  | ../../bin/heron --json-rpc

# 续聊：带上一轮的 session_id，验证"只转发本轮事件、不重放"
printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"turn","params":{"session_id":"fs_xxx","input":"再看第二行"}}' \
  | ../../bin/heron --json-rpc
```

stderr 与 stdout 必须分离：stdout 只允许 JSONL 帧，任何日志混进去都会破坏解析
（本实现所有诊断信息走 `internal/logging`，不写 stdout）。

## 9. 两端改造清单

### 9.1 Heron AI

- [ ] `jsonRPCServer` 注入 `storage.SessionWriter`（`internal/app/runtime.go:38` 的 `RuntimeBundle.Sessions`）；`runJSONRPC` 传入。
- [ ] `write` 加互斥，新增 `writeNotification`，全部走同一 `bufio.Writer` + `Flush`。
- [ ] `handleTurn` 分叉：已知 session → `Replay.LastSeq` + `Subscribe` + 事件泵；新 session → 阻塞执行后按 `flow_turn_id` 快照补发。
- [ ] 投影层：`SessionEvent` → `progress` params，按 §6 类型表与 §7 截断规则；白名单过滤，未知类型直接丢弃。
- [ ] `tool_call.started` 补 `tool_call_id`；补前置拒绝分支的 started/failed 事件（§6.4）。
- [ ] 事件泵退出：turn 结束关闭订阅 ctx，泵排空已入 channel 的事件后退出；等待设上限（建议 5s，超时则丢弃剩余事件并记 stderr 日志），**绝不阻塞终态响应**。
- [ ] 渠道 drop 兜底：`publishLocked` 在订阅 channel 满时会 drop+close（`internal/storage/session.go:188-201`）。泵检测到 channel 关闭时，用 `Replay` 从最后已发 `seq` 继续补发，最多重连 2 次，仍超限则停止补发并在终态响应中标注（不影响响应本身）。
- [ ] 扩展 `docs/CONNECT-CLI-JSONRPC.md` §11：固化本文件的字段表。
- [ ] `heron --output-format stream-json`（`cmd/server/streamjson.go`）本期**不动**。在文档中注明：该模式与本章 JSON-RPC 投影是两条并行链路，互不移植；不要为了"看起来像 claude"去改它。

### 9.2 heron-connect

- [ ] `heronTurnResult` 补 `status` 已有、补 `pending_approvals`；`handleResponse` 按 §8.5 分流。
- [ ] `handleNotification` 支持结构化 `progress`，保留旧 `content` 形式。
- [ ] 按 §8.2 映射表实现；`assistant` 的 text 块 → `EventText`。
- [ ] `summarizeInput` 复制到 heron 包，`ToolInput` 用摘要、`ToolInputRaw` 用原始 map。
- [ ] `seq` 单调校验：小于等于上次已处理的 `seq` 直接丢弃（防补发重复）；adapter 每轮重置。
- [ ] 单测直接以 §8.6.2 的帧为 fixture：解码、映射、旧格式兼容、`seq` 重复丢弃、等待态不发 `EventError`，断言按 §8.6.4 六条逐项覆盖。

## 10. 验收标准

- [ ] 续聊 turn：执行过程中依次收到 `flow_turn_started` → `team_turn_started` → `call_started` → `assistant`/`tool_use`/`tool_result` … → `call_completed` → `team_turn_completed` → `flow_turn_completed`，最后是 `turn` 响应。
- [ ] 首个 turn：上述事件组以快照形式在终态响应**之前**完整到达，`seq` 连续递增，无缺失、无乱序。
- [ ] 每条 `tool_use` 必有且仅有一条同 `tool_use_id` 的 `tool_result`，包括 executor 前置拒绝与 hook 失败路径。
- [ ] `assistant` 事件的 text 在 IM 端合并为一整段（不逐字刷屏），usage 与终态响应的加总一致。
- [ ] 不消费通知的旧客户端（只发 `turn`）行为不变；stdout 无非 JSON-RPC 输出。
- [ ] `waiting_approval` 会话不再报 `EventError`；`pending_approvals` 被解析并记录。
- [ ] 截断边界有单测（8 KiB / 32 KiB / 64 KiB），`truncated:true` 标记正确，截断后 JSON 仍合法。
- [ ] 慢订阅（channel 满）场景下不卡死终态响应，补发逻辑有测试。

## 11. 实施顺序

1. heron-ai：writer 互斥 + 投影层 + 已知 session 的订阅泵（先不碰首轮）。
2. heron-ai：`tool_call.started` 补 `tool_call_id` 与前置拒绝路径的事件。
3. heron-ai：新 session 快照补发。
4. heron-connect：解码 + 映射 + `summarizeInput`。
5. heron-connect：`status`/`pending_approvals` 分流修正。
6. 端到端：Web + 至少一个 IM 平台，验证工具卡片与正文展示、旧客户端兼容。
7. 文档：`CONNECT-CLI-JSONRPC.md` §11 更新。

## 12. 风险与待定

- **首轮非实时**：本方案接受快照模式。若要实时，需要给 FlowRuntime 加创建回调（改核心接口），另立提案。
- **`call_completed` 不上屏**：多 Agent 场景下，"哪个 agent 干了什么"目前只能从 `assistant` 文本与工具名推断。若产品需要子 Agent 卡片，是二期独立设计。
- **`tool_use` 双份**：`assistant` 消息里自带 `tool_use` 块，adapter 已规定**只从独立 `tool_use` 事件发 `EventToolUse`**，否则会出两张卡。这一点必须在映射实现里有测试守护。
- **record_published 本期丢弃**：flow 间数据流转在 IM 上不可见，是否上屏待产品确认。
- **审批交互**：需要 `EventPermissionRequest` + `RespondPermission` 一并实现，本期只上报状态。
- **上游契约演进**：白名单字段是投影层定义的，内部 `SessionEvent` 增字段不会自动外泄（符合 `CONNECT-CLI-JSONRPC.md` 扩展原则）。
- **agent 层事件原先缺 `flow_turn_id`（实现期发现的真缺陷）**：`emitAgentEvent` 写入的 header 只有 `flow_session_id/team_*/call_*`，没有 `flow_turn_id`。任何"只转发本轮事件"的消费者都会把整组工具事件丢掉，且不报错。已在 heron-ai 侧补上（`internal/agent/runtime.go`），同时在 `types.AgentRequest` 增加 `FlowTurnID`，并将 `internal/runtime/call/agent.go` 与 Spawn 子 agent 两条构造路径同步传递。**这是一次跨层改动，不只是加个字段** —— 后续新增构造 `AgentRequest` 的代码必须记得填它。
- **不追求跨 CLI 结构兼容（已定，非待定）**：claudecode / codex / acp 结构互不相同，heron-connect 靠各自 adapter 收敛到 `core.Event`。Heron 只需稳定自己的 JSON-RPC 投影并与 `core.Event` 字段同义；将来若要接入新客户端，写一个新 adapter，而不是让 heron 去模拟对方的线格式。

## 13. 代码参考

- heron-ai 方法分发与 writer：`cmd/server/jsonrpc.go:150-231, 498-520`
- heron-ai 订阅与 seq：`internal/storage/session.go:43-55, 135-201`
- heron-ai 事件类型与 header：`pkg/types/event.go:9-79`
- heron-ai 工具事件发射点：`internal/agent/runtime.go:1442-1527`
- heron-ai 模型回复事件：`internal/agent/runtime.go:613-620`
- heron-ai 首轮 session 创建：`internal/runtime/flow/runtime.go:136-168`
- heron-ai 既有通知设计（§11）：`docs/CONNECT-CLI-JSONRPC.md`
- heron-connect adapter：`agent/heron/session.go:216-310`
- heron-connect 事件模型：`core/message.go:262-330`
- heron-connect 展示处理：`core/engine_turn.go:735-1110`
- heron-connect 工具摘要参考：`agent/claudecode/claudecode.go:1155-1185`
