# 聊天斜杠命令参考

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。
> 权威来源：`core/engine_turn.go` 的 `builtinCommands` + `core/engine_setup.go` 的 `privilegedCommands`。

在任意连接的 IM 会话里发 `/命令` 即可。括号内是等价别名。带 🔒 的是**特权命令**，
需要 `admin_from` 授权（见 advanced.md 的 admin_from 段），否则返回无权限。

## 会话管理

| 命令 | 说明 |
|------|------|
| `/new [name]` | 新建会话（可选命名）。Web/bridge 平台额外**新增**独立会话，不破坏当前会话 |
| `/list`（`/sessions`） | 列出 agent 会话（分页，20/页） |
| `/switch <序号\|id>` | 按列表序号或 ID 切换/恢复会话 |
| `/name [序号] <x>`（`/rename`） | 重命名会话 |
| `/current` | 显示当前活跃会话 |
| `/delete <N\|1,2,3\|3-7>`（`/del` `/rm`） | **批量**删除会话（支持逗号与区间，如 `1,3-5,8`） |
| `/history [n]` | 显示最近 n 条消息（默认 10） |
| `/search <关键词>`（`/find`） | 按会话名/摘要/ID 搜索（v1.3.12 起支持消息历史全文） |

## 状态 / 查询

| 命令 | 说明 |
|------|------|
| `/status` | 显示运行状态（版本/运行时长/平台） |
| `/usage`（`/quota`） | token/配额用量 |
| `/doctor` | 健康诊断（agent 二进制/认证/平台/系统/依赖/网络，见 `references/gotchas.md`） |
| `/whoami`（`/myid`） | 查自己的 User ID（配置 admin_from 时用） |
| `/version` | 显示版本 |
| `/help` | 列出所有命令 |

> **`/ps`（`/btw`）不是"列出进程"**：它是向**正在执行的回合**追加一句补充输入
> （P.S.），空闲时会被拒绝（防止 stdin 竞态）。用法：`/ps 顺便也看下 xxx`。

## Agent 控制

| 命令 | 说明 |
|------|------|
| `/mode [name]` | 查看/切换权限模式（default/edit/plan/yolo…），支持当前会话热切换 |
| `/model [switch <名/别名>]` | 查看/切换模型 |
| `/reasoning [level]`（`/effort`） | 查看/切换推理强度（Codex） |
| `/allow <tool>` | 预授权某工具（下个会话生效）；无参显示已授权 |
| `/provider ...` | 管理 provider（list/add/remove/switch/current/clear/reset/none，见 providers.md） |
| `/memory [add\|global\|global add]` | 管理 agent 记忆文件 |
| `/cancel` | 取消当前轮次，**会话保持存活**（仍回一条「已取消」提示） |
| `/stop` | 停止当前执行（会话级） |
| `/compress`（`/compact`） | 手动压缩上下文 |
| `/lang <zh\|en\|zh-TW\|ja\|es\|auto>` | 切换回复语言 |

> **连续发消息时的提示语义（v1.3.17 起）**：会话忙时再发一条消息，若 agent 支持
> `interruptible`（codebuddy 恒开）则**打断当前轮、优先跑新消息**；否则**排队**。
> 这两种情况**都不再由 heron-connect 发送提示气泡** —— 内容由 CLI 自己产出，
> 避免引擎回复与 CLI 输出重复。`/cancel` 是显式取消，仍会回「已取消」。

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
`/restart`、`/upgrade`（`/update`）、`/web`、`/diff`。

> 完整特权集见 `core/engine_setup.go:495` 的 `privilegedCommands`：
> **shell / show / dir / restart / upgrade / web / diff**（共 7 个）。
> 未设置 `admin_from` 时这些命令**默认全部拒绝**（fail-closed）。
> 注意 `/search`、`/tts`、`/workspace`、`/doctor` **不是**特权命令。
> 另有 role 级 `disabled_commands` 可按项目裁剪。
