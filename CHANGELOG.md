# Changelog

## v1.0.2 (2026-08-17)

### Changed

- **Cron shell 任务空输出不再推送消息**：shell 类型的 cron 任务执行成功且 stdout 无输出时，不再发送 `✅ (no output)` 消息，改为静默完成；仅当脚本有实际输出或失败时才推送结果。由脚本决定是否发送消息。

## v1.0.1 (2026-08-13)

### Changed

- **品牌更名为 heron-connect**：项目从 cc-connect-qhn 更名为 heron-connect，npm 包改为 `@qinghuangniao/heron-connect`，二进制/命令、数据目录、GitHub 仓库与 Go module 路径全部同步更新。
- **移除 Web 前端**：删除内嵌 React 前端与 `heron-connect web` 命令，保留 HTTP Management API。
- **修正 CodeBuddy 安装包名**：从错误的 `@anthropic-ai/codebuddy-code` 更正为腾讯官方的 `@tencent-ai/codebuddy-code`。

## v2.0.6 (2026-08-12)

### Added

- **企微引用消息标注**：当用户引用（quote/reply）消息时，为引用原文添加 `[引用消息]` 前缀，使模型能够区分引用内容与用户当前输入；文本和语音引用均支持。

### Changed

- **ACP client 版本号**：ACP `initialize` 中的 `clientInfo.version` 不再硬编码，改为读取编译时注入的版本号，与主程序版本一致。

## v2.0.5 (2026-08-12)

### Fixed

- **ACP 子 Agent `_meta` 解析位置**：将 `parentToolCallId` / `parentToolUseId` 的解析优先级改为先读 `params.update._meta`（ACP 协议实际位置），再回退 `params._meta`，修复 `tool_messages = false` 仍展示子 Agent 内容的问题。
- **启动日志版本号**：`cc-connect-qhn is running` 日志增加 `version` 字段，便于从日志确认实际运行的版本。

## v2.0.4 (2026-08-11)

### Added

- **`cron_data_dir` 配置**：在 `[cron]` 段新增 `cron_data_dir` 字段，可显式指定 cron `jobs.json` 的存储目录；空值时回退到顶层 `data_dir`，保持向后兼容。支持相对路径（基于进程工作目录解析），方便多环境部署。

### Docs

- **IM 群聊会话隔离说明**：新增 `docs/session-isolation.zh-CN.md`，说明各平台群聊的会话隔离/共享规则、`share_session_in_channel`、`thread_isolation` 及选型建议。

## v2.0.3 (2026-08-11)

### Fixed

- **ACP 子 Agent 元数据兼容**：同时支持 CodeBuddy 实际使用的扁平字段 `_meta["codebuddy.ai/parentToolCallId"]` 与原有嵌套字段 `_meta["codebuddy.ai"]["parentToolUseId"]`；扁平字段存在时优先使用，确保实际 ACP 通知中的子 Agent 事件能被正确识别和按 `tool_messages` 控制显示。

## v2.0.2 (2026-08-10)

### Fixed

- **ACP 子 Agent 显示控制**：识别 CodeBuddy ACP `Agent` 工具调用及其关联的子 Agent 事件；`tool_messages = false` 时隐藏子 Agent 的文本、思考和工具细节，同时保留主 Agent 回复。
- **企微 WebSocket 长回复**：流式预览使用 20,480-byte 内容上限，并保留安全展示余量；超出预览预算后实时续发后续消息，不再插入“内容较长”提示。
- **企微流式格式与投递可靠性**：分段保持 UTF-8 边界和 Markdown 代码围栏完整；ACK 超时不再被视为已投递；终态帧提交后阻止旧的预览和工具进度更新覆盖最终回复。
- **企微工具进度**：工具开始或完成时可建立或复用流式预览，使工具进度与已有回复共同展示，并在最终回复中清除进度区域。

### Tests

- 增加 ACP 子 Agent 标记、嵌套关系和 `tool_messages` 显示控制的覆盖。
- 增加企微流式 UTF-8、字节上限、代码围栏续传、分段失败重试、终态屏障和工具进度的回归测试。

## v2.0.1 (2026-08-04)

**New agent + model refresh**: Adds `codebuddy` headless CLI driver, completes the `cmd/cc-connect` → `cmd/cc-connect-qhn` directory rename, refreshes stale model lists across all agents and provider presets.

### Added

- **`type = "codebuddy"` agent**: Headless stream-json CLI driver (`codebuddy -p <prompt> --output-format stream-json`), following the same per-turn spawn pattern as qoder/gemini/kimi. Supports `mode`, `model`, `SkillDirs`, `MemoryFileProvider`, and `ContextCompressor`. Includes example config (`examples/feishu-codebuddy.toml`).

### Changed

- **`cmd/cc-connect` → `cmd/cc-connect-qhn`**: Directory renamed to complete the v2.0.0 branding migration.
- **Model lists refreshed across all agents**: `gemini` adds `gemini-3.5-flash`; `kimi` adds `kimi-k3`; `opencode` updates `gpt-4o` → `gpt-5.4`; `codex` adds `gpt-5.4`/`gpt-5.4-mini`; `cursor` fallback models updated.
- **`provider-presets.json`**: All Claude presets now default to `claude-sonnet-5`; Gemini presets default to `gemini-3.1-pro-preview` with `gemini-3.5-flash`.
- **Codex docs**: Added third-party provider examples (DeepSeek, MiniMax) with `wire_api` guidance in `examples/feishu-codex.toml` and `config.example.toml`.

### Fixed

- **codebuddy model naming**: Fixed dotted (`claude-sonnet-4.6`) → dashed (`claude-sonnet-4-6`) to match rest of repo.
- **Stale model IDs in tests**: Updated `claude-opus-4-7` → `4-8`, `kimi-k2` → `k3`, `gemini-2.5-*` → `3.1-*` across test fixtures.

## v2.0.0 (2026-07-31)

**Breaking branding change**: All user-facing log/output strings renamed from `cc-connect` to `cc-connect-qhn`. Binary filename, GitHub release assets, updater APIs all switched to the fork's own identity.

### Breaking

- **Binary renamed**: Go binary output changed from `cc-connect` to `cc-connect-qhn`. Makefile `APP`, npm wrapper scripts (`run.js`, `install.js`, `release-assets.js`), and release asset naming all updated accordingly.
- **GitHub release assets renamed**: `cc-connect-v2.0.0-darwin-arm64.tar.gz` → `cc-connect-qhn-v2.0.0-darwin-arm64.tar.gz`.
- **All CLI help/output text renamed**: `Usage: cc-connect [flags]` → `Usage: cc-connect-qhn [flags]`, all examples, error messages, daemon status messages, platform setup prompts updated across ~30 Go files.
- **Log message renamed**: `"cc-connect is running"` → `"cc-connect-qhn is running"`.
- **Agent system prompt renamed**: Agent-visible context (system prompt, sender markers, instruction marker) uses `cc-connect-qhn` instead of `cc-connect`.
- **Daemon service renamed**: Systemd unit description `cc-connect` → `cc-connect-qhn`, launchd label `com.cc-connect.service` → `com.cc-connect-qhn.service`, service name const `cc-connect` → `cc-connect-qhn`.
- **ACP session names renamed**: ACP `clientInfo.name` and session titles use `cc-connect-qhn`.
- **i18n strings renamed**: All localized help/status strings across 5 languages updated.
- **Weixin channel version renamed**: `cc-connect-weixin/1.0` → `cc-connect-qhn-weixin/1.0`.
- **Windows daemon script renamed**: `cc-connect-daemon.ps1` → `cc-connect-qhn-daemon.ps1`.

### Changed

- **Updater repo URLs switched from upstream to fork**: `CheckForUpdate` and `SelfUpdate` in `core/updater.go`, plus CLI `update` command in `cmd/cc-connect-qhn/update.go`, now query `janostudio/cc-connect-qhn` instead of `chenhg5/cc-connect` / `cg33/cc-connect`.
- **npm package version sync fixed**: `syncNpmPackageVersion` now uses `strings.Contains` for scoped package `@qinghuangniao/cc-connect-qhn` name matching.

### NOT changed (preserved for compatibility)

- **Go import paths**: `github.com/chenhg5/cc-connect/...` unchanged.
- **`.cc-connect-qhn` data directory**: Already had `-qhn` suffix, unchanged.
- **npm global command**: `cc-connect-qhn` unchanged (already correct).

### Why v2.0.0

This is a semver-major bump because:
1. The `ServiceName` const and launchd label change means existing daemon installations need re-install.
2. The agent system prompt change means agents using memory files with old `cc-connect` instructions will get them refreshed on next run.
3. The updater URL change means the auto-update path switches from upstream to the fork — users on old versions won't see updates from the new repo.

### Files changed (35 files)

`agent/acp/`: list_sessions.go, session.go
`cmd/cc-connect-qhn/`: main.go, cron.go, send.go, daemon.go, provider.go, feishu.go, weixin.go, sessions.go, update.go, relay.go, session_id.go, config_cmd.go, doctor_runas.go, instance_lock.go, web.go
`core/`: engine.go, engine_bind_cmds.go, i18n.go, interfaces.go, runas_check.go, updater.go, web_manager.go
`daemon/`: manager.go, systemd.go, launchd.go, windows.go
`platform/weixin/`: client.go
`npm/`: run.js, install.js, release-assets.js
`Makefile`
Test files: update_test.go, engine_test.go

## v1.4.9 (2026-07-29)

Personal fork session idle reaper, WeCom rate-limit tracking, reassurance messages, token usage fix, and log diagnostics hardening for `@qinghuangniao/cc-connect-qhn`.

### Notes

- **Session idle reaper (360min)**: Added background goroutine `startSessionReaper` that periodically scans `interactiveStates` and kills agent processes that have received no events for longer than `resetOnIdle` (default 360 min, configurable via `reset_on_idle_mins`). This prevents zombie ACP subprocesses from accumulating. The reaper uses `lastEventTime` (updated on every agent event) with `turnStartTime` fallback. Disabled when `resetOnIdleMins = 0`.

- **WeCom per-chat rate-limit tracking**: Added `chatRateTracker` in `platform/wecom/rate_tracker.go` that counts sends per chatID with 1-min and 1-hour sliding windows. Before each `aibot_respond_msg` and `aibot_send_msg` call, the tracker checks against WeCom limits (30/min, 1000/hour per chat) with configurable buffer (5/min, 50/hour). If approaching limits, sends are throttled with a wait. After successful ack, the send is recorded.

- **WeCom 846607 retry with exponential backoff**: `writeAndWaitAck` now retries rate-limited sends (errcode=846607) up to 3 times with 3s/6s/12s backoff. Error 846608 (stream expired) is detected and not retried — returns immediately.

- **Reassurance messages during long waits**: Added a 1-minute timer in `processInteractiveEvents` that sends "⏳ 正在处理您的请求..." via stream preview when no agent output has been received. Uses WeCom's full-replacement stream semantics (`aibot_respond_msg`), so reassurance text is automatically replaced when real output arrives. No new messages are created — only existing stream content is updated.

- **Token usage extraction fix**: `maybeAbsorbUsageUpdate` now parses `_meta.usage.prompt_tokens` → `InputTokens` and `_meta.usage.completion_tokens` → `OutputTokens` from ACP `usage_update` notifications. `acpSession.Send()` reads the `usageSnapshot` and populates `EventResult.InputTokens`/`OutputTokens` before emitting, so `turn complete` logs now show actual token counts instead of always 0.

- **Graceful stop logging**: `acpSession.Close()` now logs per-phase elapsed time (stdin close, SIGTERM, SIGKILL) with `session_id`. Changed `process exited cleanly` from WARN to INFO (normal exit is not a warning).

- **Log diagnostics hardening**: Added `session_key`, `platform`, `msg_id` to 15+ error/warn logs in `engine_turn.go` (agent error, prompt send, rich card send, streaming card finalize, channelClosed). Added `session_key`, `platform`, `user`, `request_id`, `tool` to permission error logs in `engine.go`. Added `chat_type` to WeCom send errors. Standardized `"err"` → `"error"` key naming across codebase. Upgraded streaming preview degradation from Debug to Warn with platform name.

### Tests

- `agent/acp/session_test.go`: 4 new tests (`TestMaybeAbsorbUsageUpdate_*`) — parses meta usage, legacy format, non-usage update, zero total_tokens fallback.
- `core/engine_test.go`: 6 new tests (`TestReapIdleSessions_*`) — kills idle, skips active, skips dead, skips nil agent, disabled when zero, falls back to turnStartTime.
- `platform/wecom/websocket_test.go`: 10 new tests (`TestChatRateTracker_*`, `TestWriteAndWaitAck_StreamExpiredNoRetry`, `TestIsErrCode`) — rate tracking, cleanup, concurrent access, error code detection, stream expired no-retry.

### Docs

- `docs/wecom-optimization.md`: Full optimization plan with problem analysis, implementation details, test cases, and configuration.
- `.codebuddy/plans/stellar-thunder-newton-BxOjesy5.md`

## v1.4.8 (2026-07-23)

Personal fork session switch / list fix, error sanitization, and `/list` fallback hardening for `@qinghuangniao/cc-connect-qhn`.

### Notes
- **Fix `/list` and `/switch` user isolation**: `sessionsFromSessionManager` previously used `AllSessions()` (cross-user). Changed to `ListSessions(userKey)`, scoped to `msg.SessionKey`. Added the same fallback in `cmdSwitch` so `/switch` works after restart when the agent backend reports no sessions (ACP without `session/list`).
- **`/list` shows last user message as summary**: fallback path now fills `MessageCount=len(History)` and `Summary` from the last `role=="user"` entry (truncated to 30 runes), making sessions distinguishable after restart.
- **EventError desensitization**: all 4 code paths that relay agent errors to IM users (foreground EventError, unsolicited EventError, `Send` error, dropped-queue notifications) now route through `sanitizeAgentError` → `sanitizeAgentErrorMessage`. The function returns localized i18n messages for known patterns (`Session not found` → `MsgSessionNotFound`, quota/rate-limit → `MsgModelQuotaExceeded`, ACP internal errors → `MsgAgentInternalError`, process exits → `MsgAgentProcessExited`) and default-denies everything else to a generic `MsgAgentInternalError`. Raw errors remain in logs and hooks only.
- 3 new i18n keys: `MsgAgentInternalError`, `MsgAgentProcessExited`, `MsgAgentUnsupportedMethod` (5 languages each).
- **Fix pre-existing flaky `TestGetModelAndReasoningEffort_FromRuntimeConfigWhenUnset`**: shell mock pattern `*"method":"initialize"*` incorrectly matched `"initialized"` notification, spawning unnecessary `printf|sed` subprocesses under load. Changed patterns to `*"method":"initialize","*` / `*"method":"config/read","*` and moved `id` extraction inside case branches. Bumped test timeout to 5 s.

### Tests
- `TestSessionsFromSessionManager_*` updated for `userKey` parameter, plus 4 new tests: `_userIsolation`, `_fillsSummaryAndCount`, `_summaryTruncates`.
- `TestSanitizeAgentErrorMessage` (17 cases covering known-friendly, ACP-internal, stderr, stack traces, path-like, unknown).
- `tests/release_local/engine_matrix/restart_persistence_test.go` (5 restart-scenario tests with a fake ACP agent).

### Docs
- `.codebuddy/plans/toasty-vortex-einstein-liQoVUJT.md`

## v1.4.7 (2026-07-21)

Personal fork `/model` live switch, ACP refusal error surfacing, and orphaned subprocess cleanup for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix `/model` not applying to the current session. Previously `/model` always killed the running agent subprocess and deferred the model change to the next incoming message (which respawned with `--model <new> --resume <id>`). This worked but was not "immediate" — the `/model` reply itself was sent by cc-connect, not the new model, and on ACP backends the running subprocess was torn down even though ACP supports in-process model swaps. Introduced `core.LiveModelSwitcher` interface (mirroring the existing `LiveModeSwitcher` pattern used by `/mode`). `cmdModel` now first attempts `applyLiveModelChange` on the running `state.agentSession`; on success the subprocess is kept alive and the reply appends a localized "Current session updated immediately." notice. On failure (or when the agent doesn't implement the interface) it falls back to the legacy `cleanupInteractiveState` + `--resume` respawn path — non-ACP agents (opencode/codex/claudecode/qoder/pi/kimi/cursor/gemini/iflow) keep their existing behavior since their model is a `--model` CLI flag baked at spawn time.
- Implement `LiveModelSwitcher` on `*acpSession`. The new `SetLiveModel` method reuses the existing `SetModel` RPC path (`session/set_model` with `session/set_config_option` fallback), which was previously only invoked at session spawn. ACP backends now hot-swap models without restarting the subprocess.
- Mirror the live-switch branch in both card-action entry points (`handleModelCardAction`, `performModelSwitchAsync`) so `/model` invoked via Feishu/WeCom card buttons behaves identically to the slash command.
- Fix ACP `refusal` responses being silently sent to the user as "(空响应)". CodeBuddy wraps model-side failures (quota exceeded 429, 500 internal errors) in a JSON-RPC **success** envelope with `stopReason:"refusal"` and the real error in `_meta["codebuddy.ai/errorMessage"]`. `acpSession.Send()` previously discarded the entire `result` and unconditionally emitted `EventResult{Done:true}`, so the engine's empty-response fallback kicked in and the user got "(空响应)" while the real error was buried in logs. `Send()` now parses the `result` and, on refusal/error, emits `EventError` with the parsed `payload.Message` verbatim instead of `EventResult`. The engine's existing `EventError` handler finalizes the progress card as failed and sends the error to the user. Added `MsgModelQuotaExceeded` i18n key (5 languages) wired via the `agentErrorHandlers` substring table so quota errors get a friendly localized notice; any other refusal error passes through unchanged as `"❌ 错误: <原始 message>"` (never empty). Added `extractACPReturnError` helper that returns the full error message verbatim — never an empty string — so unmatched errors always surface to the user instead of looking like a cc-connect-qhn bug.
- New i18n keys: `MsgModelChangedLive` ("✅ Current session updated immediately." in 5 languages), `MsgModelQuotaExceeded` ("⚠️ AI 服务暂时不可用：使用额度已耗尽，请稍后再试。" in 5 languages).
- **Eliminate orphaned CLI subprocesses on session end / restart.** Every `codebuddy --acp` (and other CLI) subprocess that died or was replaced was leaked because `Close()` was never called on it — stdin stayed open, no SIGKILL was sent, and on cc-connect restart via `syscall.Exec` all children became orphans (PPID=1), accumulating over weeks (34 stale `--acp` processes, ~8.9 GB RSS observed). Five root causes fixed:
  - `acpSession.Close()` never closed the events channel — the engine's `channelClosed` cleanup path (which calls `cleanupInteractiveState`) never fired for ACP sessions, leaving dead `interactiveState` entries in the map. Now closes via `sync.Once` (`closeEvents` helper) so the engine observes termination correctly; idempotent and safe against concurrent/Double `Close()`.
  - `EventError` path in `processInteractiveEvents` returned without calling `cleanupInteractiveState` when the agent session was dead, leaking the subprocess (stdin never closed, no SIGKILL). Now calls `cleanupInteractiveState` when `!Alive()`, which closes the agent session and removes the state from the map. The previous `notifyDroppedQueuedMessages` call was removed to avoid duplication (`cleanupInteractiveState` already drains queued messages internally).
  - `Engine.Stop()` called `state.agentSession.Close()` serially with no timeout — a single stuck session blocked restart, and `syscall.Exec` then orphaned all still-running children (PPID=1). Now closes all sessions in parallel with a 180 s batch timeout; each session also stops its unsolicited reader and resolves pending permissions before `closeAgentSessionWithTimeout`.
  - `getOrCreateInteractiveStateWith` silently overwrote dead sessions without calling `Close()` when a new message arrived — added defensive cleanup (`stopUnsolicitedReader` + `markStopped` + `closeAgentSessionWithTimeout` + `delete`) so a dead state is always torn down before a fresh one is created.
  - No process group isolation (except Codex) — grandchildren (shell, npm, git, …) spawned by the CLI survived restart as orphans. Added shared `core.PrepareCmdForKill` / `core.ForceKillProcessGroup` / `core.SignalProcessGroup` helpers (Unix: `Setpgid` + negative-PID kill; Windows: `CREATE_NEW_PROCESS_GROUP` + `taskkill /T /F`) and wired them into all 9 agents (ACP, Claude Code, OpenCode, Gemini, Cursor, Pi, Kimi, Qoder, iFlow) plus the ACP `probeSpawn` one-shot. iFlow uses PTY (`pty.Start` calls `setsid` internally) so `Setpgid` is intentionally skipped there to avoid `operation not permitted`; the `osCmd` reference is still saved for `ForceKillProcessGroup` on timeout.
- **Fix pre-existing flaky `TestQueuedMessagePreservesFiles`.** `Engine.Stop()` did not wait for in-flight `processInteractiveMessageWith` goroutines before returning, so a late `session.Save()` could race with `t.TempDir()` cleanup and report `directory not empty`. Added `turnWg sync.WaitGroup` to `Engine` tracking all turn goroutines; `Engine.Stop()` now waits on it (10 s bounded) before returning.
- **Fix pre-existing `go vet` warning.** `permRecordingSession` value-embedded `controllableAgentSession`, copying its `sync.Mutex` (`literal copies lock value`). Changed to pointer embedding (`*controllableAgentSession`).

### Tests
- 3 new unit tests in `core/engine_test.go` for `/model` live switch: `TestCmdModel_AppliesLiveModelWithoutReset` (live switch succeeds, subprocess kept alive, session ID & history preserved, reply mentions immediate switch), `TestCmdModel_FallsBackToRespawnWhenLiveFails` (SetLiveModel errors → falls back to cleanupInteractiveState, session ID preserved for resume, reply omits live hint), `TestCmdModel_FallsBackToRespawnWhenNoLiveSession` (no running interactive state → legacy respawn path, non-ACP behavior). Existing `TestCmdModel_KeepHistoryPreservesSessionID` still passes (verifies no regression in the respawn path).
- 1 new unit test in `agent/acp/feature_test.go`: `TestACPSession_implementsLiveModelSwitcher` (interface compliance assertion for `*acpSession` → `core.LiveModelSwitcher`).
- 8 new unit tests in `core/engine_proc_cleanup_test.go` for subprocess cleanup: `TestGetOrCreateState_DeadSessionCleanedUp` (dead session defensively closed when a new message arrives), `TestGetOrCreateState_DeadSessionWithNilAgentSession` (nil agentSession handled without panic), `TestEventError_DeadSessionTriggersCleanup` (EventError + `!Alive()` calls `cleanupInteractiveState`), `TestEventError_AliveSessionDoesNotCleanup` (per-turn errors on live sessions preserve the session), `TestEngineStop_ParallelCloseWithTimeout` (parallel close is faster than serial), `TestEngineStop_StuckSessionDoesNotBlockForever` (stuck session doesn't block quick sessions), `TestACPStyle_CloseEventsChannel` (Close closes events channel), `TestCloseAgentSessionWithTimeout_AbandonsStuckSession` (timeout protection works).
- 6 new unit tests in `agent/acp/session_cleanup_test.go` for ACP Close: `TestClose_ClosesEventsChannel` (events channel closed after Close), `TestClose_Idempotent` (multiple Close calls don't panic via `sync.Once`), `TestCloseEvents_Once` (concurrent closeEvents is safe), `TestClose_DeadSessionClosesEvents` (already-dead session still closes channel), `TestClose_AliveStateAfterClose` (alive flag cleared), `TestPrepareCmdForKill_SetsPgid` (helper smoke test).
- Full suite: 36 packages pass, 3 consecutive runs, 0 failures. (`agent/codex` has one pre-existing flaky test `TestGetModelAndReasoningEffort_FromRuntimeConfigWhenUnset` that fails intermittently due to shell-script mock timing — passes on repeat runs, unrelated to these changes.)

## v1.4.6 (2026-07-07)

Personal fork `/cancel` fix for `@qinghuangniao/cc-connect-qhn`. Fixes the bug where output kept streaming to the user after `/cancel` was issued.

### Notes
- Fix `/cancel` continuing to relay output after cancellation. Previously `cmdCancel` only sent `agentSession.CancelTurn()` (a fire-and-forget `session/cancel` notification to the agent backend) without touching any engine-local state, so the foreground event loop in `processInteractiveEvents` kept reading and relaying already-buffered chunks from the events channel (cap 128), plus any chunks the ACP server emitted between receiving the cancel and actually stopping. The loop's `select` only recognized `stopCh` (`/stop`) and `e.ctx.Done()` — there was no cancel path. A second leak: even if the loop returned, without `eventsNeedResync=true` the unsolicited reader would pick up the leftover chunks and relay them as "background" events.
- Add a per-turn `cancelCh` to `interactiveState`, created fresh at the start of each `processInteractiveEvents` call and cleared on exit (scoped to the running turn, unlike `stopCh` which tears down the whole interactive state). `cmdCancel` now closes `cancelCh` (authoritative local stop) in addition to `CancelTurn()` (best-effort server-side stop), and the event loop checks `cancelCh` with priority so cancellation is deterministic rather than racing buffered chunks. `handleCancel` finalizes the progress card, discards the streaming preview, and sets `eventsNeedResync=true` so leftover events are drained before the next turn and are not picked up by the unsolicited reader.
- `cmdCancel` now uses `cancelCh` presence (not just `agentSession != nil`) to decide whether a turn is actually in progress, and claims the channel under `state.mu` (setting it to nil) so a racing second `/cancel` sees "no turn in progress" instead of double-closing. The session stays alive after `/cancel` — the next message reuses the same `agentSession`. Bump `clientInfo.version` to `1.4.6`.

### Tests
- 2 new unit tests in `core/engine_test.go`: `TestCmdCancel_StopsTurnAndKeepsSessionAlive` (loop exits promptly, `CancelTurn` called once, `MsgTurnCancelled` sent, post-cancel events are NOT relayed, session stays alive, `eventsNeedResync` set) and `TestCmdCancel_NoTurnInProgress` (replies `MsgNoTurnInProgress` and does not call `CancelTurn` when idle; no double-close panic).
- Full suite: `core` pass (short + `-race`), cancel tests pass under `-race -count=50`.

## v1.4.5 (2026-07-01)

Personal fork streaming dedup fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix `mergeStreamDisplayContent` duplicate output bug in `displayModeStream` mode. When `finalResponse` includes metadata (e.g. `*model · usage · path*`) appended after the answer text, the old comparison (`TrimSpace(streamContent) == finalResponse`) always failed, causing `streamContent + "\n\n" + finalResponse` which duplicated the answer. Added prefix-based dedup: if `finalResponse` starts with `streamContent` (the stream was just a prefix), return `finalResponse` as-is; if `streamContent` starts with `finalResponse`, return `streamContent` as-is. Bump `clientInfo.version` to `1.4.5`.

### Tests
- 11 new unit tests in `core/engine_test.go` covering: stream-is-prefix-of-final, final-is-prefix-of-stream, exact match, empty inputs, no-match concatenation, last-assistant dedup, trailing newlines/spaces, and the real-world bug scenario from production logs.

## v1.4.4 (2026-07-01)

Personal fork ACP protocol alignment for `@qinghuangniao/cc-connect-qhn`. Brings the generic ACP agent adapter in line with the latest [Agent Client Protocol](https://agentclientprotocol.com/) spec so commands like `/list` and `/model` work against ACP servers (e.g. CodeBuddy) that previously returned "未找到此项目的会话" / "当前 Agent 不支持模型切换".

### Notes
- Implement local session tracking as a fallback for `/list` when the ACP server does not advertise `sessionCapabilities.list`. Sessions started by cc-connect are recorded locally with their cwd and an auto-extracted title (first `agent_message_chunk`), and surfaced via `ListSessions` whenever the server-side probe is unsupported or fails. This makes `/list` and `/switch` usable against CodeBuddy without any server-side changes.
- Add engine-level fallback in `cmdList`: when `agent.ListSessions()` returns empty, build the list from cc-connect's own `SessionManager.AllSessions()` so previously started sessions remain visible even if the agent backend doesn't track them. This fixes the case where `/list` returns "未找到此项目的会话" on first run against an ACP server without `session/list` support.
- Implement `core.ModelSwitcher` on the ACP agent. Model lists are parsed from both the new `configOptions` (category `model`, the v2 ACP way) and the legacy `models` field returned by `session/new` / `session/load`. `SetModel` is applied on the next `StartSession` via `session/set_model`, with a fallback to `session/set_config_option` (configId `model`) when the server returns method-not-found. This unblocks `/model` and `/model switch <id>` on CodeBuddy.
- Fix legacy `models` field parsing: CodeBuddy returns `modelId` (not `id`) as the identifier in `availableModels` entries. Introduced dedicated `acpModelEntry` type with `json:"modelId"` tag so model IDs are correctly extracted.
- Add user-facing hint when `/model` is invoked before any session has started (model list is empty because it's only populated after the first `session/new` handshake). New i18n key `MsgModelListEmptyHint` in 5 languages.
- Handle `config_option_update` notifications so the cached model list stays in sync when the server changes the active model (e.g. after a rate-limit fallback).
- Handle `session_info_update` notifications so the locally tracked session title stays in sync with server-reported metadata.
- Handle `usage_update` notifications and implement `core.ContextUsageReporter` on `acpSession` so the `/usage` command can surface server-reported token usage for ACP sessions.
- Add `title` field to `clientInfo` in `initialize` requests (both handshake and probe paths) per the ACP spec recommendation, and bump the reported version to `1.4.4`.

### Tests
- 30 new unit tests in `agent/acp/feature_test.go` covering: local session tracking & ListSessions fallback, ModelSwitcher interface & GetModel precedence, parseModels from configOptions / legacy models / mixed inputs, session/set_model RPC success & fallback to session/set_config_option, config_option_update / session_info_update / usage_update notification handling, auto-extracted title truncation & no-overwrite guard, interface compliance assertions for `ModelSwitcher` / `ContextUsageReporter` / `sessionCallbacks`.
- 4 new unit tests in `core/session_test.go` covering `sessionsFromSessionManager` fallback (returns tracked sessions, filters by agent name, ignores empty/sentinel session IDs, empty manager).
- Full suite: `agent/acp` 71 pass, `core` 740 pass (short mode), 0 failures.

### Docs
- `.codebuddy/plans/acp-command-support-plan.md` — implementation plan with gap analysis against the latest ACP protocol spec, 4 phases, and file-level change matrix.

## v1.4.0 (2026-07-01)

Personal fork WeCom streaming display alignment with openclaw progress-draft design for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Introduce three-region `wecomStreamAssembler` (`visibleText` / `progressLines` / `heldTool`) as the single source of truth for WeCom WebSocket stream preview state, replacing the old two-field `wsStreamAssembler` that mixed tool blocks into visible text.
- Add `ProgressAssembler` optional platform interface (`core/interfaces.go`) so the engine routes `EventToolUse`/`EventToolResult` to a UI side-channel in `tool_hold` mode instead of appending tool messages to the `textParts` buffer. This physically separates tool progress from the model-produced answer text, aligning with openclaw's progress-draft philosophy.
- Implement `WSPlatform.OnToolStart`/`OnToolComplete` (`platform/wecom/websocket_progress_assembler.go`) to route tool progress into `progressLines`, then merge with `visibleText` via `UpdateMessage` so the preview shows both together instead of alternating flicker.
- Wire `WSPlatform.UpdateMessage` to track received text in `assembler.visibleText` and send the merged render (`progressLines + separator + visibleText`), so tool progress and answer text are always displayed together.
- Wire `WSPlatform.FinalizePreviewMessage` to call `assembler.finish(finalText)`, which clears `progressLines` so the final frame contains only the answer (no residual tool progress).
- Remove `streamToolHoldNeedsAnswerSeparator` hack from `core/engine_turn.go` (4 call sites) since the assembler now owns separation via `render()`.
- Remove `shouldHoldOnlyTool`/`holdTool` guessing logic from `platform/wecom/websocket_stream_queue.go` since tool holding is now driven explicitly by the engine via `ProgressAssembler` events.
- Add `SessionIDRotator` optional interface (`core/interfaces.go`) and implement it on `acpSession` so ACP backends that rotate session IDs on spawn refresh the persisted binding unconditionally, while non-ACP sessions keep `CompareAndSetAgentSessionID` semantics (only set when empty or sentinel). Fixes `TestSessionIDWriteback_DoesNotOverwriteExisting` regression introduced by commit `6e70f87`.

### Tests
- 16 new `wecomStreamAssembler` unit tests covering invariants I1-I6 (visibleText isolation, progressLines FIFO bounding, render idempotency, finish clears progress, heldTool overwrite) and paths G1-G6 (appendText/onToolStart/onToolComplete region isolation, snapshot read-only, formatToolLine explain/raw modes, truncateMiddle).
- 2 updated engine integration tests (`StreamModeToolHoldRoutesToolProgressToAssembler`, `StreamModeMultiToolRoutesAllToAssembler`) expressing the new contract: tool progress must NOT appear in any preview message, must be routed via `ProgressAssembler`.
- 5 new WeCom integration tests (`UpdateMessage_MergesProgressAndVisibleText`, `OnToolStart_DoesNotSendStandaloneFrameWhenNoVisibleText`, `FinalizePreviewMessage_ClearsProgressAndSendsOnlyAnswer`, `UpdateMessage_TracksVisibleTextInAssembler`, `FullFlow_TextToolTextFinalize`) verifying the end-to-end打通: visible text and tool progress are merged before sending, finalize clears progress.
- Full suite: `platform/wecom` 89 pass, `core` 736 pass, `agent/acp` 34 pass (short mode), 0 failures.

### Docs
- `docs/upgradefeature/wecom-stream-align-openclaw-design.md` — technical design with 18-module alignment matrix, 4-scenario before/after comparison, 5-phase rollout plan.
- `docs/upgradefeature/wecom-stream-tdd-gap-analysis.md` — TDD gap analysis with 5 reverse-failing tests, 12 uncovered paths, 22 recommended new tests.

## v1.3.12 (2026-06-05)

Personal fork codebase modularization for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Split the monolithic `core/engine.go` (~13,800 lines) into 14 focused files (`engine_admin_cmds.go`, `engine_alias_cmds.go`, `engine_bind_cmds.go`, `engine_card_actions.go`, `engine_cards.go`, `engine_cron.go`, `engine_info_cmds.go`, `engine_model_cmds.go`, `engine_provider_cmds.go`, `engine_relay.go`, `engine_reply.go`, `engine_session_cmds.go`, `engine_shell_cmds.go`, `engine_turn.go`) for improved maintainability.
- Extract six sub-systems out of `core/` into dedicated top-level packages: `relay`, `webhook`, `bridge`, `api`, `management`, `proxy`, reducing core coupling and enabling independent testing.
- Introduce `core.RelayManagerAPI` interface so `core/` references relay via interface rather than a concrete struct, breaking the circular import.
- Refactor WeCom WebSocket stream handling into three focused files: `websocket_stream_assembler.go`, `websocket_stream_queue.go`, `websocket_stream_reply.go`.
- Add public Engine accessor methods (`Commands()`, `HandleRelayRequest()`, `SendMessage()`, `ProcessInteractiveMessage()`) to expose internal functionality for the extracted packages without breaking encapsulation.

## v1.3.11 (2026-05-28)

Personal fork WeCom stream dedup regression fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom WebSocket stream aggregation so a finalized answer that extends an already streamed partial line no longer repeats the partial prefix in the closing frame.
- Cover the repeated root-directory markdown reply case from `auto-bugfix/latest/cc-connect.log` with a dedicated regression fixture.
- Keep the earlier long-finalize splitting protection and verify the full `platform/wecom` test suite still passes after the dedup adjustment.

## v1.3.10 (2026-05-28)

Personal fork WeCom long-finalize delivery fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom WebSocket preview finalize path so terminal content over 2048 bytes is split into one closing stream frame plus ordered follow-up markdown messages instead of being forced into a single oversized frame.
- Keep the existing preview `stream_id` when finalizing long replies so the in-place closing frame still lands on the original preview message.
- Add regression coverage for the online long-finalize case and for prefix/tool aggregation edge cases derived from the captured logs.

## v1.3.9 (2026-05-27)

Personal fork WeCom stream regression hardening for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom preview completion so the final answer closes the existing stream message in place instead of creating an additional near-duplicate message.
- Fix WeCom stream aggregation so partial text from the last acked frame is only reused when the next payload is truly incremental, avoiding repeated prefixes in long updates.
- Add log-derived regression fixtures under `platform/wecom/testdata/stream_regressions.json` so the exact online payload patterns from `a.log` remain covered by stable tests.

## v1.3.8 (2026-05-27)

Personal fork WeCom stream visibility and audit fixes for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom `stream` mode with `tool_messages = false` so hidden tool events no longer leak into the final visible stream payload.
- Keep WeCom long-message splitting on the final answer path when tool updates are hidden, avoiding the single-preview-message fallback.
- Continue recording inbound WeCom access attempts and unauthorized users for allow-list troubleshooting.

## v1.3.7 (2026-05-27)

Personal fork WeCom delivery and audit fixes for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Add WeCom access audit logging so inbound users and prompt send time are recorded to local JSONL files, including `allow_from` rejections.
- Fix WeCom WebSocket final reply delivery for long messages by splitting the terminal response into ordered follow-up messages instead of truncating the tail.
- Align WeCom message splitting with the documented 2048-byte limit and keep preview updates within that bound.

## v1.3.4 (2026-05-25)

Personal fork release for `@qinghuangniao/cc-connect-qhn`.

### Notes
- This is an unofficial personal fork intended for personal practice, experimentation, and self-use.
- Fork base: upstream `cc-connect` release `v1.3.3-beta.2`.
- Local fork import reference in this repository: commit `c099ce699e44d74a9f2018244375a4ff410cd7eb`.

## v1.3.6 (2026-05-27)

Personal fork WeCom stream/tool display fixes for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix ACP tool result mapping so completed tool updates prefer `rawOutput` over streamed assembly fragments.
- Fix WeCom tool-hold stream aggregation so stale partial text does not collapse the final payload into a lone backtick.
- Fix WeCom final rendering to insert a blank line between tool result blocks and the following natural-language answer, avoiding markdown/render breakage in WeCom.

## v1.3.5 (2026-05-26)

Personal fork packaging fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix npm installer release asset lookup so global install downloads from the fork release repository.
- Intended to resolve `npm install -g @qinghuangniao/cc-connect-qhn` failing with GitHub release asset `404`.

## v1.3.3-beta.2 (2026-05-09)

Beta release with Slack Assistant API, DingTalk improvements, MAX platform webhook mode, and numerous platform fixes. No breaking changes.

### New Features
- **Slack Assistant API**: support Slack Assistant API (Agent toggle) with natural on/off switching (#844)
- **DingTalk richText**: support richText message type for DingTalk platform (#828)
- **DingTalk image handling**: add DingTalk image message support (#828)
- **MAX webhook delivery mode**: add webhook delivery mode for MAX messenger platform with deployment docs (#818)
- **Claude Code env vars**: support project-level environment variables via `env` config section (#812)
- **display_mode enum**: add `display_mode` enum to replace boolean `quiet` config, with quiet/compact/normal/full options (#655)
- **Core reset_on_idle_mins default**: default to 30 minutes to prevent context drift (#494)
- **Claude Code custom system prompt**: add support for custom system prompt configuration via `system_prompt` option (#534)

### Fixed
- **Bridge security**: require token when Bridge is enabled to prevent unauthorized access (#408)
- **Feishu recalled messages**: handle recalled messages gracefully (#841)
- **Feishu media download failure**: notify user when media download fails instead of silent drop (#815)
- **WeChat video messages**: send video files as proper video messages in WeChat (#813)
- **WeChat incomplete delivery**: notify user on incomplete message delivery and enhance retry logging (#771)
- **Telegram private topics**: preserve private topic session keys (#804)
- **Kimi session UUID**: capture session UUID from stderr instead of stdout (#766)
- **Codex app_server config**: app_server backend should honor model/effort/provider config + add stdio sentinel (#837)
- **Codex progress rendering**: render progress in rich Card 2.0 format (#838)
- **Core ellipsis events**: suppress ellipsis-only events and handle context indicator in footer
- **Core Markdown table**: render inline formatting inside GFM table cells (#675)
- **Feishu user id resolution**: guard user id resolution against edge cases
- **Feishu thread topics**: skip quote injection in thread-isolated topics (#767)
- **Config display mode**: honor project display mode setting
- **Daemon restart**: add --force flag to daemon restart command (#736)
- **AskUserQuestion**: use question text as answers key for proper answer routing (#822)

## v1.3.3-beta.1 (2026-04-25)

Beta release with new agents, new features, and broad platform fixes. No breaking changes.

### New Features
- **Devin agent**: add Devin CLI as a first-class agent with full `/list`, `/mode`, and session management (#672)
- **`/ps` command** (replaces `/btw`): send a message to a busy session mid-turn; `/btw` kept as alias for backward compatibility (#620)
- **`!` shell shortcut**: use `!ls -la` as shorthand for `/shell ls -la`, with optional `--timeout` parameter (#658)
- **NO_REPLY suppression**: agents can return `NO_REPLY` to silently skip platform delivery, useful for cron/analysis tasks (#682)
- **Feishu shared WebSocket**: multiple projects sharing the same `app_id` now share one WebSocket connection with per-project `allow_chat` / `group_only` filtering (#613)
- **Message queue depth configurable**: new `[queue] max_depth` config option (default 5) (#690)
- **Claude Code opus[1m]**: add 1M-context Opus model option with shorthand descriptions (#660)
- **QQ Bot file send/receive**: full file attachment support with robustness checks (#685)
- **Bridge ImageSender/FileSender**: `cc-connect send --image/--file` now works through bridge protocol (#712)
- **Provider presets**: add NekoCode, VisionCoder, and AIHubMix to provider presets; add Trae CLI ACP and COCO ACP config examples (#739)

### Fixed
- **OpenCode image handling**: inbound images from WeChat/WeCom are now correctly passed to OpenCode CLI via `--file` flags (#717)
- **Slack Markdown**: convert standard Markdown to Slack mrkdwn format (bold, italic, strike, links, headings) (#680)
- **QQ Bot reconnect**: cancel stale goroutines on WebSocket reconnect to prevent race conditions (#678)
- **Gemini multiline prompt**: pass prompt via stdin to preserve newlines (#695)
- **Telegram HTML fallback**: upgrade silent HTML parse failures to Warn-level logs (#674)
- **Telegram /skills**: show Telegram-safe skill command format (#571)
- **Feishu webhook mode**: skip bot open_id fetch in webhook mode for private deployments (#696)
- **Reply footer**: suppress footer when only workdir is known (#701)
- **Web UI add-platform**: fix "project not found" error when adding a new platform to an uncreated project

### Contributors
Thanks to all contributors who made this release possible:
- @YoungShook — Devin agent integration, Telegram HTML fallback
- @Cigarrr — /ps command, NO_REPLY feature
- @vinnyxiong — Feishu shared WebSocket and allow_chat
- @happyTonakai — Shell `!` prefix and `--timeout`
- @AaronZ345 — Claude Code opus[1m] model
- @ferocknew — QQ Bot file support
- @soaringk — OpenCode image fix
- @Zx55 — Telegram /skills fix
- @zhaomoran — Feishu webhook mode fix
- @LyInfi — Reply footer suppression
- @meloalright — Trae/COCO ACP config examples

## v1.3.2 (2026-04-21)

Hotfix release: session filtering is now configurable and defaults to showing all sessions.

### Fixed
- **`/list` shows all sessions by default**: the session filter introduced in v1.3.0 (which hid sessions not created by cc-connect) was accidentally merged and caused confusion. The filter is now **off by default** — `/list`, `/switch`, and `/delete` show all agent sessions regardless of origin.

### Added
- **`filter_external_sessions` config option**: users who *do* want to hide externally-created sessions can set `filter_external_sessions = true` in `[[projects]]` to restore the old filtering behavior.
- **Comprehensive integration tests**: real-agent E2E tests for both Codex and Claude Code covering the full `/list` → `/new` → conversation → `/list` lifecycle with provider-based authentication (no env-var API keys required). Plus 9 adapter-level filter tests using real Codex/Claude Code session file fixtures.

## v1.3.1 (2026-04-20)

Patch release with critical bug fixes for session management, config preservation, and Weibo media support.

### Fixed
- **Session visibility (`/list`)**: historical Codex sessions disappeared after upgrade due to `AgentSessionID` being cleared on `/new` or provider switch without preservation. Added `PastAgentSessionIDs` tracking with legacy data migration so existing sessions remain visible.
- **Session naming (`/new xxx`)**: custom session names from `/new` were not mapped to the agent session ID for agents where the ID is established asynchronously (Codex, Qoder, Kimi, etc.). Added name mapping to all `EventResult` and `EventText` handlers across interactive, relay, and drain paths.
- **Config comment preservation**: `/provider switch`, `/model`, `/lang`, display settings, and TTS changes now use surgical text-level editing instead of full TOML re-serialization, preserving all comments, unknown fields, and formatting.
- **Codex `codex_home` path**: session listing, history, and deletion now consistently use the configured `codex_home` instead of hardcoded `~/.codex`.
- **Feishu card callback hint**: log a reminder when interactive card mode is enabled but `card.action.trigger` may not be subscribed.

### Added
- **Weibo image & file support**: send and receive images and files in Weibo DMs via base64 encoding within the WebSocket `send_message` payload. Implements `ImageSender` and `FileSender` interfaces.
- **Comprehensive session tests**: 12 new `SessionManager` unit tests covering `PastAgentSessionIDs`, legacy data migration, and version-based schema detection. 9 new `Engine` integration tests covering `/list` visibility across `/new`, provider switch, and real-world legacy data scenarios, plus end-to-end session name mapping tests for all three agent ID patterns (immediate, EventText, EventResult).
- **Config preservation tests**: 8 new tests verifying comment and field preservation for `SaveActiveProvider`, `SaveAgentModel`, `SaveProviderModel`, `SaveLanguage`, `SaveDisplayConfig`, `SaveTTSMode`, multi-project config, and global provider refs.

## v1.3.0 (2026-04-19)

First stable release of the 1.3 series. 555 commits since v1.2.1 with major new features, platform improvements, and broad community contributions.

### Highlights

- **Web Admin UI** — Full management dashboard embedded in the binary via `go:embed`. Project CRUD, session monitoring, cron editor, provider management, chat interface, and i18n (en/zh/zh-TW/ja/es). Use `cc-connect web` to open directly in the browser with auto-login.
- **Lifecycle Event Hooks** — New `[[hooks]]` config to trigger shell commands or HTTP webhooks on 7 event types: `message.received`, `message.sent`, `session.started`, `session.ended`, `cron.triggered`, `permission.requested`, `error`. Async by default, fail-open, non-blocking.
- **Skill Management** — New `/skills` page in the web UI with local skill browser (per-project, per-agent) and recommended skill presets fetched from remote.
- **Global Provider Management** — Add, edit, delete providers in the web UI; import from cc-switch config; per-agent-type provider presets with featured/star badges.

### New Features
- `cc-connect web` CLI command: auto-configure web admin, open browser with token-based login
- Feishu: auto-resolve `@name` mentions to clickable at-tags (`resolve_mentions` config)
- Feishu: multi-level reply chain recognition; done-emoji reaction after streaming
- Feishu: configurable progress display styles (compact/card)
- Claude Code: support CLI wrappers via `cli_path`; `/effort` command for reasoning effort; `auto` permission mode; `disallowed_tools` config
- Codex: runtime reply footer; preserve workspace app-server options
- Kimi CLI: new agent support
- Pi: new agent support
- Discord: preserve table formatting; proxy support; `@everyone`/`@here` broadcast
- Telegram: forum topic support; markdown table monospace rendering; command menu adaptation
- WeCom: configurable `api_base_url` for private deployments; file receiving via HTTP callback
- Weixin (ilink): personal chat platform with CDN media, QR setup, image/file/audio send
- Config: support `${ENV_VAR}` placeholders in TOML values
- Core: `/workspace init` with local directory paths; `/dir` directory history; `agent-sid` command; auto-compress context on token threshold; outgoing rate limiting
- Daemon: preserve proxy env in systemd service

### Bug Fixes
- Fix Windows cross-compilation (duplicate runas stub file)
- Fix web footer double 'v' prefix in version display
- Fix web modal overlay not covering full viewport (portal rendering)
- Fix provider preset cards: action buttons pinned to card bottom
- Fix web page content overlapping footer (global layout restructure)
- Fix Gemini image handling: save to workspace, prompt-based file references
- Fix Claude Code: unblock readLoop when child subprocesses hold stdout pipe
- Fix Codex: multiline prompt on resume; force-kill process group on stop
- Fix core: race condition during session cleanup; follow symlinked skill directories; persist agent_session_id; filter `/list` to cc-connect owned sessions
- Fix Feishu: slash commands in thread/reply context; user/chat name resolution in async goroutine
- Fix Telegram: UTF-8-safe command menu descriptions
- Fix TTS: don't send empty language_type to Qwen TTS API
- Fix config: `formatTOML` no longer strips user-set zero values
- Security: mask bridge token in `/api/v1/status`; path traversal protection for static files

### Contributors

Thanks to all contributors who made this release possible:

- [@leoliang1997](https://github.com/leoliang1997) — Feishu card rendering, auto-resolve @mentions
- [@xukp20](https://github.com/xukp20) — Provider env handling, skill discovery, Codex options
- [@boyu-zhu](https://github.com/boyu-zhu) — Telegram markdown table rendering
- [@RukawaKaede](https://github.com/RukawaKaede) — Claude Code CLI wrapper support
- [@meishaoqing](https://github.com/meishaoqing) — Feishu multi-level reply chain
- [@Zx55](https://github.com/Zx55) — Telegram command menu, symlinked skill dirs
- [@leighstillard](https://github.com/leighstillard) — Claude Code `/effort` command
- [@ht290](https://github.com/ht290) — inject_sender display name
- [@Sentixxx](https://github.com/Sentixxx) — Claude Code readLoop subprocess fix
- [@bugwz](https://github.com/bugwz) — WeCom private deployment API base URL
- [@cold2600438-lgtm](https://github.com/cold2600438-lgtm) — Kimi CLI agent
- [@MeteorSkyOne](https://github.com/MeteorSkyOne) — Discord table formatting
- [@happyTonakai](https://github.com/happyTonakai) — Feishu done-emoji reaction
- [@xxb](https://github.com/xxb) — Codex reply footer, Discord session routing
- [@q107580018](https://github.com/q107580018) — Feishu delete/model card flows
- [@Cigarrr](https://github.com/Cigarrr) — Workspace binding parsing
- [@g1f9](https://github.com/g1f9) — Local directory workspace init
- [@0xsegfaulted](https://github.com/0xsegfaulted) — agent-sid command
- [@yzlu0917](https://github.com/yzlu0917) — Env var config placeholders
- [@sidney061212-ai](https://github.com/sidney061212-ai) — Agent session ID persistence
- [@zkunzhu](https://github.com/zkunzhu) — Daemon proxy env preservation
- [@Yuri0314](https://github.com/Yuri0314) — TTS language type fix

## v1.2.2-beta.5 (2026-03-31)

Beta release with embedded web admin, Discord proxy support, multimodal fixes, and major platform improvements.

### New Features
- **Embedded Web Admin**: Web frontend is now compiled into the binary via `go:embed` — no separate `npm install` needed. Use `/web setup` to configure, or build with `no_web` tag to exclude. Binary size increases ~1MB (#356)
- **Web Admin Dashboard**: Full-featured management UI with project CRUD, session management, cron job editor, global settings, chat interface with bridge WebSocket, slash commands, and i18n (en/zh/zh-TW/ja/es) (#316)
- **Discord Proxy Support**: Discord platform now supports `proxy`, `proxy_username`, `proxy_password` options for HTTP API and WebSocket Gateway connections
- **Feishu Progress Styles**: Configurable progress display styles (compact/card) to reduce message spam
- **Claude Code Auto-Permission Mode**: New `auto` permission mode for Claude Code agent (#329)
- **WeCom File Receiving**: WeCom HTTP callback now supports receiving files and forwarding them to the agent (#330)
- **Outgoing Rate Limiting**: Per-platform outgoing message rate limiting
- **Telegram Forum Topics**: Migrated to `go-telegram/bot` library with forum topic support (#321)
- **Global Settings UI**: Expose global configurations (language, quiet, display, stream preview, rate limit, log) in the web admin

### Bug Fixes
- **Gemini Image Handling**: Save attachments to workspace directory instead of `/tmp` so Gemini CLI tools can access them; use prompt-based file references instead of unsupported `--image` flag
- **Security**: Mask bridge token in `/api/v1/status` endpoint; add path traversal protection for static file serving
- **Codex**: Fix multiline prompt preservation on resume (#341); force kill session process group on stop (#340)
- **Session Recycling**: Wait for old session to close before creating new one (#352)
- **Discord**: Harden session routing and remove implicit continue bridge (#322); execute slash commands when defer fails (#300)
- **Slack**: Pass file uploads to agent (#296)
- **Telegram**: UTF-8-safe command menu descriptions (#301)
- **WeCom**: Strip @bot mentions from inbound text (#303)
- **Daemon**: macOS launchd do not respawn on clean exit (#304)
- **Core**: Route workspace model changes through session context (#339); outgoing rate limit refinements and i18n tightening
- **Config**: `formatTOML` no longer strips user-set zero values (e.g. `quiet = false`)

### Improvements
- **CI**: Add Node.js setup for web frontend build in CI pipeline; use `no_web` tag for e2e/smoke tests
- **Tests**: Expanded coverage across agents, config, and core packages
- **Selective Compilation**: Added `no_web` build tag to exclude web assets from binary

### Contributors

Special thanks to all contributors who made this release possible:

- **cg33** — Embedded web admin, Discord proxy, Gemini fix, security hardening
- **xxb** — Discord session routing fix, codex process kill, workspace reconnect (#322, #340, #315)
- **dev-null-sec** — Codex multiline prompt fix (#341)
- **xukp20** — Workspace model routing (#339)
- **zhengbuqian** — Telegram go-telegram/bot migration and forum topics (#321)
- **huangdijia** — Claude Code auto permission mode (#329)
- **buddhism5080** — Discord file sending (#307)

## v1.2.2-beta.4 (2026-03-22)

Beta release with Weixin (ilink) personal chat support, session/continue improvements, and platform fixes.

### New Features
- **Weixin Personal (ilink)**: New platform with long-poll `getUpdates` / `sendMessage`, QR `weixin setup`, CDN decrypt for inbound media and `ImageSender`/`FileSender` outbound (#257)
- **Telegram**: Voice/audio reply support (#225) and async startup recovery
- **Discord**: `@everyone` / `@here` broadcast support (#132)
- **Cron**: Optional new session per run and per-job timeout (#236)
- **Claude Code**: `disallowed_tools` configuration option (#232)
- **Auto-Compress**: Compress context when estimated tokens exceed threshold (#231)
- **Continue / Sessions**: Fork session on `--continue` to avoid context contamination (#244); replace persisted `ContinueSession` sentinel with real agent session id; reserve CLI `--continue` bridge for real user traffic
- **Core**: `/dir` directory history; `/model` switching aligned with provider flow (#246)
- **Providers**: MiniMax M2.7 high-speed model added to example configs (#217)

### Bug Fixes
- **Weixin**: Harden send path (empty body skip, response body cap, dedup keys, multi-voice segments); treat `sendMessage` JSON `ret != 0` as failure so quota/API errors surface correctly
- **Feishu**: Always reply to the original message; dispatch message handling asynchronously (#57)
- **Codex**: Mode switch and `--json` flag position fixes (#240, #239)
- **Multi-Workspace**: Workspace command prefix missing leading slash (#135)
- **Non-Claude Agents**: Ignore `ContinueSession` sentinel where inappropriate (#244 follow-up)
- **npm / Update**: Version sync after update; pre-release version comparison normalization

### Improvements
- **Tests**: Expanded coverage across `config`, `core`, agents, and platforms
- **Logging / Errors**: Additional error logging in several code paths

### Contributors

Special thanks to all contributors who made this release possible:

- **cg33** — Weixin ilink platform, setup CLI, and CDN media (#257)
- **Shawn** — Feishu async dispatch and reply-to-original fixes (#57)
- **quabug** — Discord broadcast and non-Claude ContinueSession handling (#132, #244)
- **huluma1314** — Auto-compress when token threshold exceeded (#231)
- **Leigh Stillard** — Fork session on `--continue` (#244)
- **Deeka Wong** — Telegram audio replies and core `/model` provider flow (#225, #246)
- **q107580018** — Telegram async startup recovery
- **just4zeroq** — Codex mode and JSON flag fixes (#240)
- **术士木星** — Cron session-per-run and job timeout (#236)
- **hushicai** — Claude `disallowed_tools` (#232)
- **Octopus** — MiniMax M2.7 high-speed in examples (#217)
- **alinnb** — `/dir` directory history
- **Claude** — Continue-session bridge fixes, auto-compress/cron edge cases, Weixin send hardening and API error handling, and broad test improvements

## v1.2.2-beta.3 (2026-03-19)

Beta release with major multi-user mode, improved workspace stability, and platform enhancements.

### New Features
- **Multi-User Mode**: Per-user rate limits, role-based ACL (allow_from/admin_from), and audit logging
- **ImageSender**: Unified image sending support for 6 platforms (Feishu, Telegram, Discord, Slack, DingTalk, QQ)
- **MiniMax M2.7**: Upgraded default model from M2.5 to M2.7 for improved reasoning
- **/whoami Command**: Display user ID for allow_from/admin_from configuration
- **/btw Command**: Inject messages into busy sessions without interrupting
- **/dir Command**: Dynamic runtime work directory switching
- **Cron Muting**: Mute/unmute cron jobs with platform wrapper and UI integration
- **Interrupt Support**: Send interrupt signal to agent sessions (Ctrl+C equivalent)
- **CORS Support**: Cross-origin requests enabled for Bridge API
- **Message Queuing**: Queue messages when agent is busy instead of discarding
- **QQ Bot Markdown**: Full Markdown message support for QQ Bot

### Bug Fixes
- **Workspace Session Persistence**: Sessions now persist to disk in multi-workspace mode
- **Race Conditions**: Multiple data race fixes (adminFrom, degraded field, userRolesMu)
- **Memory Leaks**: Fixed pendingAcks leak on WeCom WebSocket disconnect, goroutine leaks
- **i18n**: Complete translation coverage for error messages
- **Relay Timeout**: Return partial text after timeout instead of error
- **QQ Bot Reconnect**: Handle nil wsConn on failed reconnect

### Improvements
- **Message Queue**: Extracted message queue handling into dedicated method
- **Cron UX**: Improved human-readable cron expressions
- **Slack**: Typing indicator, file download error handling, auth diagnostics
- **Provider Config**: `models` list for per-provider model selection via alias
- **Build**: Test infrastructure with P0/P1分层测试targets

### Contributors

Special thanks to all contributors who made this release possible:

- **sean2077** - Multi-user mode, ACL, and audit logging
- **0xsegfaulted** - Multi-workspace fixes and interrupt support
- **octo-patch** - MiniMax M2.7 upgrade
- **windli2018** - Bridge CORS support
- **jenvan** - CORS fixes

## v1.2.2-beta.2 (2026-03-16)

Beta release with significant improvements to agent stability, platform onboarding, and user experience.

### New Features
- **Feishu/Lark CLI Onboarding**: New `cc-connect feishu setup` command with QR code terminal display for quick bot configuration, supporting both new bot creation and existing bot binding
- **Pi Agent**: Added support for Pi coding agent with full session management and tool handling
- **Session TUI Browser**: New `cc-connect sessions` subcommand with terminal UI for browsing session history
- **Multi-Workspace Mode**: Channel-based workspace resolution with auto-binding by convention and interactive init flow
- **Design Documentation**: Added comprehensive design plans for multi-workspace and session resilience features
- **Slack Enhancements**: Typing indicator via emoji reactions, mrkdwn formatting guidance in system prompt
- **Session Resilience**: Automatic `--continue` on first connection, resume-failure fallback, and context usage indicators
- **Management API**: HTTP REST API endpoints for external management tools with WebSocket bridge support
- **Cron Setup Command**: `/cron setup` for easy cron job configuration with memory file integration

### Bug Fixes
- **RateLimiter Goroutine Leak**: Fixed cleanup goroutine not stopped on replacement and engine shutdown
- **DrainEvents Infinite Loop**: Fixed infinite loop when channel is closed in `drainEvents`
- **InteractiveKey Consistency**: Fixed `executeCardAction` using wrong key for `interactiveStates` lookup in multi-workspace mode
- **Workspace Command Prefix**: Fixed missing leading slash in workspace command prefix check
- **Agent Session Close**: Always close events channel on session timeout to prevent goroutine leaks
- **Pi Agent Mutex**: Move thinking field read inside mutex in `StartSession` to prevent race condition
- **Session AgentID Protection**: Protect `Session.AgentSessionID` writes with mutex to prevent data races
- **Session Routing Race**: Prevent session routing race when `/new` runs during active turn
- **Discord Duplicate Messages**: Deduplicate gateway `MessageCreate` events causing duplicate responses
- **Codex JSON Lines**: Handle large stdout JSON lines without scanner buffer overflow
- **UTF-8 Safety**: Use rune-based splitting in `splitMessage` to prevent invalid UTF-8 sequences

### Improvements
- **Gemini Display**: Enhanced tool display with diff syntax highlighting and improved Telegram markdown rendering
- **Thread Safety**: Added comprehensive thread-safe accessors for Session fields
- **Test Engine**: Thread safety improvements to test engine and fixed test assertions
- **Input Validation**: Consolidated interactive state cleanup and added input validation
- **i18n**: Updated rate limit messages to mention `/btw` command for adding context during processing

### Contributors

Special thanks to all contributors who made this release possible:

- **kevinWangSheng** - Multiple critical bug fixes (RateLimiter, drainEvents, UTF-8 safety, session routing)
- **q107580018** - Feishu CLI onboarding with QR code integration
- **sean2077** - Session TUI browser and sessions management
- **quabug** - Pi agent implementation and Discord fixes
- **AtticusZeller** - Gemini tool display and Telegram markdown enhancements
- **leighstillard** - Multi-workspace design, session resilience, and Slack improvements
- **Shawn** - Thread safety fixes and test improvements
- **zhuguanqi** - Session management and data race fixes
- **Steve-Rye** - JSON lines handling improvements
- **Xihui He** - iFlow and agent enhancements
- **Mr.QiuW** - Various platform improvements

## v1.2.2-beta.1 (2026-03-12)

Beta release with major new features and security improvements.

### New Features
- **`/usage` Command**: Add a built-in quota usage command with a generic agent usage-reporting interface; Codex now supports ChatGPT OAuth usage lookup via `~/.codex/auth.json`
- **Feishu Interactive Cards**: Beautiful card-based UI for slash commands (/help, /list, /status, etc.) with tabbed navigation and in-place updates
- **Lark Platform Support**: Added support for Lark (飞书国际版) with proper domain handling
- **Codex Reasoning Effort**: New `/reasoning` command to switch reasoning effort levels (low/medium/high)
- **Codex Model Cache Fallback**: `/model` command now falls back to local `~/.codex/models_cache.json` when API is unavailable
- **Gemini Timeout Config**: New `timeout_mins` option to configure per-turn timeout for Gemini agent
- **Batch Session Deletion**: `/delete` now supports comma lists, ranges, and mixed forms for batch deletion
- **TTS Support**: Text-to-speech with Qwen and OpenAI providers
- **Admin Privilege System**: Admin-only commands for privileged operations
- **iFlow Tool Timeout**: Configurable tool timeout and reset timer on partial completion
- **Card-based Permission Prompts**: Permission requests now use interactive cards with callback support
- **Shared Session Support**: Share sessions across all platforms with `share_session_in_channel` option

### Bug Fixes
- **Security Hardening**: Socket permissions tightened (0600), token redaction in logs, warning for open `allow_from`
- **Slack @mention Support**: Fixed AppMentionEvent handling for channel @mentions
- **Update Fallback**: Self-update now falls back to .tar.gz/.zip archive when bare binary returns 404
- **Skill Symlink**: Fixed skill directory scanning to follow symbolic links
- **QQBot Error Handling**: Added error logging for json.Unmarshal and WriteJSON calls
- **Claude Code Path**: Fixed underscore handling in findProjectDir path matching

### Improvements
- **Daemon Config Flag**: Support daemon install with config file path
- **Message Tracing**: Added message tracing and threaded replies
- **Scanner Buffer**: Optimized scanner buffer sizes for large outputs

## v1.2.1 (2026-03-09)

Patch release with bug fixes and minor enhancements.

### Bug Fixes
- **Engine: Idle Timer During Permission Wait** - Stop idle timer while waiting for user permission response to prevent session termination
- **Feishu: Nil Pointer Checks** - Add nil checks for `SenderId.OpenId` and `msg.Content` to prevent panics
- **Feishu: URL Validation** - Validate URLs before creating hyperlinks to prevent rejection of non-HTTP(S) URLs
- **Cron: Error Logging** - Log `json.Unmarshal` errors instead of silently ignoring when cron file is corrupted
- **Engine: Stale Event Prevention** - Add `drainEvents` utility to clear buffered events between turns

### New Features
- **Bind Setup Command** - `/bind setup` writes relay instructions to memory file for better bot-to-bot relay configuration

## v1.2.0 (2026-03-08)

This is the first stable release of cc-connect 1.2.0, consolidating all beta changes and adding new features.

### New Features (since beta.7)
- **Official QQ Bot Platform**: Native integration with Tencent's official QQ Bot Platform via WebSocket, supporting text, image, and document messages
- **iFlow CLI Agent**: Full support for iFlow CLI agent with interactive tool-call handling and mode switching
- **Shell Command Execution**: Custom commands can execute shell commands directly with `exec` field in config
- **Telegram Bot Menu**: Auto-register bot command menu on startup for better discoverability
- **DingTalk Reply Preprocessing**: Improved markdown content preprocessing for reply messages
- **Multi-Bot Relay Persistence**: Relay bindings now persist across restarts with improved binding messages

### Improvements
- **Quiet Mode**: `/quiet` now supports both per-session and global scope modes
- **Compression Command**: Improved `/compress` command handling and code refactoring
- **i18n**: Added new message keys and improved command formatting

### All 1.2.0 Highlights (from beta releases)
- **Bot-to-Bot Relay**: Forward messages between different messaging platforms
- **Streaming Preview**: Real-time message preview on Telegram, Discord, and Feishu
- **Typing Indicators**: Visual processing feedback on supported platforms
- **Session Search**: Search sessions by name, ID prefix, or summary
- **Custom Slash Commands**: Define reusable prompt templates
- **Agent Skills Discovery**: Auto-discover and invoke user-defined skills
- **Daemon Mode**: Run as background service with systemd/launchd support
- **Rate Limiting**: Per-session sliding-window rate limiter
- **Command Aliases**: Define shortcut aliases for commands
- **Self-Update**: In-place binary updates with auto-restart
- And many more improvements and bug fixes...

## v1.2.0-beta.7 (2026-03-07)

### New Features
- **Multi-Bot Relay Binding**: `/bind` now supports binding multiple bots in a group chat; use `/bind <project>` to add, `/bind -<project>` to remove specific project
- **System-level Systemd**: Daemon mode now supports system-level systemd (`/etc/systemd/system/`) when running as root, useful for servers and containers
- **Config Example Command**: `cc-connect config-example` prints embedded config template for quick reference
- **Interactive Command Buttons**: `/lang`, `/model`, `/mode` commands now show interactive button menus for easy selection
- **Exec Commands**: Custom commands can execute shell commands directly with `exec` field in config
- **Configurable Idle Timeout**: Agent idle timeout can be configured via `idle_timeout_mins` in config

### Improvements
- **Daemon Error Messages**: Improved systemd detection and error messages for WSL2, containers, and SSH environments
- **Codex CLI Visibility**: Patched codex session source to make CLI output visible

### Bug Fixes
- **Streaming Preview**: Fixed stale preview messages when streaming degrades

## v1.2.0-beta.6 (2026-03-06)

### New Features
- **Bot-to-Bot Relay**: Forward messages between different messaging platforms via CLI (`cc-connect relay`) and internal API; enables cross-platform bot communication
- **Session Search**: Search sessions by name, ID prefix, or summary with `/search <keyword>` command
- **List Pagination**: `/list` now supports pagination with `--page` and `--page-size` flags for large session counts
- **Per-Platform Streaming Preview Control**: Configure streaming preview per platform via `streaming_preview` setting (Telegram, Discord, Feishu)
- **Silent Cron Mode**: Suppress cron job notification messages with `silent = true` in cron job config
- **Voice Qwen Mode**: Voice function now supports Qwen audio model for speech-to-text
- **Feishu Three-Tier Rendering**: Intelligent markdown rendering strategy — simple text uses plain messages, rich markdown uses Post, code blocks/tables use Card

### Improvements
- **Status Display**: Improved `/status` command output with better formatting and Feishu message rendering fixes
- **Self-Update**: Auto-restart after update; added Gitee mirror support for Chinese users
- **Windows Self-Update**: Full Windows support for in-place binary updates
- **Message Splitting**: Improved boundary checks for cleaner message chunking
- **Platform Startup**: Better error handling and logging during platform initialization
- **Session Switch i18n**: Added translation for session switch success message

### Bug Fixes
- **Idle Session Timeout**: Added timeout for unresponsive agent sessions to prevent hangs
- **Streaming Preview**: Removed `maxChars` check that caused premature preview termination
- **Message Deduplication**: Deduplicate messages by process start time to prevent duplicate processing

## v1.2.0-beta.5 (2026-03-06)

### New Features
- **Streaming Preview**: Real-time message preview that updates in-place as the agent streams output; supported on Telegram, Discord, and Feishu with configurable interval, min delta, and max length
- **Rate Limiting**: Per-session sliding-window rate limiter to prevent message flooding; configurable `max_messages` and `window_secs`
- **Typing Indicators**: Visual processing feedback — Telegram/Discord show native typing action, Feishu adds emoji reaction (auto-removed on completion)
- **Command Aliases**: Define shortcut aliases for commands (`[[aliases]]` in config.toml or `/alias add`); e.g. map "帮助" → "/help"
- **Banned Words Filter**: Block messages containing configured sensitive words (`banned_words` in config.toml)
- **Project-level Command Disabling**: Disable specific commands per project via `disabled_commands` config
- **Session Deletion**: Delete sessions with `/del` command
- **`/switch` Fuzzy Matching**: Switch sessions by name, ID prefix, or summary substring in addition to numeric index

### Improvements
- **Streaming Preview + Tool Messages UX**: In non-quiet mode, when thinking/tool messages are sent, the streaming preview freezes and the final response is delivered as a new message at the bottom of the chat (instead of silently updating an older message above the tool messages)
- **Telegram Markdown→HTML**: Full Markdown-to-HTML conversion with proper escaping, placeholder-based tag nesting, and automatic fallback to plain text on parse errors
- **Discord Code-Fence-Aware Splitting**: Message chunking now respects code block boundaries, closing and re-opening fences across splits
- **Feishu Dual Rendering**: Simple markdown uses Post messages (normal font), code blocks/tables use Card messages (native rendering); matches Claude-to-IM's approach
- **Feishu Permission Interaction**: Confirmed WebSocket mode incompatibility with card button callbacks; uses text-based `/perm` commands (consistent with Claude-to-IM)
- **Session Creation & Naming**: Improved session naming with last user message as summary
- **Graceful Shutdown**: Improved context handling and lock release during shutdown
- **Unit Tests**: Added ~50 new test cases covering markdown conversion, message splitting, session management, and engine logic

### Bug Fixes
- **Telegram HTML Crossed Tags**: Fixed `<b><i>...</b></i>` nesting issues by using placeholder-based formatting pipeline
- **Telegram HTML Attribute Escaping**: Fixed `"` in URLs breaking `<a href>` attributes (escape to `&quot;`)
- **Telegram Duplicate Messages**: Fixed duplicate sends caused by streaming preview optimization skipping final HTML update
- **Streaming Preview Cursor**: Removed trailing `▍` cursor from final messages
- **Feishu Message Recall**: Unified preview and final message types to Card, eliminating unnecessary delete-and-resend
- **Feishu Reaction Cleanup**: Register empty handler for `im.message.reaction.deleted_v1` to suppress error logs
- **`fmt.Sprintf` Warnings**: Remove non-constant format strings flagged by `go vet`

## v1.2.0-beta.2 (2026-03-01)

### New Features
- **`/upgrade` Command**: Check for available updates (including beta) and self-update the binary in-place; queries both GitHub and Gitee releases
- **`/restart` Command**: Restart cc-connect service from chat with post-restart success notification
- **`/config reload` Command**: Hot-reload configuration (display, providers, commands) without restarting
- **`/name` Command**: Set custom display names for sessions (e.g. `/name my-feature`, `/name 3 bugfix`); names persist across restarts and show in `/list`, `/switch`, `/status`
- **Default Quiet Mode**: Configure `quiet = true` globally or per-project in config.toml to suppress thinking/tool progress by default; users can still toggle with `/quiet`
- **Command Prefix Matching**: Type shortened commands like `/pro l` for `/provider list`, `/sw 2` for `/switch 2`; works for all commands and subcommands
- **Numeric Session Switching**: `/list` shows numbered sessions; `/switch 3` switches by number instead of copying long IDs
- **Group Chat Mention Filtering**: Feishu, Discord, and Telegram bots now only respond to @mentions in group chats instead of all messages
- **Claude Code Router Support**: Integration with Claude Code Router for enhanced routing capabilities
- **Third-party Provider Proxy**: Local reverse proxy rewrites incompatible `thinking` parameters for third-party LLM providers (e.g. SiliconFlow)

### Improvements
- **Session History for Claude Code**: `/history` now works after `/switch` by reading from agent JSONL files
- **List Summary**: `/list` now shows the most recent user message as summary instead of the first
- **Session Names in UI**: Custom session names display with 📌 prefix in `/list`, `/switch`, `/status`
- **API Server Shutdown**: Clean shutdown without "use of closed network connection" error
- **Agent Session Timeouts**: 8-second graceful shutdown timeout for all agent sessions with kill fallback
- **Feishu Rich Text**: Use Post (rich text) messages instead of Interactive Cards for normal font size

### Bug Fixes
- **DingTalk Startup**: Fix false startup failure when stream client returns nil error
- **Deadlock on /new and /switch**: Release lock before async agent session close to prevent hangs
- **Provider Command**: Correctly list providers when no active provider is set
- **Unknown Command Handling**: Show i18n-friendly warning and fall through to agent for native commands

### Security & Reliability
- **Race Condition Fixes**: `sync.Once` for channel close, mutex protection for concurrent fields, non-blocking event sends
- **Atomic File Writes**: Config, session, and cron files use temp+rename pattern
- **Message Deduplication**: Platform-level dedup for Feishu and DingTalk webhooks
- **HTTP Client Timeouts**: Shared 30s-timeout HTTP client for all outbound requests
- **Path Traversal Protection**: Validate command file paths
- **Sensitive Data Redaction**: Redact API keys and tokens in logs

## v1.2.0-beta.1 (2026-03-01)

### New Features
- **Custom Slash Commands**: Define reusable prompt templates as global slash commands (`[[commands]]` in config.toml or `/commands add`); supports positional parameters (`{{1}}`), rest parameters (`{{2*}}`), default values (`{{1:default}}`), and runtime add/del/list
- **Agent Skills Discovery**: Auto-discover and invoke user-defined skills from agent directories (e.g. `.claude/skills/<name>/SKILL.md`); list with `/skills`, invoke with `/<skill-name> [args]`; supports all agents (Claude Code, Cursor, Gemini, Codex, Qoder)
- **`/config` Command**: View and modify runtime configuration (e.g. `thinking_max_len`, `tool_max_len`) from chat, with persistent save to `config.toml`
- **`/doctor` Command**: Run system diagnostics covering agent authentication, platform connectivity, system resources, dependencies, and network latency; fully i18n-supported
- **Discord Slash Commands**: Register native Discord Application Commands so typing `/` shows an autocomplete menu; supports per-guild instant registration via `guild_id` config
- **Daemon Mode**: Run cc-connect as a background service (`cc-connect daemon install/start/stop/status/logs`); supports systemd (Linux) and launchd (macOS)
- **Qoder CLI Agent**: Full support for the Qoder coding agent with streaming JSON, mode switching, and model selection
- **Telegram Proxy**: Support HTTP/SOCKS5 proxy for Telegram bot API connections
- **WeChat Work Proxy Auth**: Add `proxy_username` / `proxy_password` for authenticated forward proxies
- **i18n Expansion**: Add Traditional Chinese (zh-TW), Japanese (ja), and Spanish (es) language support
- **`--stdin` Support**: Read prompt from stdin for CLI usage (`echo "hello" | cc-connect send --stdin`)

### Improvements
- **Slow Operation Monitoring**: Warn-level logs for slow platform send (>2s), agent start (>5s), agent close (>3s), agent send (>2s), and agent first event (>15s); turn completion logs now include `turn_duration`
- **`tool_max_len=0` Fix**: Remove hardcoded 200-char truncation in all agent sessions (Claude Code, Cursor, Codex, Gemini, Qoder), making the user-configurable `tool_max_len` setting authoritative
- **Cursor `/list` Improvements**: Parse binary blob structure to show accurate message counts and first user message summary

### Bug Fixes
- **Telegram proxy**: Only override `http.Transport` when proxy is actually configured
- **Discord interaction fallback**: Gracefully fallback to channel messages when interaction token expires

## v1.1.0 (2026-03-02)

### New Features
- **`/compress` Command**: Compress/compact conversation context by forwarding native commands to agents (Claude Code `/compact`, Codex `/compact`, Gemini `/compress`); keeps long sessions manageable
- **Auto-Compress**: Added optional automatic context compression when estimated token usage exceeds a configurable threshold (`[projects.auto_compress]`).
- **Telegram Inline Buttons**: Permission prompts on Telegram now use clickable inline keyboard buttons (Allow / Deny / Allow All) instead of requiring text replies
- **`/model` Command**: View and switch AI models at runtime; supports numbered quick-select and custom model names. Fetches available models from provider API in real-time (Anthropic, OpenAI, Google), with built-in fallback list
- **`/memory` Command**: View and edit agent memory files (CLAUDE.md, AGENTS.md, GEMINI.md) directly from chat; supports both project-level and global-level (`/memory global`)
- **`/status` Command**: Display system status including project, agent, platforms, uptime, language, permission mode, session info, and cron job count

### Improvements
- **Cron list display**: Multi-line card-style formatting with human-readable schedule translations and next execution time
- **Model switch resets session**: Switching model via `/model` now starts a fresh agent session instead of resuming the old one, preventing stale context from affecting the new model
- **Permission modes docs**: README now documents permission modes for all four agents (Claude Code, Codex, Cursor Agent, Gemini CLI)
- **Natural language scheduling docs**: INSTALL.md now explains how to enable cron job creation via natural language for non-Claude agents
- **README revamp**: Redesigned project header with architecture diagram, feature highlights, and multi-agent positioning

### Bug Fixes
- **Gemini `/list` summary**: Fixed session list showing raw JSON (`{"dummy": true}`) instead of actual user message summary
- **GitHub Issue Templates**: Added structured templates for bug reports, feature requests, and platform/agent support requests

## v1.1.0-beta.7 (2026-03-02)

(see v1.1.0 above — beta.7 changes are included in the stable release)

## v1.1.0-beta.6 (2026-02-28)

### New Features
- **QQ Platform** (Beta): Support QQ messaging via OneBot v11 / NapCat WebSocket
- **Cron Scheduling**: Schedule recurring tasks via `/cron` command or CLI (`cc-connect cron add`), with JSON persistence and agent-aware session injection
- **Feishu Emoji Reaction**: Auto-add emoji reaction (default: "OnIt") on incoming messages to confirm receipt; configurable via `reaction_emoji`
- **Display Truncation Config**: New `[display]` config section to control thinking/tool message truncation (`thinking_max_len`, `tool_max_len`); set to 0 to disable truncation
- **`/version` Command**: Check current cc-connect version from within chat

### Bug Fixes
- **Windows `/list` fix**: Claude Code sessions now discoverable on Windows despite drive letter colon in project key paths
- **CLAUDECODE env filter**: Prevent nested Claude Code session crash by filtering CLAUDECODE env var from subprocesses

### Docs
- Clarified global config path `~/.cc-connect-qhn/config.toml` in INSTALL.md
- Fixed markdown image syntax in Chinese README

## v1.1.0-beta.5 (2026-03-01)

### New Features
- **Gemini CLI Agent**: Full support for `gemini` CLI with streaming JSON, mode switching, and provider management
- **Cursor Agent**: Integration with Cursor Agent CLI (`agent`) with mode and provider support

## v1.1.0-beta.4 (2026-03-01)

### Bug Fixes
- Fixed npm install: check binary version on install, replace outdated binary instead of skipping
- Added auto-reinstall logic for outdated binaries in `run.js`

## v1.1.0-beta.3 (2026-03-01)

### New Features
- **Voice Messages (STT)**: Transcribe voice messages to text via OpenAI Whisper, Groq Whisper, or SiliconFlow SenseVoice; requires `ffmpeg`
- **Image Support**: Handle image messages across platforms with multimodal content forwarding to agents
- **CLI Send**: `cc-connect send` command and internal Unix socket API for programmatic message sending
- **Message Dedup**: Prevent duplicate processing of WeChat Work messages

## v1.1.0-beta.2 (2026-03-01)

### New Features
- **Provider Management**: `/provider` command for runtime API provider switching; CLI `cc-connect provider add/list`
- **Configurable Data Dir**: Session data stored in `~/.cc-connect-qhn/` by default (configurable via `data_dir`)
- **Markdown Stripping**: Plain text fallback for platforms that don't support markdown (e.g. WeChat)

## v1.1.0-beta.1 (2026-03-01)

### New Features
- **Codex Agent**: OpenAI Codex CLI integration
- **Self-Update**: `cc-connect update` and `cc-connect check-update` commands
- **I18n**: Auto-detect language, `/lang` command to switch between English and Chinese
- **Session Persistence**: Sessions saved to disk as JSON, restored on restart

## v1.0.1 (2026-02-28)

- Bug fixes and stability improvements

## v1.0.0 (2026-02-28)

- Initial release
- Claude Code agent support
- Platforms: Feishu, DingTalk, Telegram, Slack, Discord, LINE, WeChat Work
- Commands: `/new`, `/list`, `/switch`, `/history`, `/quiet`, `/mode`, `/allow`, `/stop`, `/help`
