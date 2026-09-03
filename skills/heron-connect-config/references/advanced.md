# 高级 / 管理功能配置

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

## 日志（[log]）— 按天归档 + 按大小轮转

```toml
[log]
level = "info"               # debug | info | warn | error
# file = "logs/app.log"      # 日志文件路径；空 = 前台打 stdout
# max_size_mb = 10           # 单文件大小阈值（MB），超限切分归档
# retention_days = 7         # 归档保留天数（按天切分 + 自动清理）
```

- 日志文件路径 / 大小 / 保留天数均可在此配置，也可用 daemon CLI 参数覆盖
  （优先级 **CLI > TOML > 默认**，默认 10MB / 7 天）。
- 轮转策略是「按天 + 按大小」双轨：每天切分，且单文件超 `max_size_mb` 也切分。
  归档命名 `app-YYYY-MM-DD[-seq].log`，主文件始终叫 `app.log`（或你指定的名字）。
- 归档超过 `retention_days` 天自动删除，不会无限膨胀。
- 前台运行（非 daemon）若配了 `file` 也写文件并同样轮转；未配则打 stdout。

## Web 管理后台 + Management API（[management]）

```toml
[management]
enabled = true
port = 9820                # 默认 9820
token = "${MANAGEMENT_TOKEN}"   # 必填
# cors_origins = ["https://app.example.com"]
```

启动后浏览器打开 `http://localhost:9820/`；或 `heron-connect web`（自动配置 token 并开浏览器）。
接口地址 `http://localhost:9820/api/`，详见 docs/management-api.md。
> 仅在未用 `no_web` 标签编译时可用。

> **Web 会话的 platform 名是 `"web"`**：Web 后台消息经 `[bridge]` 接入，不经过任何
> 真实平台。想单独给 Web 会话配显示模式 / 关掉空闲切换，用虚拟 `type = "web"` 平台
> 条目 + `[projects.platforms.display]` / `reset_on_idle_mins = 0`，见
> `references/platforms.md` 的「虚拟 `type = "web"` 平台」。

## Bridge 外部接入（[bridge]）— WebSocket + REST

```toml
[bridge]
enabled = true
port = 9810                # 默认 9810
token = "${BRIDGE_TOKEN}"  # 需共享密钥；insecure=true 时才可省
# path = "/bridge/ws"      # 默认路径
# cors_origins = []
# insecure = false         # 本地开发无 token 运行（勿用于生产）
```

供自定义 UI 或脚本接入，协议见 docs/bridge-protocol.md。

## HTTP Webhook（[webhook]）

```toml
[webhook]
enabled = true
port = 9111                # 默认 9111
token = "shared-secret"    # 空=无鉴权
# path = "/hook"           # 路径前缀，默认 "/hook"
```

## 定时任务（[cron] 与 CLI）

```toml
[cron]
silent = false             # 启动时不发通知
session_mode = "reuse"     # "" | reuse(默认) | new_per_run
# cron_data_dir = "/path"  # cron 数据目录，默认用顶层 data_dir
```

更常用的是 CLI / 聊天命令：

```bash
heron-connect cron add --cron "0 6 * * *" --prompt "汇总 GitHub trending" --desc "每日 Trending"
```
也可在聊天里直接告诉 Agent「每天早上 6 点汇总 GitHub trending」。

## 心跳巡检（[projects.heartbeat]）

Agent 按固定间隔在主会话中唤醒检查环境（与 cron 不同，共享上下文）。

```toml
[projects.heartbeat]
enabled = true
interval_mins = 30
session_key = "telegram:123:123"   # 目标会话 key（必填）
# only_when_idle = true
# silent = true
# timeout_mins = 30
# prompt = "check inbox"           # 为空则读 work_dir 的 HEARTBEAT.md
```

## 多用户与命令 ACL（[projects.users]）

```toml
[projects.users]
default_role = "member"

[projects.users.roles.admin]
user_ids = ["platform_user_id_1"]
disabled_commands = []
rate_limit = { max_messages = 50, window_secs = 60 }

[projects.users.roles.member]
user_ids = ["*"]
disabled_commands = ["*"]                       # 禁用所有内置命令
rate_limit = { max_messages = 10, window_secs = 60 }
```

## 特权命令（admin_from）

`admin_from` 控制谁能执行 `/dir /shell /restart /upgrade /commands addexec` 等特权命令。

```toml
[projects]
name = "x"
# admin_from = "user_id_1,user_id_2"
# admin_from = "*"     # 警告：授予所有允许用户对主机的完整 shell 访问，仅限个人单用户部署
```
未设置则所有用户都无法使用特权命令。用 `/whoami` 查自己的 User ID。

## 其他全局设置

```toml
language = "zh"               # "en" | "zh" | "zh-TW" | "ja" | "es" | 空=自动检测
data_dir = "/path/to/custom/dir"
attachment_send = "on"        # off 则禁 IM 回传图片/文件
idle_timeout_mins = 120       # 两次 agent 事件间最大等待；0=禁用；默认 120
provider_presets_url = "https://..."   # 远程推荐 provider 列表 JSON URL

# 事件钩子：匹配到事件时执行命令或发 HTTP
# [[hooks]]
# event = "session.created"   # 事件名或 "*"
# type = "command"            # command | http
# command = "curl -X POST ..."
# # url = "https://example.com/hook"   # type=http 时用
```

## 自定义斜杠命令（[[commands]]）与别名（[[aliases]]）

全局自定义命令：`/名称` 展开成一段 prompt 模板或执行一条 shell 命令。

```toml
[[commands]]
name = "deploy"
description = "部署当前分支到测试环境"
prompt = "部署当前分支，先跑测试再发布到 staging"

# 或执行 shell（prompt/exec 二选一）
# [[commands]]
# name = "logs"
# description = "查看最近日志"
# exec = "tail -n 50 /var/log/app.log"
# work_dir = "/path/to/project"

[[aliases]]                    # 触发词 → 命令
name = "帮助"
command = "/help"
```

## 屏蔽词（banned_words）

```toml
banned_words = ["违禁词1", "违禁词2", "secret_project"]   # 命中即拦截该条消息
```

## 语音：STT 与 TTS（[speech] / [tts]）

需要 `ffmpeg`。STT 把语音转文字交给 agent；TTS 把回复合成为语音（飞书）。

```toml
[speech]
enabled = true
provider = "openai"        # openai | groq | qwen | gemini
language = "zh"            # 空=自动
# [speech.openai]  api_key / base_url / model   按 provider 填对应子块

[tts]
enabled = true
provider = "edge"          # qwen | openai | minimax | espeak | pico | edge
voice = "zh-CN-XiaoxiaoNeural"
# tts_mode = "voice_only"  # voice_only(默认) | always
# [tts.openai]  api_key / base_url / model
```

## 其它可选项目能力（简要）

```toml
# 上下文自动压缩：token 超阈值自动 /compress
# [projects.auto_compress]
# enabled = true
# max_tokens = 200000       # 触发阈值
# min_gap_mins = 30         # 两次压缩最小间隔

# 观察模式：把原生终端 Claude Code 会话转发到某个 IM 频道
# [projects.observe]
# enabled = true
# channel = "slack:C123"

# 多工作区：mode = "multi-workspace" 时按 base_dir 下子目录各建一个工作区
# 需 base_dir（不能与 agent.options.work_dir 同时用）
# [projects]
# name = "monorepo"
# mode = "multi-workspace"
# base_dir = "/path/to/monorepo"

# 注入发送者身份到每条消息
# inject_sender = true   # 放在 [[projects]] 下

# bot 对 bot 中继超时
# [relay]
# timeout_secs = 120      # 0=禁用中继；默认 120
```
