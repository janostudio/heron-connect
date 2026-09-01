# openclaw 借鉴落地方案：IM 交互四方向

> 2026-08-31，承接 `03-comparison.md` §八 的四个结论，逐条给出 heron-connect 的改造设计。
> 原则沿用 `05-refactoring-plan.md`：不推倒重建、每 Phase 独立可交付、接口向后兼容、学 UX 不抄代码。

对应关系：

| # | 方向 | openclaw 证据 | heron-connect 现状 |
|---|---|---|---|
| ① | 显式消息准入/记录/分发管线 | `turn/kernel.ts` runChannelTurn + `ChannelTurnAdmission` | `handleMessage` 长函数，准入判断内联（engine.go:1090-1878） |
| ② | 进度草稿行级就地更新 + 事件类型化 | `streaming.ts` 判别联合 + `mergeChannelProgressDraftLine` | `compactProgressWriter.AppendStructured` 只 append+截断（progress_compact.go:429） |
| ③ | 会话绑定生命周期统一回收 | `thread-bindings-policy.ts` idle/maxAge/回收 | session_key 前缀 + 各平台各自 reset_on_idle（reapIdleSessions engine.go:3056） |
| ④ | cron 一等子系统 | `src/cron/` at/every/cron + durable delivery + isolated + failure notify | CronJob + robfig/cron 标准表达式 + 重建 replyCtx（engine_cron.go） |

---

## 方向①：显式消息准入管线（Message Pipeline）

### 现状

`handleMessage`（engine.go:1090）是一条 ~790 行的长函数，准入逻辑按顺序内联：recall → voice → 空内容 → alias → rate-limit → banned-word → workspace 解析 → permission 待答 → …… 每个判断各自 `return`。问题：

- 没有显式的"决策结果"类型，只有隐式的 `return` 分支；
- 没有统一的观测点（只在入口打一条 `message received`，中间各步的 reject 原因散落在各自的 slog）；
- 难以单元测试单条准入规则（必须搭完整 engine）。

### 目标（借鉴 openclaw 的 admission 枚举）

把准入抽成一个**纯决策阶段**，产出明确的 admission 结果，再统一分发：

```go
// core/message_pipeline.go
type MessageAdmission int

const (
	AdmissionDispatch  MessageAdmission = iota // 正常交给 agent 处理
	AdmissionObserveOnly                        // 记录但不启动 agent（如只读通知/心跳）
	AdmissionHandled                            // 已本地处理（斜杠命令/权限应答等）
	AdmissionDrop                               // 静默丢弃（空消息、被拦截等）
	AdmissionReject                              // 拒绝并回复原因（限流/违禁词/权限不足）
)

type AdmissionDecision struct {
	Action  MessageAdmission
	Reason  string   // 拒绝/丢弃原因，统一进日志
	Reply   string   // 需要回复给用户的内容（Reject 用）
	Session *Session // 预解析出的会话（Dispatch 用）
}

// AdmissionStage 是一条准入规则。
type AdmissionStage func(ctx *MessagePipelineContext) *AdmissionDecision

type MessagePipeline struct {
	stages []AdmissionStage
}
```

`handleMessage` 重构为「跑 stages → 按 decision 分发」：

```go
func (e *Engine) handleMessage(p Platform, msg *Message) {
	decision := e.admissionPipeline.Run(p, msg) // 内部按序跑 stages，第一个非 nil decision 短路
	switch decision.Action {
	case AdmissionReject:
		e.reply(p, msg.ReplyCtx, decision.Reply)
	case AdmissionHandled, AdmissionDrop, AdmissionObserveOnly:
		// 已处理或不需要 agent，记录 trace 后返回
	default: // AdmissionDispatch
		e.dispatchToAgent(p, msg, decision.Session)
	}
}
```

### 阶段拆分（把现内联逻辑一一映射成 stage）

| stage | 现逻辑位置 | decision |
|---|---|---|
| recall | handleMessageRecall | Handled |
| voice | handleVoiceMessage | Handled（或 Dispatch 续行） |
| empty | content=="" 且无附件 | Drop |
| alias | resolveAlias + ExtraContent | 无 decision（改 msg） |
| rateLimit | checkRateLimit | Reject |
| bannedWord | matchBannedWord | Reject |
| workspace | resolveWorkspace | Reject 或改 ctx |
| pendingPermission | handlePendingPermission | Handled |

### 分步落地（Phase 1.x）

1. **定义类型**：`MessageAdmission` + `AdmissionDecision` + `MessagePipelineContext`（新文件 `core/message_pipeline.go`）；
2. **抽 stage 函数**：把现有内联块原样搬成 `func (e *Engine) stageXxx(ctx) *AdmissionDecision`，**不改行为**——这是纯机械抽取，先保证等价；
3. **接线**：`handleMessage` 改为跑 pipeline，`dispatchToAgent` 承接原来的后半段（processInteractiveMessage 调用点）；
4. **观测**：统一 `emit` 一条 `message.admission` 日志（含 stage 名 + decision + reason + sessionKey），对齐 openclaw 的 `ChannelTurnLogEvent`；
5. **测试**：每个 stage 单测（不需要完整 engine），补一条 pipeline 集成测试覆盖"同一条消息被 stage1 短路后 stage2 不再执行"。

**收益**：准入规则可独立测试、reject 原因统一可查、加新规则（如未来"黑名单用户""群静默"）只加一个 stage，不再动长函数。

---

## 方向②：进度草稿行级就地更新

### 现状

`ProgressCardEntry`（progress_compact.go:47）是扁平结构 `{Kind,Text,Tool,ID,Status,ExitCode,Success}`，`AppendStructured`（:429）只做 `w.items = append(...)` + 超限截断。v1.1.26 已经补了 `ToolID`（Event.ToolID → Entry.ID）用于"同 id 调用/结果配对"，但**配对是 web 端做的，writer 本身仍是无差别的 append**。

### 目标（借鉴 openclaw 的行 `id` + `correlationKey` + merge）

把「合并语义」下沉到 writer，而不是交给 web 端：

```go
// 扩展 ProgressCardEntry，增加合并语义字段
type ProgressCardEntry struct {
	Kind     ProgressCardEntryKind `json:"kind"`
	Text     string                `json:"text"`
	Tool     string                `json:"tool,omitempty"`
	ID       string                `json:"id,omitempty"`           // 已有：tool_use id
	CorrelationKey string          `json:"correlation_key,omitempty"` // 新增：合并键（同 key 行级更新）
	Status   string                `json:"status,omitempty"`
	ExitCode *int                  `json:"exit_code,omitempty"`
	Success  *bool                 `json:"success,omitempty"`
}
```

`AppendStructured` 增加合并分支：

```go
// 当 item.CorrelationKey 非空且已存在同 key 的 item 时，就地更新该行
// 而非追加新行（对应 openclaw mergeChannelProgressDraftLine）。
func (w *compactProgressWriter) AppendStructured(item ProgressCardEntry, fallback string) bool {
	// ...
	if item.CorrelationKey != "" {
		if idx := w.findByCorrelationKey(item.CorrelationKey); idx >= 0 {
			w.items[idx] = item              // 就地替换
			w.entries[idx] = fallback
			w.renderAndUpdate()              // 复用现有 render+send 逻辑
			return true
		}
	}
	// 原 append 路径不变
}
```

### 合并键的生成规则（对齐现有 tool_use/tool_result 语义）

| 事件 | CorrelationKey |
|---|---|
| `EventToolUse` | `ToolID`（有则用；无则退化 `tool:ToolName` 但连续同名会合并——v1.1.26 web 端已做"连续同名 ×N 分组"，这里下沉） |
| `EventToolResult` | 与配对 tool_use 相同的 `ToolID`（Engine 已透传 ToolID，见 Event.ToolID） |
| 其他（thinking/info） | 空（不合并，继续 append） |

### 分步落地

1. **加字段** `CorrelationKey`（向后兼容：json `omitempty`，旧 payload 无此字段不受影响）；
2. **writer 加 merge**：`findByCorrelationKey` + 就地替换 + 复用 render；
3. **Engine 接线**：`processInteractiveEvents` 里 `EventToolUse`/`EventToolResult` 填 `CorrelationKey = event.ToolID`（fallback 到 tool name 需谨慎，见下）；
4. **web 端简化**：v1.1.26 在 ProgressCard.tsx 做的按 id 配对/连续同名分组，可改为直接信任后端已合并的行（前端配对逻辑可保留作兜底，但不再必需）；
5. **测试**：writer 单测（同 key 第二次 append 不增行、更新文本、超限截断仍生效）。

**收益**：进度卡不再"一轮对话刷满"，tool_use→tool_result 天然变成一行状态流转（running→completed/failed），且对**所有平台**生效（不再依赖 web 端特殊处理）。

**注意（连续同名合并的坑）**：用 `tool:ToolName` 做合并键会误合并「两个并行的同名工具」，所以**只有当 ToolID 存在时才启用行级合并**；无 ToolID 时保持 append（回退现状），不强行按名字合并。这比 v1.1.26 web 端的"连续同名 ×N"更保守、更正确。

---

## 方向③：会话绑定生命周期统一回收

### 现状

- 会话绑定 = `sessionKey` 字符串（平台前缀 + 会话 id），存在 `interactiveStates` map；
- 回收靠 `reapIdleSessions`（engine.go:3056）按 `lastEventTime` + `resetOnIdle` 清理；
- `resetOnIdle` 有平台级覆盖（`resolveResetOnIdleForPlatform`），但**语义是"agent 事件空闲"，不是"绑定生命周期"**；
- 之前事故：web 后台被项目级 720 idle 误切（因 web 用的是 bridge 平台名，没查到平台级 override）。

### 目标（借鉴 thread-bindings-policy 的显式生命周期）

引入**绑定级策略对象**，把"何时回收 / 回收后干嘛"从散落的 idle 判断里抽出来：

```go
// core/session_binding.go
type SessionBindingPolicy struct {
	// IdleTimeout 是"无 agent 事件"多久后回收。<=0 表示不回收。
	IdleTimeout time.Duration
	// MaxAge 是绑定创建后强制回收的硬上限（防长驻泄漏）。<=0 不限制。
	MaxAge time.Duration
	// OnExpire 回收前回调（如"发一条会话已过期提示"）。可选。
	OnExpire func(sessionKey string)
}
```

策略解析层级（对齐 openclaw 的 account > channel > global）：

```go
func (e *Engine) resolveBindingPolicy(platformName string) SessionBindingPolicy {
	// 1. 平台级 override（已有 map，扩展到含 MaxAge）
	// 2. engine 级默认（resetOnIdle → IdleTimeout）
	// 3. 硬编码兜底
}
```

### 分步落地

1. **加字段**：`interactiveState` 记录 `createdAt`（已隐含有，补显式）——用于 MaxAge；
2. **抽策略**：`SessionBindingPolicy` + `resolveBindingPolicy`（复用现有 `resolveResetOnIdleForPlatform`，扩展成返回完整 policy）；
3. **reaper 改造**：`reapIdleSessions` 从"只看 resetOnIdle"改为"读 policy 的 IdleTimeout + MaxAge 两个维度"；
4. **修 web idle 误切根因**（顺带）：确保 `resolveBindingPolicy` 用 `msg.Platform`（逻辑平台名）而非 `p.Name()`（bridge 恒等），与之前修过的 bug 同一根因，此处一并固化到策略解析层；
5. **测试**：policy 解析层级、MaxAge 强回收、平台 override 命中。

**收益**：回收策略集中、可观测（Expire 回调）、MaxAge 兜底防长驻泄漏；web/bridge 等虚拟平台的 idle 误切问题在策略层根治，而非逐个平台打补丁。

---

## 方向④：cron 一等子系统化

### 现状

`CronJob`（cron.go:22）字段已较全（Prompt/Exec/SessionMode/TimeoutMins/Silent/Mute），但：

- 只有标准 cron 表达式（robfig/cron/v3）；
- 执行结果没有 durable delivery——发送失败只记 `LastError`，不重试、不通知；
- 没有 isolated 会话执行（`new_per_run` 是"每次新会话"，但复用同一 engine 的 agent 实例）；
- 失败无主动通知（除非 prompt 本身要求）。

### 目标（借鉴 openclaw 的 at/every + durable delivery + failure notify）

增量，不推翻：

#### 4.1 扩展调度表达式

`CronExpr` 字段兼容两种新格式（前缀区分，向后兼容标准 5 段 cron）：

```
CronExpr = "0 11 * * *"          // 现有标准 cron，不变
CronExpr = "@every 10m"          // 新增：固定间隔
CronExpr = "@at 2026-09-01 09:00" // 新增：一次性定时
```

解析处（`CronScheduler`）加一个 `normalizeSchedule(expr)`：识别 `@every`/`@at` 前缀，转成 robfig 支持的等价表达（`@every` 直接支持；`@at` 转成精确的一次性 schedule 或在触发后自删）。

#### 4.2 durable delivery + 失败通知

`ExecuteCronJob`（engine_cron.go:33）返回后，若发送失败（`ReplyContextReconstructor` 重建失败 / Send 失败），做两件事：

1. 重试一次（短退避）；
2. 仍失败则 `sendFailureNotificationAnnounce`——推一条"cron job X 执行失败"到 job 关联的管理员/群（用 job 的 session_key 本身，或配置的 fallback 目标）。

字段上给 `CronJob` 加 `RetryCount`（默认 1）与 `NotifyOnFailure *bool`。

#### 4.3 isolated 会话（可选，Phase 后期）

`SessionMode` 增加 `"isolated"`：每次 run 用独立的 agent 进程（`getOrCreateWorkspaceAgent` 的变体），不污染主会话，也不受主会话 idle 回收影响。对齐 openclaw 的 `isolated-agent/`。**此项成本最高、收益边际，放最后**。

### 分步落地

1. **调度表达式扩展**：`normalizeSchedule` + 单测（标准/@every/@at 三态）；
2. **durable delivery**：重试 + 失败通知 + `RetryCount`/`NotifyOnFailure` 字段（向后兼容，旧 job 默认重试 1 次不通知）；
3. **isolated**（可选，暂缓）。

**收益**：cron 可靠性提升（失败可感知、可重试）、支持间隔/一次性调度；为"每日复盘"这类关键 job 提供兜底。

---

## 落地顺序与风险

| 顺序 | 方向 | 风险 | 理由 |
|---|---|---|---|
| 先 | ② 进度行级合并 | 低（纯增量，向后兼容） | 直接解决 v1.1.26 遗留问题，所有平台受益 |
| 次 | ④ cron 调度+重试 | 低（增量字段+新函数） | 独立模块，不动消息主链路 |
| 再 | ① 消息管线 | 中（重构长函数，需保证行为等价） | 纯机械抽取 + 补测试，收益大但要谨慎 |
| 后 | ③ 会话绑定生命周期 | 中（动 reaper，涉及之前 bug 的敏感区） | 与 idle 误切事故同源，需带回归测试 |
| 暂缓 | ④ isolated 会话 | 高 | 边际收益低，成本高 |

建议按 ② → ④(前两步) → ① → ③ 顺序推进，每个方向独立 commit、独立发布。
