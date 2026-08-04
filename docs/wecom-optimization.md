# WeCom + Session 优化方案

基于 `cc-connect-qhn-2026-07-21_144927.log`（403,417 行 / 478MB / 7 天）的日志分析。

---

## 日志分析摘要

| 级别 | 数量 | 说明 |
|------|------|------|
| ERROR | 6 | WeCom 频率限制(5) + session idle timeout(1) |
| WARN | 122 | slow agent send(40)、process exited cleanly(23)、slow first event(13)、graceful stop timeout(11)、slow session close(11)、WeCom 流式过期(10)、WeCom 频率限制(8)、slow final reply(7) |
| 无 PANIC/FATAL | 0 | 进程稳定 |

---

## 优化 1: Session 空闲 360 分钟自动清理

### 问题
当前 `eventIdleTimeout = 2h` 是 per-turn 级别超时（agent 在处理但无输出）。跨轮 session 清理依赖 `resetOnIdle`，但该值默认 0（关闭），且只检查 `session.UpdatedAt`（用户发消息时更新），无法覆盖 agent 进程中无输出的情况。

日志中 `wecom:T12260048A:T12260048A` 的 session 在 agent 无响应 2h8m 后触发 idle timeout kill，但这个超时只 kill 当前 turn，不清理 session 数据。

### 方案

#### 1a. 将默认 `resetOnIdle` 改为 360 分钟
```
文件: cmd/cc-connect-qhn/main.go
const defaultResetOnIdleMins = 360  // 原来是 0
```
含义：用户 360 分钟没发新消息到某个 session，下次发消息时自动创建新 session。

#### 1b. 增加后台 Session Reaper
在 engine 中增加 goroutine，定期扫描活跃的 ACP 进程，超过 360 分钟无 agent 事件的进程直接 kill。

```
文件: core/engine.go

// 新增字段
type Engine struct {
    ...
    sessionReapInterval time.Duration  // 扫描间隔，默认 5 分钟
    ...
}

// 新增方法
func (e *Engine) startSessionReaper() {
    go func() {
        ticker := time.NewTicker(e.sessionReapInterval)
        defer ticker.Stop()
        for {
            select {
            case <-e.ctx.Done():
                return
            case <-ticker.C:
                e.reapIdleSessions()
            }
        }
    }()
}

func (e *Engine) reapIdleSessions() {
    e.interactiveMu.Lock()
    defer e.interactiveMu.Unlock()
    
    for key, state := range e.interactiveStates {
        if state == nil || state.agentSession == nil || !state.agentSession.Alive() {
            continue
        }
        if e.resetOnIdle <= 0 {
            continue
        }
        // 检查距上次 agent 事件的时间
        lastEvent := state.lastEventTime
        if lastEvent.IsZero() {
            lastEvent = state.turnStartTime  // 回退到 turn 开始时间
        }
        if time.Since(lastEvent) > e.resetOnIdle {
            slog.Info("reaping idle agent session",
                "session_key", key,
                "idle_for", time.Since(lastEvent),
                "threshold", e.resetOnIdle)
            go e.cleanupInteractiveState(key, state)
        }
    }
}
```

```
文件: core/engine_turn.go

// 在 processInteractiveEvents 的 event loop 中，每次收到 event 后:
state.mu.Lock()
state.lastEventTime = time.Now()
state.mu.Unlock()
```

```
文件: core/engine.go

// interactiveState 新增字段:
type interactiveState struct {
    ...
    lastEventTime  time.Time  // 上次收到 agent event 的时间
    turnStartTime  time.Time  // 当前 turn 开始时间
    ...
}
```

#### 1c. Reaper 与 `resetOnIdle` 的关系
- `resetOnIdle`：跨轮清理，用户 360 分钟不发消息→下次发消息时新建 session
- Reaper：同轮/后台清理，agent 进程无事件 360 分钟→直接 kill 进程
- 两者使用同一个 `resetOnIdle` 配置值，保持一致性

---

## 优化 2: Graceful Stop 增加日志细节

### 问题
日志中 `acpSession: graceful stop timed out, sending SIGTERM` 出现了 11 次，每次都恰好 ~8 秒超时。`acpSession.Close()` 的三阶段 shutdown 中 Phase 1（stdin close + wait 8s）总是超时，但没有日志说明具体哪个阶段超时。

另外 `process exited cleanly` 用了 WARN 级别，正常进程退出不应该是 WARN。

### 方案

```
文件: agent/acp/session.go

func (s *acpSession) Close() {
    sid := s.currentACPSessionID()
    
    // Phase 0: send cancel notification
    s.cancelCurrentTurn()
    slog.Info("acpSession: sent cancel, closing stdin", "session_id", sid)
    
    // Phase 1: close stdin, wait for clean exit
    s.closeStdin()
    
    phase1Start := time.Now()
    select {
    case <-s.processExited:
        slog.Info("acpSession: exited after stdin close",
            "session_id", sid,
            "elapsed", time.Since(phase1Start))
        return
    case <-time.After(8 * time.Second):
        slog.Warn("acpSession: stdin close timeout, sending SIGTERM",
            "session_id", sid,
            "elapsed", time.Since(phase1Start))
    }
    
    // Phase 2: SIGTERM
    core.SignalProcessGroup(s.cmd.Process.Pid, syscall.SIGTERM)
    phase2Start := time.Now()
    select {
    case <-s.processExited:
        slog.Info("acpSession: exited after SIGTERM",
            "session_id", sid,
            "elapsed", time.Since(phase2Start))
        return
    case <-time.After(5 * time.Second):
        slog.Warn("acpSession: SIGTERM timeout, sending SIGKILL",
            "session_id", sid,
            "elapsed", time.Since(phase2Start))
    }
    
    // Phase 3: SIGKILL
    core.ForceKillProcessGroup(s.cmd.Process.Pid)
    phase3Start := time.Now()
    select {
    case <-s.processExited:
        slog.Info("acpSession: exited after SIGKILL",
            "session_id", sid,
            "elapsed", time.Since(phase3Start))
    case <-time.After(3 * time.Second):
        slog.Error("acpSession: SIGKILL timeout, abandoning process",
            "session_id", sid,
            "elapsed", time.Since(phase3Start))
    }
}
```

同时修改进程退出日志级别：
```
// 原来:
slog.Warn("acp: process exited cleanly", "session_id", sid)
// 改为:
slog.Info("acp: process exited cleanly", "session_id", sid)
```

---

## 优化 3: WeCom 频率限制精细化处理

### 企微规则

| 维度 | 限制 | 说明 |
|------|------|------|
| 每分钟 | **30 条/会话** | `aibot_respond_msg` 和 `aibot_send_msg` 共享配额 |
| 每小时 | **1000 条/会话** | 同上，共享配额 |
| 流式更新 | **每条都算 1 条** | 每次 `aibot_respond_msg` 的 stream update 都消耗配额 |
| 错误码 846607 | 频率超限 | 可等待后重试 |
| 错误码 846608 | 流式过期 | 超过 10 分钟不可更新，不可重试 |

### 问题
日志中：
- 5 次 ERROR: `errcode=846607` 频率限制
- 8 次 WARN: `errcode=846607` 频率限制  
- 10 次 WARN: `errcode=846608` 流式过期

当前代码对这两个错误码没有任何特殊处理，只记录日志。

### 方案

#### 3a. 增加 Per-Chat 消息计数器

```
文件: platform/wecom/websocket.go

// chatRateTracker 追踪每个会话的消息发送速率
type chatRateTracker struct {
    mu    sync.Mutex
    chats map[string]*chatWindow
}

type chatWindow struct {
    minute []time.Time  // 最近 1 分钟内的发送时间戳
    hour   []time.Time  // 最近 1 小时内的发送时间戳
}

func (t *chatRateTracker) record(chatID string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    
    if t.chats == nil {
        t.chats = make(map[string]*chatWindow)
    }
    cw := t.chats[chatID]
    if cw == nil {
        cw = &chatWindow{}
        t.chats[chatID] = cw
    }
    
    now := time.Now()
    cw.minute = append(cw.minute, now)
    cw.hour = append(cw.hour, now)
    
    // 清理过期记录
    minuteCutoff := now.Add(-1 * time.Minute)
    hourCutoff := now.Add(-1 * time.Hour)
    
    cw.minute = filterAfter(cw.minute, minuteCutoff)
    cw.hour = filterAfter(cw.hour, hourCutoff)
}

func (t *chatRateTracker) check(chatID string) (minuteCount, hourCount int, needWait time.Duration) {
    t.mu.Lock()
    defer t.mu.Unlock()
    
    cw := t.chats[chatID]
    if cw == nil {
        return 0, 0, 0
    }
    
    minuteCount = len(cw.minute)
    hourCount = len(cw.hour)
    
    // 每分钟限制 30，预留 5 条 buffer
    if minuteCount >= 25 {
        oldest := cw.minute[0]
        needWait = oldest.Add(1 * time.Minute).Sub(time.Now())
        if needWait < 0 {
            needWait = 0
        }
    }
    
    // 每小时限制 1000，预留 50 条 buffer
    if hourCount >= 950 {
        oldest := cw.hour[0]
        hourWait := oldest.Add(1 * time.Hour).Sub(time.Now())
        if hourWait > needWait {
            needWait = hourWait
        }
    }
    
    return minuteCount, hourCount, needWait
}
```

#### 3b. 发送前预判节流

在每次实际发送前检查速率，如果接近限制就等待：

```
// 在 sendStreamFrameAndWaitAck 和 Send() 调用 writeAndWaitAck 之前:

minCount, hourCount, needWait := p.rateTracker.check(rc.chatID)
if needWait > 0 {
    slog.Warn("wecom-ws: rate limit approaching, throttling",
        "chat_id", rc.chatID,
        "minute_count", minCount,
        "hour_count", hourCount,
        "wait", needWait)
    
    select {
    case <-time.After(needWait):
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

#### 3c. 发送后记录

```
// 在 writeAndWaitAck 成功返回后:
p.rateTracker.record(rc.chatID)
```

#### 3d. 846607 重试逻辑

当收到 846607 错误时，进行指数退避重试（最多 3 次）：

```
// 在 writeAndWaitAck 收到 ack 错误后:
if err != nil && strings.Contains(err.Error(), "846607") {
    for retry := 0; retry < 3; retry++ {
        backoff := time.Duration(math.Pow(2, float64(retry))) * 3 * time.Second
        slog.Warn("wecom-ws: rate limited, retrying",
            "req_id", reqID,
            "retry", retry+1,
            "backoff", backoff)
        
        select {
        case <-time.After(backoff):
        case <-ctx.Done():
            return ctx.Err()
        }
        
        if err = p.writeJSON(frame); err != nil {
            continue
        }
        // wait for ack again
        select {
        case err = <-ch:
            if err == nil {
                return nil
            }
            if !strings.Contains(err.Error(), "846607") {
                return err
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

#### 3e. 846608 特殊处理

流式过期不可重试，但可以尝试用 `aibot_send_msg` 发送最终内容作为 fallback：

```
if err != nil && strings.Contains(err.Error(), "846608") {
    slog.Warn("wecom-ws: stream expired, falling back to aibot_send_msg",
        "req_id", reqID)
    // 标记 stream 已过期，后续用 aibot_send_msg 发送
    p.markStreamExpired(rc.chatID, rc.streamID)
}
```

---

## 优化 4: 安抚消息（3 分钟无输出更新流式消息）

### 问题
日志中 `slow agent first event` 最长 54 秒，`slow agent send` 最长 58 分钟。用户在这些等待期间没有反馈，会认为卡住了。

### 方案
不发送新消息（浪费配额），而是更新已有的流式消息。

```
文件: core/engine_turn.go

// 在 processInteractiveEvents 中增加安抚 timer:

reassureInterval := 3 * time.Minute  // 可配置
reassureTimer := time.NewTimer(reassureInterval)
defer reassureTimer.Stop()

// 在 event loop 的 select 中增加:
case <-reassureTimer.C:
    // 如果已经有流式预览，更新它
    if sp.hasPreview() {
        sp.reassure("⏳ 仍在处理中，请耐心等待...")
    } else {
        // 还没有流式预览，创建一个
        sp.ensurePreview("⏳ 正在处理您的请求...")
    }
    reassureTimer.Reset(reassureInterval)
```

```
文件: core/streaming.go

// streamPreview 增加方法:

func (sp *streamPreview) hasPreview() bool {
    return sp.handle != nil
}

func (sp *streamPreview) ensurePreview(content string) {
    if sp.handle == nil {
        sp.start(content)  // 调用 SendPreviewStart
    } else {
        sp.update(content)  // 调用 UpdateMessage
    }
}

func (sp *streamPreview) reassure(content string) {
    if sp.handle != nil {
        sp.update(content)
    }
}
```

每次收到文本 event 时重置安抚 timer：

```
// 在 event loop 中处理 EventText/EventThinking 时:
reassureTimer.Reset(reassureInterval)
```

### 安抚消息的配额考量
- 安抚消息使用 `aibot_respond_msg` 更新已有 stream（`UpdateMessage`），不算新消息
- 如果还没有 stream（agent 在执行工具但无文本输出），`ensurePreview` 会创建一个新 stream，这会消耗 1 条配额
- 安抚消息每 3 分钟最多发 1 条，在正常 10 分钟流式窗口内最多 3 条
- 如果 per-chat 速率追踪显示配额紧张，可以跳过安抚

---

## 优化 5: Token 使用量提取修复

### 问题
所有 `turn complete` 日志中 `input_tokens=0 output_tokens=0`。

根因：
1. `acpSession.Send()` emit `EventResult` 时没有设置 `InputTokens`/`OutputTokens`
2. `maybeAbsorbUsageUpdate()` 没有解析 `_meta.usage.prompt_tokens` / `_meta.usage.completion_tokens`

### 方案

```
文件: agent/acp/session.go

// 修改 maybeAbsorbUsageUpdate，解析 _meta.usage:

func (s *acpSession) maybeAbsorbUsageUpdate(params json.RawMessage) {
    var wrap struct {
        Update json.RawMessage `json:"update"`
    }
    if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
        return
    }
    var head struct {
        Kind string `json:"sessionUpdate"`
        Used int    `json:"used"`
        Size int    `json:"size"`
        Meta *struct {
            Usage *struct {
                PromptTokens     int `json:"prompt_tokens"`
                CompletionTokens int `json:"completion_tokens"`
                TotalTokens      int `json:"total_tokens"`
            } `json:"usage"`
        } `json:"_meta"`
    }
    if json.Unmarshal(wrap.Update, &head) != nil || head.Kind != "usage_update" {
        return
    }
    
    snap := &core.ContextUsage{
        UsedTokens:    head.Used,
        ContextWindow: head.Size,
        TotalTokens:   head.Used,
    }
    if head.Meta != nil && head.Meta.Usage != nil {
        snap.InputTokens = head.Meta.Usage.PromptTokens
        snap.OutputTokens = head.Meta.Usage.CompletionTokens
        if head.Meta.Usage.TotalTokens > 0 {
            snap.TotalTokens = head.Meta.Usage.TotalTokens
        }
    }
    
    s.usageMu.Lock()
    s.usageSnapshot = snap
    s.usageMu.Unlock()
}
```

```
// 修改 Send()，emit EventResult 时填入 token 值:

func (s *acpSession) Send(ctx context.Context, sessionID string, content string, ...) error {
    // ... 现有逻辑 ...
    
    // 读取 usage snapshot
    s.usageMu.RLock()
    snap := s.usageSnapshot
    s.usageMu.RUnlock()
    
    evt := core.Event{
        Type:      core.EventResult,
        SessionID: sid,
        Done:      true,
    }
    if snap != nil {
        evt.InputTokens = snap.InputTokens
        evt.OutputTokens = snap.OutputTokens
    }
    s.emit(evt)
}
```

---

## 实施优先级

| 优先级 | 优化 | 影响面 | 复杂度 | 收益 |
|--------|------|--------|--------|------|
| P0 | Token 提取修复 | 小（1 个文件） | 低 | 修复日志中 token 始终为 0 的问题 |
| P0 | Graceful stop 日志 | 小（1 个文件） | 低 | 便于定位进程退出慢的原因 |
| P1 | Session 360min 清理 | 中（3 个文件） | 中 | 避免僵尸 ACP 进程积累 |
| P1 | WeCom 速率追踪 + 重试 | 中（2 个文件） | 中 | 减少 846607 错误 |
| P2 | 安抚消息 | 中（2 个文件） | 中 | 改善用户体验 |

---

## 配置变更

```toml
# config.toml 新增/修改项

[project.auto-bugfix]
# 原来 reset_on_idle_mins 默认 0（关闭），现在默认 360
reset_on_idle_mins = 360

# 安抚消息间隔（可选，默认 3 分钟）
reassure_interval_mins = 3

# WeCom 速率限制预留 buffer（可选）
wecom_rate_minute_buffer = 5    # 预留 5 条（30-5=25 条阈值）
wecom_rate_hour_buffer = 50     # 预留 50 条（1000-50=950 条阈值）
```

---

## 测试用例

### 测试框架与约定

- **框架**: Go 标准 `testing` + 内联 stub（不使用 testify/assert）
- **命名**: `TestFunctionName_Scenario`（PascalCase + 下划线分隔场景）
- **断言**: `t.Fatalf` / `t.Errorf`（不使用 testify assert）
- **并发测试**: `sync.WaitGroup` + goroutine
- **超时断言**: `time.After` in select
- **Helper**: `t.Helper()` 标记辅助函数

### 测试 1: Token 使用量提取修复

**文件**: `agent/acp/session_test.go`（新建）

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestMaybeAbsorbUsageUpdate_ParsesMetaUsage` | 完整的 usage_update JSON 包含 `_meta.usage` | `prompt_tokens`、`completion_tokens`、`total_tokens` 被正确提取到 `usageSnapshot` |
| `TestMaybeAbsorbUsageUpdate_WithoutMetaUsage` | 旧版 usage_update 没有 `_meta` 字段 | `InputTokens`/`OutputTokens` 为 0，`UsedTokens` 仍然正确 |
| `TestMaybeAbsorbUsageUpdate_NotUsageUpdate` | 非 usage_update 类型的 session/update | 不更新 `usageSnapshot` |
| `TestSend_EventResultHasTokens` | Send() 完成后 emit 的 EventResult | `InputTokens` 和 `OutputTokens` 不为 0（当 usageSnapshot 存在时） |
| `TestSend_EventResultNoTokensWhenNil` | usageSnapshot 为 nil 时 | `InputTokens`/`OutputTokens` 为 0 |

**测试数据结构**（内联 fixture）:

```go
// 完整 usage_update JSON（含 _meta.usage）
const usageUpdateWithMeta = `{
  "sessionUpdate": "usage_update",
  "used": 48152,
  "size": 1000000,
  "cost": {"amount": 29.15, "currency": ""},
  "_meta": {
    "codebuddy.ai/requestId": "req-123",
    "usage": {
      "prompt_tokens": 48152,
      "completion_tokens": 163,
      "total_tokens": 48315,
      "completion_tokens_details": {
        "accepted_prediction_tokens": 0,
        "reasoning_tokens": 0
      },
      "prompt_tokens_details": {
        "cached_tokens": 20826
      }
    }
  }
}`

// 旧版 usage_update JSON（无 _meta）
const usageUpdateLegacy = `{
  "sessionUpdate": "usage_update",
  "used": 1000,
  "size": 100000
}`
```

---

### 测试 2: Graceful Stop 日志细节

**文件**: `agent/acp/session_test.go`（追加）

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestClose_ExitsOnStdinClose` | 进程在 stdin close 后立即退出 | Phase 1 日志包含 "exited after stdin close" |
| `TestClose_ExitsOnSIGTERM` | stdin close 超时 → SIGTERM 后退出 | Phase 1 超时日志 + Phase 2 退出日志 |
| `TestClose_ExitsOnSIGKILL` | SIGTERM 超时 → SIGKILL 后退出 | 三个阶段日志链完整 |
| `TestClose_SIGKILLTimeout` | SIGKILL 后仍不退出 | Phase 3 超时日志（ERROR 级别） |
| `TestProcessExitedCleanly_InfoLevel` | 进程正常退出 | 日志级别为 INFO 而非 WARN |

---

### 测试 3: Session 空闲 360 分钟自动清理

**文件**: `core/engine_test.go`（追加）

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestReapIdleSessions_KillsIdleSession` | 有一个 agent 进程超过 360 分钟无事件 | reaper 扫描后 `cleanupInteractiveState` 被调用 |
| `TestReapIdleSessions_SkipsActiveSession` | agent 进程最近有事件 | 不被 kill |
| `TestReapIdleSessions_SkipsDeadSession` | agent 进程已退出（`Alive()==false`） | 不重复处理 |
| `TestReapIdleSessions_SkipsNilAgentSession` | `agentSession == nil` | 不 panic |
| `TestReapIdleSessions_DisabledWhenResetOnIdleZero` | `resetOnIdle = 0` | reaper 不执行清理 |
| `TestReapIdleSessions_ConcurrentAccess` | reaper 和正常 turn 处理并发 | 无 race condition |
| `TestLastEventTime_UpdatedOnEachEvent` | processInteractiveEvents 中收到 event | `state.lastEventTime` 被更新 |
| `TestLastEventTime_FallsBackToTurnStart` | 没有收到过 event 的 session | reaper 使用 `turnStartTime` 作为回退 |

**测试辅助**:

```go
// 用于测试 reaper 的 fake agent session
type idleTestAgentSession struct {
    alive     bool
    lastEvent time.Time
}

func (s *idleTestAgentSession) Alive() bool { return s.alive }
// ... 实现 core.AgentSession 接口的其余方法 ...
```

**关键**: 测试中使用 `resetOnIdle = 1 * time.Millisecond` 来加速验证，不等待真实的 360 分钟。

---

### 测试 4: WeCom 频率限制追踪

**文件**: `platform/wecom/websocket_test.go`（追加）

#### 4a. chatRateTracker 单元测试

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestChatRateTracker_RecordAndCheck` | 记录 25 条消息后检查 | `minuteCount=25`, `needWait > 0` |
| `TestChatRateTracker_BelowThreshold` | 记录 10 条消息后检查 | `needWait = 0` |
| `TestChatRateTracker_HourWindow` | 记录 950 条（超过小时阈值） | `hourCount=950`, `needWait > 0` |
| `TestChatRateTracker_CleanupExpired` | 记录消息后等待 61 秒 | 旧的分钟记录被清理 |
| `TestChatRateTracker_MultipleChats` | 两个不同 chatID | 计数独立，互不影响 |
| `TestChatRateTracker_ConcurrentAccess` | 10 个 goroutine 并发记录 | 无 race，计数准确 |
| `TestChatRateTracker_EmptyTracker` | 未记录任何消息时检查 | 返回 `0, 0, 0` |

#### 4b. 846607 重试逻辑测试

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestWriteAndWaitAck_RateLimitedRetry` | ack 返回 846607 错误 | 最多重试 3 次，指数退避 |
| `TestWriteAndWaitAck_RateLimitedRetrySuccess` | 第 2 次重试成功 | 返回 nil，记录成功 |
| `TestWriteAndWaitAck_RateLimitedRetryExhausted` | 3 次重试全部失败 | 返回最后的 846607 错误 |
| `TestWriteAndWaitAck_RateLimitedRetryNonRateError` | 重试期间收到非 846607 错误 | 立即返回该错误，不再重试 |
| `TestWriteAndWaitAck_RateLimitedRetryContextCancelled` | 重试期间 context 取消 | 返回 context 错误 |

#### 4c. 846608 流式过期测试

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestWriteAndWaitAck_StreamExpired_NoRetry` | ack 返回 846608 | 不重试，记录 WARN |
| `TestWriteAndWaitAck_StreamExpired_Fallback` | 846608 后调用 Send() | 使用 `aibot_send_msg` fallback |

#### 4d. 发送前预判节流测试

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestSendStreamFrame_WaitsWhenRateLimited` | 计数器显示接近阈值 | 发送前等待一段时间 |
| `TestSendStreamFrame_SkipsWaitWhenUnderLimit` | 计数器显示远低于阈值 | 直接发送 |
| `TestSend_ProactiveMessage_WaitsWhenRateLimited` | `Send()` 路径也做速率检查 | 同样等待 |

---

### 测试 5: 安抚消息

**文件**: `core/engine_test.go`（追加）

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestReassureMessage_SendsAfter3MinNoOutput` | 3 分钟无文本事件 | `UpdateMessage` 被调用，内容为安抚文本 |
| `TestReassureMessage_ResetsOnTextEvent` | 收到文本事件后 | timer 重置，不触发安抚 |
| `TestReassureMessage_ResetsOnToolProgress` | 收到工具进度事件后 | timer 重置 |
| `TestReassureMessage_NoPreviewCreatesOne` | 3 分钟仍无 preview | `SendPreviewStart` 被调用 |
| `TestReassureMessage_UsesExistingPreview` | 已有 preview | `UpdateMessage` 更新已有 stream |
| `TestReassureMessage_IntervalConfigurable` | 自定义间隔 1 分钟 | 按自定义间隔触发 |
| `TestReassureMessage_StopsOnTurnComplete` | turn 完成后 | 不再发送安抚消息 |
| `TestReassureMessage_MultipleTriggers` | 6 分钟无输出 | 第 3 分钟和第 6 分钟各触发一次 |

**测试辅助**: 使用 `reassureInterval = 10 * time.Millisecond` 加速验证。

---

### 测试 6: 集成测试

**文件**: `tests/integration/wecom_rate_limit_test.go`（新建，build tag: `integration`）

| 测试函数 | 场景 | 验证点 |
|----------|------|--------|
| `TestIntegration_WeComRateTrackerFullFlow` | 完整发送流程 + 速率追踪 | 计数器在 `writeAndWaitAck` 成功后递增 |
| `TestIntegration_WeComStreamUpdateRateLimit` | 大量流式更新触发节流 | 接近 25/min 时发送变慢 |
| `TestIntegration_WeComRateLimitRecovery` | 触发 846607 后重试成功 | 消息最终送达 |

---

### 测试执行

```bash
# 单元测试（快速，无需外部依赖）
go test ./agent/acp/ -run "TestMaybeAbsorbUsageUpdate|TestSend_EventResult|TestClose_"
go test ./core/ -run "TestReapIdleSessions|TestLastEventTime|TestReassureMessage"
go test ./platform/wecom/ -run "TestChatRateTracker|TestWriteAndWaitAck_RateLimited|TestSendStreamFrame_Waits"

# 并发安全测试
go test ./platform/wecom/ -run "TestChatRateTracker_Concurrent" -race
go test ./core/ -run "TestReapIdleSessions_Concurrent" -race

# 集成测试（需要外部依赖）
go test -tags=integration ./tests/integration/ -run "TestIntegration_WeComRate"
```
