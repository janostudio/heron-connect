# 聊天斜杠命令参考

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。
> 权威来源：`core/engine_turn.go` 的 `builtinCommands` + `core/engine_setup.go` 的 `privilegedCommands`。

在任意连接的 IM 会话里发 `/命令` 即可。括号内是等价别名。带 🔒 的是**特权命令**，
需要 `admin_from` 授权（见 advanced.md 的 admin_from 段），否则返回无权限。

## 会话管理

| 命令 | 说明 |
|------|------|
| `/new [name]` | 新建会话（可选命名） |
| `/list`（`/sessions`） | 列出 agent 会话 |
| `/switch <id>` | 切换到某会话 |
| `/name <x>`（`/rename`） | 重命名当前会话 |
| `/current` | 显示当前活跃会话 |
| `/delete <id>`（`/del` `/rm`） | 删除会话 |
| `/history [n]` | 显示最近 n 条消息（默认 10） |

## 状态 / 查询

| 命令 | 说明 |
|------|------|
| `/status` | 显示运行状态 |
| `/usage`（`/quota`） | token/配额用量 |
| `/ps`（`/btw`） | 列出当前 agent 进程 |
| `/whoami`（`/myid`） | 查自己的 User ID（配置 admin_from 时用） |
| `/version` | 显示版本 |
| `/help` | 列出所有命令 |

## Agent 控制

| 命令 | 说明 |
|------|------|
| `/mode [name]` | 查看/切换权限模式（default/edit/plan/yolo…） |
| `/model [name]` | 查看/切换模型 |
| `/reasoning [level]`（`/effort`） | 查看/切换推理强度（Codex） |
| `/allow <tool>` | 预授权某工具（下个会话生效） |
| `/provider ...` | 管理 provider（list/add/remove/switch/current/clear/reset/none，见 providers.md） |
| `/memory` | 管理 agent 记忆 |
| `/cancel` | 取消当前执行 |
| `/stop` | 停止当前执行 |
| `/compress`（`/compact`） | 手动压缩上下文 |
| `/lang <zh\|en\|...>` | 切换回复语言 |

## 展示 / 输出

| 命令 | 说明 |
|------|------|
| `/quiet` | 循环切换 display 模式（full→quiet→compact→stream），会**改写** `tool_messages`/`thinking_messages`（见 display.md 与 gotchas） |
| `/tts [always\|voice_only]` | 查看/切换 TTS 模式 |

## 扩展类

| 命令 | 说明 |
|------|------|
| `/commands [add\|addexec\|del]`（`/command` `/cmd`） | 管理自定义命令（占位符语法见 advanced.md 的 commands 段） |
| `/skills`（`/skill`） | 列出 agent 的 .md skills |
| `/alias [add\|del\|list]` | 管理命令别名 |
| `/config get\|set\|reload` | 动态查看/改/重载配置 |
| `/bind` | 平台绑定制 |

## 定时 / 任务 / 大盘

| 命令 | 说明 |
|------|------|
| `/cron ...` | 定时任务（add/addexec/list/del/enable/disable/mute/unmute/setup，见 advanced.md） |
| `/dashboard ...` | 项目大盘（见 dashboard.md） |
| `/heartbeat ...`（`/hb`） | 心跳巡检（手动触发/管理，见 advanced.md） |

## 工作区 / 多工作区

| 命令 | 说明 |
|------|------|
| `/workspace init\|bind\|route\|unbind\|shared\|list`（`/ws`） | 多工作区模式管理（见 advanced.md 的 multi-workspace） |

## Web 后台

| 命令 | 说明 |
|------|------|
| `/web setup` | 🔒 聊天内开启 Web 管理后台（自动写 config，见 advanced.md） |
| `/web status` | 查看 Web 后台状态 |

## 特权命令（🔒 需 admin_from）

`/shell`（`/sh` `/exec` `/run`）、`/show`、`/dir`（`/cd` `/chdir` `/workdir`）、
`/restart`、`/upgrade`（`/update`）、`/web`、`/diff`、`/search`（`/find`）。

> 完整特权集见 `core/engine_setup.go` 的 `privilegedCommands`：shell/show/dir/restart/
> upgrade/web/diff。未设置 `admin_from` 时这些命令**默认全部拒绝**（fail-closed）。
