# 静默吞错模式排查报告

> 2026-09-01，承接空响应修复（docs/research/07-empty-response-analysis.md）后的系统排查。
> 主题：**错误被静默吞掉 / 降级成"看似正常" / 静默失败**，与空响应同族。

## 已修复（本次）

- 所有 agent 适配器空 result → EventError（见 07 文档）
- engine_turn.go 三处 `pendingSend` 错误 Debug 级 → Warn 级（1469/1537/1625），让"已成功 turn 的异步 Send 尾音错误"至少可查（语义上这轮已成功，不通知用户以免误导）

## 待处理（按严重程度排序，未改）

### 第一优先级：用户无感知的失败

| # | 位置 | 问题 |
|---|---|---|
| 1 | relay/relay.go:247/251 | relay 群发 `ReconstructReplyCtx`/`Send` 失败 Debug 级静默，转发丢失无感知 |
| 2 | bridge/bridge.go:1434 + 多处 `_ = sendToAdapter` | web 端 typing/card/delete 下发失败 Debug 级，用户点击无反馈；`1134` card `cmd:` 动作 handler nil 静默丢弃 |
| 3 | core/engine.go:101-129 | `/restart` 成功通知各失败路径 Debug 级，用户收不到重启确认 |

### 第二优先级：数据不一致 / 动作失败无反馈

| # | 位置 | 问题 |
|---|---|---|
| 4 | management/mgmt_server.go:1427/1455/1513 | provider 激活/删除/新增 `_ = ApplyXxxSave(...)` 忽略持久化错误，API 返回 200 但重启后丢失 |
| 5 | core/engine_card_actions.go:386-389 | 目录卡片动作 `errMsg` 仅 Debug，用户点击失败无反馈 |
| 6 | platform/discord/discord.go:360-427 | JoinThread 失败 Debug，回复可能发错位置 |
| 7 | core/engine_alias_cmds.go:135-139 | `/delete` 卡模式状态创建失败 `_ =` 忽略 |
| 8 | agent/codex/appserver_session.go:532/596/644/649/658 | 审批/权限/tool 响应写回失败 `_ =` 静默 |

### 第三优先级：仅日志缺失（有兜底，不丢内容）

- core/streaming.go 预览清理/rollover 失败 Debug（有 fallback 到 send）
- platform 各适配器 typing/reaction 失败 Debug（不影响主消息）
- 富卡片中间更新失败 Debug（最终有 fallback）

## 结论

最值得优先修的是 **relay 群发失败静默** 和 **bridge 下发失败静默**（#1/#2），它们与已修的空响应 bug 同性质——真实错误让用户无感知。其余（#4 provider 持久化）属于数据一致性，也值得单独处理。第三优先级可暂缓。

修复建议：第一/二优先级的 `slog.Debug` + `continue`/`_ =` 模式，统一改为 `slog.Warn`（可查）或按语义通知用户/返回错误。
