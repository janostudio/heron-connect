# Heron AI Agent 转交过程展示设计

**状态：** 方案草案  
**范围：** heron-ai JSON-RPC progress 投影、heron-connect Heron adapter 与通用进度展示

## 1. 结论

转交应作为 **Agent/Team handoff（委派）** 展示，不应伪装成模型工具调用。界面可以复用工具卡片的视觉样式，但协议和事件语义要独立：handoff 不增加工具调用统计，不冒充模型发起的 `tool_use`，也不复用 `tool_use_id`。

**需要 heron-ai 改造。** heron-ai 掌握路由目标、来源、任务和 mailbox 投递结果；当前 JSON-RPC progress 投影没有把这些信息发给 heron-connect。connector 只拿到下游事件，无法可靠地从最终回复里的 JSON 反推出转交生命周期。heron-connect 也需要新增事件映射与展示处理。

本设计补充 `2026-10-20-heron-ai-process-event-stream-design.md`，不修改该文档中已定的模型文本和真实工具事件行为。

## 2. 背景与问题

当前观察到的输出类似：

```json
{
  "reply": "已转给执行侧核实 heron-ai 仓库的主分支信息，稍后给出结论。",
  "repo": "heron-ai",
  "task": "查询仓库主分支",
  "branch": "",
  "source": "primary",
  "authorization": "",
  "intent_hint": "查仓库主分支",
  "next": {
    "action": "activate",
    "teams": ["owner"],
    "reason": "用户要求查仓库 heron-ai 的主分支，属执行类查询，交 owner 分诊"
  }
}
```

这不是工具调用参数，而是把面向用户的说明（`reply`）与编排控制信息（如 `next.action`、目标 Team、route reason）混在了同一个 JSON 文本里。heron-connect 将 assistant 文本块映射为 `EventText`；Web/IM 因而把整个 JSON 当普通文本显示。与此同时，heron-ai 当前的 `call_started` / `call_completed` 投影不包含完整转交目标与任务，connector 无从生成准确的委派卡片。

现有两类过程必须继续区分：

- **模型工具调用**：模型产生 `tool_use`，由 `tool_call.started` 执行，随后有 `tool_result`。应继续映射为 `EventToolUse` / `EventToolResult`。
- **Agent/Team 转交**：Flow 决定把任务交给另一个 Team/Agent，或写入 mailbox。它是编排层 handoff，不是模型工具。

## 3. 目标与非目标

### 3.1 目标

- 用户看到清楚的过程卡片，例如“转交给 owner”及任务摘要，而不是原始路由 JSON。
- 明确区分“已开始准备转交”“已成功投递”和“投递失败”；不把“已投递”误说成“目标任务已完成”。
- 将面向用户的 `reply` 文本与路由控制字段分开传输；控制字段不进入普通文本/最终回复。
- 复用现有进度卡视觉与平台能力，同时保留 handoff 的独立语义和统计。
- 不影响已有 CLI adapter，不要求它们理解 Heron 的 handoff 事件。

### 3.2 非目标

- 不将 handoff 映射成 `EventToolUse`，不增加工具调用次数或工具成功/失败统计。
- 不展示 `authorization`、原始 mailbox payload、完整内部 route object 或未过滤的执行上下文。
- 不把 mailbox 已接收等同于下游 Agent 已完成。
- 不改变普通模型工具调用、assistant 文本与终态 reply 的既有协议。

## 4. 推荐方案

在 Heron AI 的事件投影层增加稳定的 handoff progress 事件；heron-connect 将其映射为独立的通用 handoff 事件，并用进度卡片的呈现能力显示。**UI 可以借用工具卡布局，但事件类型仍是 handoff。**

```text
Flow 选择 NextActivate
  → heron-ai 为每个目标生成 handoff_id
  → progress: handoff_started
  → enqueue mailbox
      ├─ 接收成功：progress: handoff_dispatched
      └─ 投递失败：progress: handoff_failed
  → heron-connect 映射 EventHandoffStarted / EventHandoffUpdated
  → Web/IM 显示“转交给目标 Team：任务摘要”及投递状态
```

首期事件仅覆盖应用当前启用的 `asyncDispatch` mailbox 路径；关闭 async dispatch 时目标 Team 在同一 FlowTurn 内联执行，不产生 mailbox handoff 事件，仍由既有 team/call/tool 事件表达。

`handoff_id` 必须在开始投递前生成，不能依赖 enqueue 成功后才返回的 mailbox message ID。建议使用 Heron 自己的 `newID("ho")`；成功事件可额外附 `mailbox_message_id` 供排查。`handoff_started` 在 Flow 确定目标、准备 enqueue 时发；只有 mailbox 写入成功后才能发 `handoff_dispatched`。若投递失败，发 `handoff_failed` 并保留脱敏错误摘要。一个 `NextActivate` 有多个目标 Team 时，每个目标独立生成 ID 和生命周期，不能把部分成功合并成全成功。

## 5. JSON-RPC progress 协议

沿用现有 JSON-RPC 2.0 `progress` notification 和公共事件头：`session_id`、`flow_turn_id`、`seq`、`event_id`、`type`。`seq` 沿用会话全局递增序号，允许因其它内部事件过滤而跳号；connector 仅用它去重和拒绝乱序，不据此判定漏事件。每个 handoff 生命周期帧都必须重复 `handoff_id`、`source_team`、`target_team`、`route_action`；这样即使 start 帧因重连或慢订阅未到，终态帧也能独立显示。

### 5.1 开始事件

```json
{
  "jsonrpc": "2.0",
  "method": "progress",
  "params": {
    "session_id": "fs_123",
    "flow_turn_id": "ft_456",
    "seq": 24,
    "event_id": "fs_123-24",
    "type": "handoff_started",
    "handoff_id": "ho_123",
    "source_team": "primary",
    "target_team": "owner",
    "task_summary": "查询 heron-ai 仓库主分支",
    "route_action": "activate"
  }
}
```

### 5.2 投递成功与失败

```json
{
  "jsonrpc": "2.0",
  "method": "progress",
  "params": {
    "session_id": "fs_123",
    "flow_turn_id": "ft_456",
    "seq": 25,
    "event_id": "fs_123-25",
    "type": "handoff_dispatched",
    "handoff_id": "ho_123",
    "source_team": "primary",
    "target_team": "owner",
    "route_action": "activate",
    "task_summary": "查询 heron-ai 仓库主分支",
    "mailbox_message_id": "mb_789",
    "status": "accepted"
  }
}
```

失败使用 `type: "handoff_failed"`、相同的 `handoff_id`、`source_team`、`target_team`、`route_action`、可选 `task_summary`、`status: "failed"` 及面向用户的简短 `error`。错误不得带凭据、原始 prompt 或完整内部 payload。所有 handoff 状态帧重复身份字段，connector 即使未收到 start 也能按终态新建卡片。

字段要求：

| 字段 | 要求 | 说明 |
|---|---|---|
| `handoff_id` | 必需且稳定 | 在 enqueue 前生成（建议 `newID("ho")`）；同一目标的 started/dispatched/failed 必须相同。不得依赖 enqueue 成功后才产生的 mailbox ID。 |
| `source_team` | 必需 | 发起转交的 Team。 |
| `target_team` | 必需 | 单条事件只表示一个目标。 |
| `task_summary` | 可选 | 只发纯文本任务摘要，最多 512 UTF-8 字节；优先使用 typed `TaskSummary`，缺失时 UI 显示通用委派文案。 |
| `route_action` | 必需 | 首期为 `activate`，供审计/诊断；UI 不必直接展示。 |
| `status` | 终态必需 | `accepted` 表示 mailbox 已接收，不表示目标执行完成。 |
| `mailbox_message_id` | dispatched 可选 | 实际 mailbox message ID，仅用于诊断/关联，不作为 handoff 生命周期主键。 |
| `error` | failed 必需 | 简短、可展示的失败原因；对敏感内部错误先做脱敏。 |

`task_summary` 优先取 typed route/handoff request 的显式任务字段；在迁移现有 `Route` 时，可由已解析的顶层 `task` 字段填入，再退回 `Route.Reason`。不得从任意 assistant 文本或完整 JSON 回复中猜测任务；两者都为空时省略该字段，由 UI 使用“委派给 <target_team>”作为摘要。不发送 `authorization`、原始 `next` 对象或不必要的 `branch/source/intent_hint` 副本。

### 5.3 不同生命周期的含义

`handoff_dispatched` 的完成边界是“目标 mailbox 已成功接收任务”。它只结束**转交动作**，不宣告目标 Team 已完成工作。目标执行和结果返回若也要显示，应由 heron-ai 另发 `handoff_result`（或关联的 agent/call 生命周期事件），与本次 handoff 使用同一 ID；这是可选后续能力，不是首期成功状态的隐含含义。

## 6. `reply` 与控制面分离

heron-ai 应让自然语言回复和路由控制在协议上成为不同字段/事件：

- progress handoff 帧承载 `target_team`、`task_summary`、`handoff_id` 和投递状态。
- JSON-RPC 最终 `reply` 只承载给用户看的自然语言，例如“已转给 owner 核实，稍后反馈。”
- 结构化输出或内部 `next` 不得被整体放进 `assistant.content[].text` 或最终 `reply`。

迁移期，heron-connect 必须防止 JSON 在流式预览阶段先泄漏、最终阶段才被清理。规则固定如下：

1. 对首个非空字符为 `{` 的 Heron `assistant` text block，先按 turn 暂存，不立即发 `EventText`；普通非 JSON 文本仍按现状立即流出。暂存上限为 UTF-8 64 KiB。
2. 收到 turn 终态时，仅当暂存文本不超过 64 KiB、能完整解析，且同时满足 `reply` / `task` / `source` 为 string、`next.action == "activate"`、`next.teams` 为非空 string 数组时，才视为已知旧 envelope；丢弃控制 JSON，只释放 `reply`。`repo`、`branch`、`authorization`、`intent_hint` 可选且不影响匹配。若最终响应 `reply` 也包含该 envelope，同样只以抽取后的 `reply` 作为 `EventResult`。
3. 不匹配 schema 的 JSON 一律原样释放，保证普通 JSON 回答不被改写。若发生真实错误/挂起而没有有效 handoff envelope，也释放暂存内容；候选文本超过 64 KiB 时按协议错误处理，只显示“转交数据过大，已省略”，不把可能含控制字段的原始 JSON 释放给用户。为保持此安全界限，旧版兼容 envelope 的最大大小固定为 64 KiB；新协议的 handoff 控制信息应使用 progress 帧，不依赖该缓冲。普通非 envelope JSON 在 64 KiB 以内原样保留。

此兼容逻辑只作防漏显示，不替代 heron-ai 的结构化 handoff 事件，否则仍无法显示目标、投递失败状态或可靠区分编排控制数据。

## 7. heron-ai 改造清单

- [ ] 明确首期只覆盖 `asyncDispatch` mailbox enqueue 路径；同步 inline activate 不发 handoff 事件。
- [ ] 在 Flow 处理 `NextActivate`、实际调用 mailbox enqueue 的位置，为每个目标预先生成稳定 handoff ID，并记录来源 Team、目标 Team、任务摘要。
- [ ] 将已解析的任务字段规范化到 typed route/handoff request 的 `TaskSummary`；兼容现有 `Route` 时按“显式任务摘要 → `Route.Reason` → 缺省”的优先级填充，不解析任意 assistant 文本。任务摘要为空时省略字段。
- [ ] 投递成功后发 `handoff_dispatched` 并可附 mailbox message ID；enqueue 失败后发 `handoff_failed`。事件应反映真实执行结果，不从最终 JSON 回复推断。
- [ ] 为 progress 投影增加 handoff 白名单类型和上述字段，保留公共 session/turn/seq/event ID；不把内部 `SessionEvent` 整体暴露。
- [ ] `task_summary` 限制为最多 512 UTF-8 字节、`error` 最多 1 KiB；只从任务摘要/route reason 生成，不转发 `authorization`、prompt 或内部上下文。
- [ ] 确保实时订阅与首轮快照都投影同样的事件，事件顺序正确且同一事件不重复发送。
- [ ] 将最终 JSON-RPC `reply` 限定为用户可见文案，控制面数据只走结构化字段。
- [ ] 测试单目标成功、多目标部分成功、投递失败、首轮快照、续聊实时事件以及 stdout JSONL 兼容。

## 8. heron-connect 改造清单

- [ ] 扩展 Heron progress 结构体和解析器，识别 handoff 类型；保留当前普通 assistant、真实工具事件和旧版 progress 行为。
- [ ] 采用单一 `core.EventHandoff` 事件类型，并在 `Event` 增加 `HandoffID`、`HandoffSource`、`HandoffTarget`、`HandoffTask`、`HandoffStatus`、`HandoffError` 字段。状态固定为 `started`、`accepted`、`failed`；将 wire `handoff_dispatched` 映射为 `accepted`。每个状态事件都带同一 `HandoffID` 及完整来源/目标字段。
- [ ] 在 Engine 中处理 `EventHandoff`，将其投影为 `ProgressEntryHandoff`；不增加 `toolCount` / 工具名统计，不走 `EventToolUse` / `EventToolResult` 分支。
- [ ] 将 `ProgressEntryHandoff` 加入 Go 通用进度卡模型和 Web `ProgressCard` 类型/renderer；以 `handoff_id` 作为 `CorrelationKey`，started → accepted/failed 原位更新同一行。其它平台沿用结构化 progress payload；不支持结构化卡的路径输出简短“转交给目标：任务”文案。
- [ ] 若先收到终态帧而缺少 started（例如重连边界），按完整终态帧直接创建 accepted/failed 行，不丢弃该状态；后续重复或更旧的状态按 seq 去重。
- [ ] 多目标按不同 `HandoffID` 独立显示；缺少 `task_summary` 时使用“委派给 <target_team>”作为卡片摘要。
- [ ] 严格匹配旧 JSON 转交 envelope 时按 §6 暂存 assistant 文本，并在终态统一决定释放 `reply` 还是原 JSON；不能等到最终阶段才单独清理，否则流式预览已泄漏。
- [ ] 不把 handoff 计入工具调用计数，不将 `authorization` 写入日志、进度卡或会话历史。
- [ ] 测试 64 KiB 以内普通 JSON 回答暂存后能原样释放；超限候选只显示省略提示，不泄漏原文。
- [ ] 测试有效转交 envelope 在流式预览、最终回复、历史记录中都不泄漏控制字段，只显示 `reply`。
- [ ] 测试 mailbox handoff 的 started/accepted/failed 合并、重复序号去重、多目标部分失败、终态帧独立到达、任务摘要缺省及长度限制。
- [ ] 测试普通 assistant 文本仍即时输出；非 async inline activate 不产生 handoff；真实工具卡、工具计数和其它 CLI 行为不变。

## 9. 展示建议

工具调用卡通常表现为“工具名 + 输入 + 执行结果”；Agent 转交卡则表达“来源 → 目标 + 任务 + 投递状态”。建议视觉结构接近，但文案和图标明确区分，例如：

```text
Agent 转交
primary → owner
任务：查询 heron-ai 仓库主分支
状态：已投递，等待执行
```

- 开始阶段显示“正在转交”。
- mailbox 接收成功显示“已投递”，不显示“已完成”。
- enqueue 失败显示失败状态及经过脱敏的原因。
- 多个目标显示多张独立 handoff 行/卡片。
- 有下游完成/结果事件时，再通过同一 `handoff_id` 更新为“执行完成”并展示摘要；没有该事件时保持“已投递”。

## 10. 验收标准

- [ ] 观察到 `next.action=activate` 时，用户看到 handoff 卡片/行；原始路由 JSON 不出现在正文、流式预览或会话历史中。
- [ ] 卡片准确显示来源 Team、目标 Team 和任务摘要；不展示授权字段及内部上下文。
- [ ] 每个目标的 handoff 状态由开始更新为 accepted 或 failed；同一 ID 更新原条目，不产生重复卡。
- [ ] accepted 只表示 mailbox 成功接收，界面不误报下游任务完成。
- [ ] 模型发起的真实 `tool_use` 仍走现有工具卡路径，工具计数不包含 handoff。
- [ ] 64 KiB 以内、且不匹配旧 handoff envelope 的普通 JSON 回复保持原样；其它 CLI adapter 行为不变。
- [ ] 首轮快照、续聊实时、序号跳号/重复、async mailbox 与 inline activate 分界、多目标部分失败及超长/缺失摘要均有测试覆盖。

## 11. 分阶段建议

1. **Heron AI 契约先行：** 确定 handoff 事件在 enqueue 前后发出，并把 `reply` 与控制数据分离；提供稳定测试帧。
2. **Connector 结构化接入：** 增加 handoff 事件和卡片 entry，避免用 tool event 冒充。
3. **兼容兜底：** 仅对已知 JSON envelope 精确抽取 `reply`，防止旧版本上游继续泄漏控制 JSON；设置删除兼容逻辑的版本/条件。
4. **联调验收：** 验证单目标、多目标、dispatch 失败与异步结果回流，再决定是否扩展结果完成事件。

## 12. 参考代码

- heron-ai NextActivate mailbox 路由：`internal/runtime/flow/runtime.go`
- heron-ai Team call started/completed 事件：`internal/runtime/team/runtime.go`
- heron-ai progress 事件投影：`cmd/server/progress.go`
- heron-connect Heron progress 映射：`agent/heron/session.go`
- heron-connect 通用 Event：`core/message.go`
- heron-connect 工具事件处理与计数：`core/engine_turn.go`
- heron-connect 进度卡片模型：`core/progress_compact.go`
