# heron 异步回合结果回推：实施流程

> 记录时间：2026-10-20
> 上游问题单：`troubleshooting-2026-10-20-async-turn-visibility.md`
> 关联设计：`docs/plans/2026-10-20-heron-ai-process-event-stream-design.md`
> 样本会话：`fs_65b26ec7349acdca`（heron-ai/s9，v0.0.18 × heron-connect v1.3.29）

**目标**：让 heron-ai 后台异步回合（`activate` 派发出去的 owner / repo-info 等工作）产出的结果，自动回推到发起它的那个会话，用户无需追问。

**核心判断**：连接器不缺能力，缺的是"让已有通道活着"。

---

## 0. 结论先行

heron-connect 早已有一条与具体 agent 无关的通用通道 —— `core/engine_turn.go:151` 的 `runUnsolicitedReader`。它在**回合结束后**继续消费 agent 事件流，把 agent 主动吐出的内容转成平台消息、写进 `session.History`、维护 `backgroundActive` 状态。

这条通道就是异步结果回推的标准出口。**只要 heron-ai 把后台回合的最终结果当成一次 agent 自发输出写进同一个事件流，连接器侧几乎零改动，结果就会自动出现在会话里、自动进入上下文。**

所以本流程的主线是：**先修好这条通道的触发条件（连接器侧，独立可发），再让引擎往里面写东西（引擎侧）。** 不新建 `subscribe` 方法、不引入 `follow_async` 参数、不约定 `flow_turn_id` 级消息映射。

---

## 1. 当前断点（按修复顺序）

### B-a 适配器把"正常结束"误标为异常（heron-connect，必须修）

`agent/heron/session.go` 的 `handleResponse` 三个出口全部 `return s.emit(...)`：

| 条件 | 发射事件 |
|---|---|
| `result.Error != ""` | `EventError` |
| `result.Reply == ""` | `EventError`（"model returned an empty result"） |
| 正常 | `EventResult` |

`EventError` 会让 `core/engine_turn.go:1728` 置 `eventsNeedResync = true`，而回合结束后的重启条件是 `core/engine.go:2229`：

```go
alive := state.agentSession != nil && state.agentSession.Alive() && !state.stopped && !state.eventsNeedResync
if alive { e.startUnsolicitedReader(...) }
```

**`eventsNeedResync = true` 时后台 reader 根本不启动。** 后台结果从一开始就没有消费者。

### B-b progress 被单个 FlowTurnID 锁死（heron-connect，必须修）

`agent/heron/session.go:497 acceptProgress`：

```go
if s.progressTurnID == "" {
    s.progressTurnID = progress.FlowTurnID
} else if s.progressTurnID != progress.FlowTurnID {
    return false   // 后续所有 turn 的 progress 静默丢弃
}
```

一次 Send 周期内，只要锁定到 `ft_A`，同周期后续 `ft_B`（后台 turn）的 progress 全部被丢。**即使 B-a 修好、reader 起来了，后台 turn 的 `assistant` / `flow_turn_completed` 仍然进不来。**

### B-c 引擎把结构化原始 JSON 当正文推（heron-ai）

`cmd/server/progress.go` 的 `projectModelResponse` 把 `payload["text"]` 原样塞进 `content[{type:"text"}]`。结构化 agent（`agent.Structured != nil`）的 `resp.Text` 是模型按要求输出的原始 JSON。事件 payload 无 `structured` 标记，projection 层无从判断。

### B-d 引擎不区分前台/后台 turn（heron-ai）

`flow_turn_started` 的 payload 无 `origin`；`activate` 派发后 session 立即置 `waiting_input`，调用方无法得知"还有后续"；后台 turn 的 `Reply` 只落在 `flow_records` / `evidence.jsonl`，不进事件流。

---

## 2. 分层方案

```
heron-ai 引擎                 heron-connect 适配器              heron-connect 引擎
─────────────                 ─────────────────                ──────────────────
后台 turn 完成
  → flow_turn_completed
    携带 reply + origin
                        →  acceptProgress 按 seq 去重，
                           允许多 turn 通过
                        →  handleResponse 不再把
                           "无回复"标成 EventError
                                                  →  eventsNeedResync 保持 false
                                                  →  runUnsolicitedReader 存活
                                                  →  event.Content 落 History
                                                  →  e.send(p, replyCtx, chunk)
                                                     用户看到后台结果
```

---

## 3. 实施步骤

### 阶段一：连接器解阻塞（可独立发布，先做）

**Task 1 — 放宽 `acceptProgress` 的 turn 锁**

- 文件：`agent/heron/session.go`
- 把单一的 `progressTurnID + lastProgressSeq` 改成 **按 `FlowTurnID` 分桶**的 `map[string]int64`。
- 去重键仍是 `flow_turn_id + seq`；**不再**在 turn 变化时整体拒收。
- 每个 Send 周期开始时清空该 map（现有 `Send` 内已重置 `progressTurnID/lastProgressSeq`，改为 `clear(map)`）。
- 保留 `status` 合成帧的特例（`Seq == 0`、无 `EventID`）。

**Task 2 — `handleResponse` 区分"异常"与"空回复"**

- 文件：`agent/heron/session.go`
- `result.Error != ""`：**保持** `EventError`（这是真错误，重同步是对的）。
- `result.Reply == ""` **且没有** `result.Error`：改为先发 `EventText{Content: ""}` 不发，直接发 `EventResult{Content: <兜底文案>, Done: true}`。
  - 兜底文案沿用现有 i18n 或直接 `"(空响应)"` 占位，关键是**事件类型是 Result 而不是 Error**。
  - 理由：引擎这一轮确实正常结束了，后台可能还在跑；把它标成 Error 会连带杀掉后台 reader 存活的资格。
- 保留 `slog.Warn("heronSession: turn returned empty reply")` 用于可观测性。

**Task 3 — 补测试**

- 文件：`agent/heron/session_test.go`
- 新增：
  - `TestHeronSessionAcceptsProgressAcrossMultipleFlowTurns` —— 同一 Send 周期内先 `ft_A` 后 `ft_B`，断言 `ft_B` 的 assistant 事件**能**发出。
  - `TestHeronSessionEmptyReplyEmitsResultNotError` —— 空 reply 断言 `EventResult`。
  - `TestHeronSessionErrorResponseStillEmitsError` —— 已有的 `TestHeronSessionHandleErrorResponseEmitsError` 保持通过即可，防止 Task 2 改过头。

**Task 4 — 引擎侧回归验证**

- 文件：`core/engine_turn.go`（**不改代码**，只加验证）
- 确认 `runUnsolicitedReader` 的 `EventResult` 分支（`engine_turn.go` 内 `case EventResult`）在收到非空 `Content` 时能正常 `session.AddHistory` + `e.send`。
- 若已有测试覆盖，跳过；否则补一个 `core/engine_test.go` 用例，构造一个"回合结束后再推 EventResult"的假 session，断言消息被投递。

> ⚠️ **Task 6（`origin`）必须与 Task 1 同批或先于 Task 1 上线**：放宽 `acceptProgress` 而不引入 `origin`，后台 turn 的 `assistant` 原文会直接刷进用户通道，比原始 JSON 事故更糟。
>
> 阶段一完成后即可验证：**在 heron-ai 不改的前提下**，手工用 `heron-connect send` 之外的路径（例如直接对 JSON-RPC 补一个 progress 帧）灌一个后台结果，看它是否落到用户端。这是最小的端到端验证闭环。

### 阶段二：引擎侧补事件（heron-ai）

**Task 5 — 后台 turn 完成时把 reply 写进事件流**

- 文件：`internal/runtime/flow/runtime.go`（`finishTurn` 附近，约 1455 行）
- `finishTurn` 现有 payload：`{"session", "turn", "error"}`，补 `"reply": result.Reply`。
- 当该 turn 的 `Input == ""`（mailbox 唤醒的后台 turn）时，额外发一个 `flow_turn_completed` 语义的派生通知，供连接器直接取 `reply` 当正文。
- **注意**：`finishTurn` 在 `SessionWaitingInput` 时发的是 `flow_turn_waiting_input` 而不是 `flow_turn_completed`，而派发回合和后台回合都走 `waiting_input`。需要靠 `origin`（Task 6）区分，不能只看事件类型。

**Task 6 — 给 turn 加 origin 标记**

- 文件：`internal/runtime/flow/runtime.go`（`runTurnWithActivationsAndContext` 里 `EventFlowTurnStarted` 的 payload）
- 新增字段 `origin: "user" | "mailbox" | "resume"`，取值来源即 `ResumeForMailbox` / `Resume` / `Start` 三条入口。
- 连接器侧只需透传，不做逻辑判断；渲染层用它决定是否"作为追加消息投递"。

**Task 7 — 结构化输出标记**

- 文件：`internal/agent/runtime.go`（`emitAgentEvent`，约 627-634 行）+ `cmd/server/progress.go`（`projectModelResponse`，约 149-186 行）
- `agent.model_response` payload 补 `structured: bool` 与 `reply: string`（解析后的正文）。
- `projectModelResponse` 在 `structured == true` 时推 `reply`，或至少把 `text` 换成解析后的正文。
- 这条是**独立问题**（文档 B1），不阻塞异步回推，但同一批事件里一起改成本最低。

### 阶段三：状态语义（heron-ai，可选）

**Task 8 — `activate` 后返回 `waiting_async`**

- 文件：`internal/runtime/flow/runtime.go`（`sessionStatusAfterTurn` 与 `turnStatusForSession`）
- 当前派发回合结束一律 `waiting_input`（代码注释明确写着这是刻意的：dispatch turn 要能结束、要能接新输入）。
- 改为：显式派发了后台工作 → `waiting_async`。调用方据此知道"这不是终态"。
- **风险**：`HandleInputWithContext` 里的状态白名单需要同步放行 `waiting_async`，否则下一轮用户消息会被拒。**这项优先级最低**，在 Task 5/6 已经能让结果回流之后，它只改善语义清晰度，不做也不影响功能。

---

## 3.5 过程披露：分三层，别一股脑全推

引擎侧的数据比原先估计的完整得多。`flow` / `team` / `agent` 三层事件用的是**同一个 `storage.SessionWriter` 实例**（`internal/app/runtime.go` 创建一次，再经 `SetSessionWriter` 分发给团队与 Agent 运行时）：

| 写入点 | 层 | 发布 |
|---|---|---|
| `internal/runtime/flow/runtime.go:1541 appendSessionEvent` | flow | `Append` 内 `publishLocked` |
| `internal/runtime/team/runtime.go:736/755/788/811/848/871/912` | team | 同上 |
| `internal/agent/runtime.go:191` | agent | 同上 |

`JSONLSessionWriter.Append` 写完 JSONL 后无条件 `publishLocked`（`internal/storage/session.go:131`），广播给该会话的所有 `Subscribe` 订阅者。**所以后台回合的每一个 team、每一次 tool_call、每一次 model_response，引擎一直在广播，只是没有消费者。**

信息不缺，缺的是**分层展示**。三条通道不能混：

| 内容 | 通道 | 实现 | 理由 |
|---|---|---|---|
| 最终答复（如 default 汇总的那句话） | `EventResult` → 消息气泡 + `session.History` | 已有（`runUnsolicitedReader` 内 `case EventResult`） | 用户真正要的，且必须进上下文 |
| 中间进度（"owner 已激活"、"repo-info 完成"） | `EventThinking` → 临时状态 | 新增，见 Task 5b | Web 有 typing 就够；IM 端按 `ThinkingMessages` 配置过滤，避免刷屏 |
| 完整明细（每个 tool_call、每次 model_response） | **不进用户通道** | 只在 `flow.jsonl` / `team.jsonl` / `agent.jsonl`，按需查询 | 一次 turn 可能几十条，直接推给 UI 是灾难 |

**现状与目标一致的部分**：`runUnsolicitedReader` 对 `EventToolUse` / `EventToolResult` 只打 `slog.Debug`、不转发（`core/engine_turn.go` 对应分支）。这个设计是对的，**不要改**。

**必须引入 `origin` 的理由**：放开 `acceptProgress` 之后，后台 turn 的 `assistant` 事件会与前台 turn 的混在同一个流里。没有 `origin` 作为"哪些 assistant 能当正文"的唯一依据，后台 agent 的中间输出会被当作正文推给用户——比原始 JSON 刷屏更糟。因此 **Task 6（`origin`）是 Task 1（放宽 turn 锁）的前置依赖**。

**用户主动追问进度**：当前只能靠用户再发一句话、由 agent 用 3 次 Bash 读自己的 `flow.jsonl` 来回答（样本会话 20:20:51 那次耗时 30 秒）。补充方案：

- 轻量路径（推荐）：`runUnsolicitedReader` 在收到 `team_turn_completed` 时，把一句 `↳ <team> 完成` 作为 `EventThinking` 推给 Web（IM 端按配置过滤）。
- 查询路径：扩展 `heron-connect sessions show`（`cmd/heron-connect/sessions.go`），读取 heron 会话的三个 jsonl，渲染成各 team 状态一览。作为"查看详情"入口，不做自动推送。

**不做**：不把后台 turn 的 `assistant` 原文当正文发送（这正是本次事故的形态）。

---

## 3.6 时序推演：`default → owner → other` 三段式

以真实样本 `fs_65b26ec7349acdca` 的形态为例（default 派发 owner，owner 再派发 other）：

```
t0   用户发问
     connect ──turn──> 引擎
     flow turn ft_A 开始（input=用户问题，origin=user）

t1   default 判定需要外部核实
     → NextActivate teams=[owner]
     → asyncDispatch: enqueueWorkMessage(owner, caller_team=default)
                      + launchAsyncDispatch（goroutine，不内联执行）
     ← ft_A 立即结束，status=waiting_input，Reply="已转给执行侧核实…"
     connect ──> 用户看到"已转给执行侧核实…"          ← 用户视角的"第一段答复"

t2   owner 回合 ft_B 开始（input="", origin=mailbox）  ← other 此时还没开工
     owner 判定需要查仓库 → NextActivate teams=[other]
     → 同样 enqueue + launchAsyncDispatch
     ← ft_B 结束（waiting_input）
     ⚠️ 注意：ft_B 的 Reply 也是中间产物，不是最终答案

t3   other 回合 ft_C 开始（input="", origin=mailbox）
     执行 repo-info 等实际工作，产出 RepoFacts 记录
     ← NextProceed，activator=owner → 结果回流 owner

t4   owner 再次被唤醒 ft_D（mailbox），汇总 other 的结论
     ← NextProceed，activator=default → 回流 default

t5   default 被唤醒 ft_E（mailbox），组织最终答案
     Reply = "heron-ai 仓库的主分支是 master"
     ← ft_E 结束
     ★ 这里才是要回推给用户的内容
```

**责任判定：**

| 环节 | 现状 | 缺口 |
|---|---|---|
| t1 前台答复 | ✅ 正常送达 | 无 |
| t2→t5 后台 turn | 引擎照常执行、照常广播事件 | 连接器 `eventPump` 已在 t1 结束 `Stop()` |
| t5 最终答复 | 只写进 `flow_records` / `evidence.jsonl` | 未进事件流 payload（Task 5） |
| t2/t4 的中间 Reply | 同样是"中间产物" | **不能**当正文推送，需 `origin` 区分（Task 6） |

**关键认识**：`default → owner → other` 是**链式回流**，每一跳都是一个独立的 mailbox 回合，都有自己的 `Reply`。因此"把 reply 推给用户"不能对所有 turn 一视同仁——**只有回流到入口 team（`activator == ""`）的那个回合，其 Reply 才是给用户看的**。这条判定规则应写进 Task 5：

```
origin == "mailbox" && activator == ""  → Reply 是面向用户的最终答复
origin == "mailbox" && activator != ""  → Reply 是中间产物，仅作进度提示或丢弃
```

---

## 3.7 补充任务

**Task 5b — 中间进度以 `EventThinking` 转发**（连接器，与 Task 1/6 同批）

- 文件：`core/engine_turn.go`（`runUnsolicitedReader`）
- 在 `case EventToolUse` / `case EventToolResult` 附近新增对 **team 级事件**的处理：收到 team 回合完成时，向平台推一句简短进度（形如 `↳ <team_id> 完成`），复用现有 `EventThinking` 通道，**不写 history**。
- heron 适配器需先把 progress 的 `team_turn_completed` 映射成 `core.Event`（当前 `handleNotification` 只处理 `assistant/tool_use/tool_result/status/call_completed/record_published`，需补 `team_turn_started/team_turn_completed/flow_turn_started/flow_turn_completed` 的映射）。

**Task 5c — 按 `activator` 判定最终答复**（引擎，与 Task 5 同批）

- 文件：`internal/runtime/flow/runtime.go`（`finishTurn`）
- 仅当该 turn 由 mailbox 唤醒且 `activation.activator == ""` 时，才把 `result.Reply` 作为面向用户的答复带进事件流；其余后台 turn 的 Reply 标记为 `intermediate: true`，供 Task 5b 作为进度文案使用。

**Task 6 前置声明**：`origin` 字段必须随 Task 1 一起上线（或先于 Task 1），否则放宽 `acceptProgress` 会让后台 `assistant` 原文直接刷进用户通道。

---

## 4. 验收标准

**必须全部满足：**

1. 用户发"查看仓库 heron-ai 目前主分支是什么" → 前台回合回"已转给执行侧核实" → **无需追问**，后台结果作为**追加消息**出现在同一会话。
2. 后台结果同时出现在 `session.History`（agent 上下文里可见），不是只发给人看。
3. 连接器日志出现 `unsolicited events detected, relaying to platform` + `unsolicited turn complete`（`core/engine_turn.go` 内已有日志点）。
4. 结构化 agent 的原始 JSON **不再**作为正文出现在聊天里（Task 7 完成后）。
5. 真错误场景（`result.Error != ""`）仍然正确触发 `EventError`，重同步行为不变。
6. `default → owner → other` 链式回流中，**只有回流到入口 team 的那一跳**被当作用户答复推送；中间跳的 Reply **不**出现在正文（Task 5c）。
7. 后台回合期间用户能看到进度提示（如 `↳ owner 完成`），但该提示**不写入** `session.History`（Task 5b）。

**明确不做：**

- 不新增 JSON-RPC `subscribe` 方法。
- 不给 `turn` 加 `follow_async` 参数。
- 不做"编辑已有气泡"式的合并渲染 —— 后台结果**新增一条消息**。理由：幂等、连接器零改动、与 `runUnsolicitedReader` 现有实现天然一致。

---

## 5. 验证命令

```bash
# 连接器侧：后台结果是否被转发
grep -n "unsolicited" scripts/cc-connect-qhn/logs/heron-connect.log | tail -20

# 引擎侧：后台 turn 是否带上了 reply / origin
jq -c 'select(.type=="flow_turn.completed" or .type=="flow_turn.waiting_input")
       | {turn: .payload.turn.id, status: .payload.turn.status,
          origin: .payload.turn.origin, reply_len: (.payload.reply|length)}' \
  .agents/data/sessions/<sid>/flow.jsonl

# 引擎侧：确认后台 turn 的 input 为空（mailbox 驱动）
jq -c 'select(.type=="flow_turn.started")
       | {id: .flow_turn_id, input_len: (.payload.input|length)}' \
  .agents/data/sessions/<sid>/flow.jsonl
```

---

## 6. 排期建议

| 阶段 | 内容 | 依赖 | 可独立发布 |
|---|---|---|---|
| 一 | Task 1-4（连接器解阻塞）+ **Task 6 前置** | 无 | 是 |
| 一' | Task 5b（进度提示转发） | 阶段一已发 | 是 |
| 二 | Task 5、5c、7（引擎补事件 + activator 判定） | 阶段一已发 | 是（需双方版本对齐） |
| 三 | Task 8（`waiting_async`） | 阶段二 | 是 |

**阶段一单独发布也有价值**：它修的是"任何 agent 只要回合正常结束，后台自发事件都能被转发"这条通用能力，与 heron-ai 是否改造无关。
