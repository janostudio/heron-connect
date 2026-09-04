# 高级 / 管理功能配置

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

## 日志（[log]）— 按天归档 + 按大小轮转

```toml
[log]
level = "info"               # debug | info | warn | error
# file = "/abs/path/app.log" # 日志文件路径；空 = 前台打 stdout / daemon 打默认文件
# max_size_mb = 10           # 单文件大小阈值（MB），超限切分归档
# retention_days = 7         # 归档保留天数（按天切分 + 自动清理）
```

**四个字段全部在 `[log]` 段配置，日常改日志直接编辑 toml 即可**，不需要命令行。

### `file` 路径的解析规则（重要）

- **相对路径的基准随运行方式不同**：
  - **前台运行**：相对**启动命令的当前目录（cwd）**。
  - **daemon 运行**：相对 `heron-connect daemon install --work-dir` 指定的目录
    （默认 = config.toml 所在目录；systemd 的 `WorkingDirectory` / launchd 的
    `WorkingDirectory` 都设为它）。
  - ⚠️ 所以 `file = "logs/app.log"` 写到哪里取决于在哪启动、work-dir 指到哪。
    **要稳定就用绝对路径**，如 `file = "/var/log/heron-connect/app.log"`。
- 代码**不自动补绝对路径**（`filepath.Dir` + `os.OpenFile` 原样使用），目录不存在会
  自动 `MkdirAll` 创建。
- 若 `file` 缺省（空）：
  - 前台运行 → 打 stdout，不落文件。
  - daemon 运行 → 写默认 `~/.heron-connect/logs/heron-connect.log`。

### 优先级（覆盖顺序）

```
CC_LOG_FILE / CC_LOG_MAX_SIZE / CC_LOG_RETENTION_DAYS  环境变量（最高）
        ↓ 覆盖
[log] 段的 file / max_size_mb / retention_days
        ↓ 覆盖
默认值（max_size 10MB / retention 7 天）
```

> 命令行 `--log-file` 等参数**只在 `heron-connect daemon install` 时可选传入**，属于
> 安装期覆盖（CLI > TOML > 默认）；运行时改日志一律走 toml 或 `CC_*` 环境变量。

### 轮转与归档

- 轮转是「按天 + 按大小」双轨：每天切分，且单文件超 `max_size_mb` 也切分。
- 归档命名 `app-YYYY-MM-DD[-seq].log`，与 `file` **同目录**；主文件始终叫 `app.log`
  （或你指定的名字）。
- 归档超过 `retention_days` 天自动删除，不会无限膨胀。

## 流式预览（[stream_preview]）与即时回复（[instant_reply]）

实时流式预览把 Agent 输出边生成边更新到消息里（类似"正在输入"），支持 Telegram /
Discord / 飞书；即时回复在收到用户消息后、Agent 开始处理前先发一条确认消息。

```toml
[stream_preview]
enabled = true            # 默认 true
# disabled_platforms = ["feishu"]   # 在指定平台禁用流式预览
interval_ms = 1500        # 更新最小间隔毫秒（默认 1500）
min_delta_chars = 30      # 发送更新前最少新增字符数（默认 30）
max_chars = 4000          # 预览最大长度（默认 4000）

[instant_reply]
enabled = false           # 默认 false
content = "🤔 Thinking..." # 自定义文案；空 = 用 i18n 默认（"⏳ 处理中..."）
```

- 已配置流式卡片的平台（如钉钉 AI Card）会自动跳过即时回复（卡片本身已带处理指示）。
- `instant_reply` 与 `[projects.users]` 里的每会话 `rate_limit` 是两个不同概念，见下文。

## 速率限制（[rate_limit] 与 [outgoing_rate_limit]）

`[rate_limit]` 是**入站**限流（防止用户刷屏），`[outgoing_rate_limit]` 是**出站**限流（防止
对平台 API 发送过快被封号，如企微）。

```toml
[rate_limit]
max_messages = 20         # 窗口内最大消息数；0=禁用（默认 20）
window_secs = 60          # 滑动窗口秒数（默认 60）

[outgoing_rate_limit]
max_per_second = 0        # 全局每秒消息数；0=无限制（默认）
burst = 3                 # 最大突发；默认 = ceil(max_per_second)

# 每平台覆盖（可选）
[outgoing_rate_limit.platforms.wecom]
max_per_second = 1
[outgoing_rate_limit.platforms.telegram]
max_per_second = 25
```

> `[projects.users.roles.*]` 里的 `rate_limit` 是**按角色**的入站限流（见下文 ACL），
> 与这里的全局 `[rate_limit]` 可叠加，取更严格者。

## Web 管理后台 + Management API（[management]）

```toml
[management]
enabled = true
port = 9820                # 默认 9820
token = "${MANAGEMENT_TOKEN}"   # 必填
# cors_origins = ["https://app.example.com"]
```

启动后浏览器打开 `http://localhost:9820/`；接口地址 `http://localhost:9820/api/`，
详见 docs/management-api.md。
> 仅在未用 `no_web` 标签编译时可用。

### `heron-connect web` 命令的完整行为（重要）

`heron-connect web` 是一键开启 Web 后台的引导命令，**只配置并打开浏览器，不启动服务本身**
（服务还是要单独跑 `heron-connect`）。它的具体行为：

1. **找不到 config 文件 → 直接报错退出**（不会自动生成 config；要先跑一次 `heron-connect`）。
2. **`[management]` 未启用 → 自动配置**：生成 management token 和 bridge token 各一个
   （16 字节随机），写回 config.toml，并提示「**重启 heron-connect 后生效**」。
   —— 注意它会**同时改 `[management]` 和 `[bridge]` 两段**（Web 会话依赖 bridge 接入）。
3. 默认用 `http://localhost:PORT/login?token=...` 自动开浏览器（WSL 走 `cmd.exe start`）。
4. `heron-connect web --no-browser`（`-n`）→ 只打印 URL 和 token，不开浏览器。

> 聊天内也有等价命令 `/web setup`（🔒 特权）和 `/web status`。

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

## 定时任务（cron）

定时任务让 Agent 按 cron 表达式自动执行 prompt 或 shell 命令。任务**定义**持久化在
`<data_dir>/crons/jobs.json`（静态配置，可纳入版本控制），运行时状态（`last_run` /
`last_error`）单独存 `<data_dir>/crons/.state.json`，避免每次执行都改写 jobs.json。

### 全局 [cron] 段

```toml
[cron]
silent = false             # 启动时不发"⏰ 任务名"通知；默认 false
session_mode = "reuse"     # 所有任务默认会话模式：reuse(默认) | new_per_run
cron_data_dir = ""         # jobs.json 目录；空 = 用顶层 data_dir
```

### 任务字段（jobs.json 里每个 job 的 JSON 字段）

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 自动生成的唯一 ID（只读） |
| `project` | string | 所属 project 名 |
| `session_key` | string | 投递目标会话 key（`no_delivery` 时可空） |
| `cron_expr` | string | 标准 5 段 cron 表达式，如 `"0 6 * * *"` |
| `prompt` | string | 交给 agent 的任务 prompt（与 `exec` 二选一） |
| `exec` | string | 直接执行的 shell 命令（与 `prompt` 二选一） |
| `work_dir` | string | `exec` 的工作目录；空 = agent 的 work_dir |
| `description` | string | 简短描述 |
| `enabled` | bool | 是否启用 |
| `silent` | bool | 抑制开始通知；nil = 用全局默认 |
| `mute` | bool | 抑制**所有**消息（开始+结果），静默执行 |
| `session_mode` | string | `reuse`(复用活跃会话) 或 `new_per_run`(每次新建会话) |
| `mode` | string | 权限模式覆盖：default / bypassPermissions / acceptEdits / plan / auto / dontAsk；空 = 用项目默认 |
| `timeout_mins` | int | 单次执行最多等待分钟数；nil=默认 30，0=不限，>0=分钟数 |
| `retry_count` | int | 失败后重试次数；默认 1，0=不重试 |
| `notify_on_failure` | bool | 重试耗尽后是否向目标会话推失败通知；默认 false |
| `no_delivery` | bool | 无投递模式：agent 只产出文件/副作用，不向任何平台发消息（`session_key` 忽略，可空） |
| `created_at` | time | 创建时间（只读） |

> 完整 cron 指南见仓库 `docs/cron-usage.md`（含字段详解、会话模式、cron 表达式参考、FAQ）。

### 四种创建方式

| 方式 | 入口 | 适用场景 |
|------|------|---------|
| 聊天命令 | 在 IM 里发 `/cron ...` | 最简单，自动绑定当前会话 |
| CLI | `heron-connect cron ...` | 脚本化 / 跨会话指定 session_key |
| 管理 API | `POST /api/v1/cron` 等 | 外部系统集成 |
| Web UI | 后台 Cron 页面 | 可视化（表单暂不支持 session_mode/mute/timeout_mins） |

### 聊天命令（/cron）

```
/cron add <分> <时> <日> <月> <星期> <提示词>       # 创建 prompt 任务（绑定当前会话）
/cron addexec <分> <时> <日> <月> <星期> <shell>     # 创建 shell 任务（仅管理员）
/cron list                                          # 列出所有任务
/cron del <任务ID>
/cron enable <任务ID>  /  /cron disable <任务ID>
/cron mute <任务ID>    /  /cron unmute <任务ID>
/cron setup                                          # 显示配置指引
/cron                                                # 显示任务列表（支持卡片）
```

### CLI 子命令

```bash
heron-connect cron add [options] [<min> <hour> <day> <month> <weekday> <prompt>]
heron-connect cron list [--project <name>]
heron-connect cron edit <id> <field> <value>
heron-connect cron info <id> [field]
heron-connect cron del <id>          # 也可 delete/rm/remove
```

**add 常用选项**：

```bash
heron-connect cron add --cron "0 6 * * *" --prompt "汇总 GitHub trending" --desc "每日 Trending"
heron-connect cron add --cron "*/30 * * * *" --exec "df -h" --desc "磁盘检查"
heron-connect cron add 0 6 * * * 汇总 GitHub trending 并总结
# 其它选项：-p/--project、-s/--session-key、--session-mode、--timeout-mins、--data-dir
```

**edit 可编辑字段**：
- string：`project` `session_key` `cron_expr` `prompt` `exec` `work_dir` `description` `session_mode`
- bool：`enabled` `mute` `silent`
- int：`timeout_mins`
- 只读：`id` `created_at` `last_run` `last_error`

```bash
heron-connect cron edit <id> cron_expr "0 9 * * *"
heron-connect cron edit <id> enabled false
heron-connect cron edit <id> timeout_mins 60
heron-connect cron edit <id> mute true
```

### 无投递 cron（`no_delivery`）

`no_delivery: true` 是 cron 任务的一个字段，表示**无投递模式**：agent 照常执行并产出
文件/副作用，但**不向任何平台推送消息**（开始通知、结果都不发）。此时 `session_key`
被忽略，可以留空。

典型用途：只产数据、不需要在群里汇报的定时任务（如生成报表/大盘数据，产出文件即可）。
配合项目大盘见 `references/dashboard.md` 的「no_delivery 模式」。

```json
{ "cron_expr": "0 1 * * *", "prompt": "生成今日报表", "session_key": "", "no_delivery": true }
```

> 与 `mute` / `silent` 的区别：`silent` 只隐藏"开始"通知、结果照发；`mute` 完全静默
> （不推开始+结果）；`no_delivery` 更进一步——**连投递目标都不绑定**（`session_key`
> 留空），彻底不产生任何出站消息，适合纯后台产出型任务。

也可在聊天里直接告诉 Agent「每天早上 6 点汇总 GitHub trending」，由它调用 `/cron add`。

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

## 数据目录（data_dir）与各目录路径说明

`data_dir` 是 heron-connect 存放**所有运行时数据**的根目录。它决定了以下子目录/文件的实际位置：

| 数据 | 相对 data_dir 的路径 |
|------|---------------------|
| 会话历史 / session 状态 | `<data_dir>/sessions/` |
| 项目状态 | `<data_dir>/projects/<name>.state.json` |
| cron 任务定义 | `<data_dir>/crons/jobs.json`（状态在 `.state.json`） |
| 工作区绑定（multi-workspace） | `<data_dir>/workspace_bindings.json` |
| 心跳/relay/metrics/dashboard 数据 | `<data_dir>/` 下各自子目录 |

**路径语义（重要）**：

- 缺省（不写）→ 默认 `~/.heron-connect`（自动取用户 home 目录拼出绝对路径）。
- 显式设置 → **原样使用，不自动补绝对路径**。所以相对路径（如 `data_dir = "data"`）
  是相对**启动命令的 cwd**；要稳定就用绝对路径。
- `cron_data_dir`：cron 的 jobs.json 目录，**空 = 用 data_dir**；设置了则 cron 独立存放。
- 与 `[log].file` 无关：日志路径单独在 `[log]` 段配（见上文「日志」）。

## 其他全局设置

```toml
language = "zh"               # "en" | "zh" | "zh-TW" | "ja" | "es" | 空=自动检测
data_dir = "/abs/path/to/data"  # 运行时数据根目录；默认 ~/.heron-connect（见上表）
attachment_send = "on"        # off 则禁 IM 回传图片/文件
idle_timeout_mins = 120       # 两次 agent 事件间最大等待；0=禁用；默认 120
workspace_idle_timeout_mins = 15  # 多工作区模式下空闲回收阈值；0=禁用；默认 15（进程级全局）
provider_presets_url = "https://..."   # 远程推荐 provider 列表 JSON URL

# 每会话消息队列深度：会话忙时排队，超过 max_depth 则丢弃并提示
# [queue]
# max_depth = 5               # 每会话最多排队消息数（默认 5）

# 事件钩子：匹配到事件时执行命令或发 HTTP
# [[hooks]]
# event = "message.received"  # 事件名（见下方清单）或 "*"
# type = "command"            # command | http
# command = "echo $CC_HOOK_USER_NAME >> /tmp/cc.log"   # type=command 用
# # url = "https://example.com/hook"                    # type=http 用
# # async = true              # 默认 true；false = 阻塞到完成
# # timeout = 10              # 秒；command 默认 10，http 默认 5
```

### 事件钩子（[[hooks]]）事件清单

| event | 触发时机 |
|-------|---------|
| `message.received` | 用户消息到达 |
| `message.sent` | Agent 回复发送完成 |
| `session.started` | Agent 会话启动 |
| `session.ended` | Agent 会话结束 |
| `cron.triggered` | 定时任务触发 |
| `permission.requested` | Agent 请求权限 |
| `error` | 发生错误 |
| `*` | 匹配所有事件 |

`type = "command"` 时，事件上下文通过 `CC_HOOK_*` 环境变量传给 shell 命令：
`CC_HOOK_EVENT` / `CC_HOOK_PROJECT` / `CC_HOOK_TIMESTAMP` / `CC_HOOK_SESSION_KEY` /
`CC_HOOK_PLATFORM` / `CC_HOOK_USER_ID` / `CC_HOOK_USER_NAME` / `CC_HOOK_CONTENT` /
`CC_HOOK_ERROR`（按事件类型只填充相关字段）。`type = "http"` 时 POST JSON 载荷到 `url`。

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

### 命令占位符语法（prompt 和 exec 都支持）

| 占位符 | 含义 |
|--------|------|
| `{{1}}` `{{2}}` | 第 N 个位置参数 |
| `{{1:default}}` | 带默认值的位置参数 |
| `{{2*}}` | 第 N 个及之后所有参数 |
| `{{2*:default}}` | 同上，带默认值 |
| `{{args}}` | 所有参数 |
| `{{args:default}}` | 所有参数，无参数时用默认值 |
| （无占位符） | 参数追加到末尾 |

运行时也可动态管理：`/commands add`、`/commands addexec`（`--work-dir`）、`/commands del`。
此外会自动发现 agent 的 commands 目录（如 `.claude/commands/*.md`、`.gemini/commands/*.md`）。

## 屏蔽词（banned_words）

```toml
banned_words = ["违禁词1", "违禁词2", "secret_project"]   # 命中即拦截该条消息
```

- 匹配**大小写不敏感**（内部统一转小写比对）。

## 语音：STT 与 TTS（[speech] / [tts]）

需要 `ffmpeg`。STT 把语音转文字交给 agent；TTS 把回复合成为语音（飞书）。

### STT（语音转文字）

```toml
[speech]
enabled = true
provider = "openai"        # openai | groq | qwen | gemini
language = "zh"            # 空 = 自动检测
# [speech.openai]  api_key / base_url / model   # 按 provider 填对应子块
# [speech.groq]    api_key / model              # 默认 whisper-large-v3-turbo
# [speech.qwen]    ...  [speech.gemini] ...
```

- 音频先经 ffmpeg 转码（AMR/OGG → MP3）再送 provider。

### TTS（文字转语音）

```toml
[tts]
enabled = true
provider = "edge"          # qwen | openai | minimax | espeak | pico | edge
voice = "zh-CN-XiaoxiaoNeural"
# tts_mode = "voice_only"  # voice_only(默认，只回语音) | always(语音+文字都发)
# max_text_len = 300       # 合成文本最大长度，超限截断
# [tts.openai]  api_key / base_url / model
```

- `espeak` / `pico` 是**本地引擎**（无需 API key）；`qwen`/`openai`/`minimax`/`edge` 走在线。
- 合成后 wav → opus 依赖 ffmpeg。
- 运行时可用 `/tts [always|voice_only]` 切换。

## 本地文件引用标准化（[projects.references]）

控制 Agent 输出里的本地文件路径/平台链接如何渲染给用户。字段（`config.example.toml`
有完整示例）：

```toml
[projects.references]
normalize_agents = ["claudecode"]   # 把 agent 输出的绝对路径标准化为相对 work_dir 显示
render_platforms = ["feishu"]       # 对这些平台渲染可点击的引用（如飞书卡片链接）
display_path = "relative"           # relative(相对 work_dir) | absolute
marker_style = "backtick"           # 引用标记风格
enclosure_style = "code"            # 包裹风格
```

## 其它可选项目能力（简要）

```toml
# 上下文自动压缩：token 超阈值自动 /compress
# [projects.auto_compress]
# enabled = true
# max_tokens = 12000        # 触发阈值（默认 12000）
# min_gap_mins = 30         # 两次压缩最小间隔（默认 30）

# 观察模式：把原生终端 Claude Code 会话转发到 Slack 频道（依赖 Slack 平台）
# [projects.observe]
# enabled = true
# channel = "slack:C123"

# 多工作区：mode = "multi-workspace" 时按 base_dir 下子目录各建一个工作区
# 需 base_dir（不能与 agent.options.work_dir 同时用）；用 /workspace 命令管理
# [projects]
# name = "monorepo"
# mode = "multi-workspace"
# base_dir = "/path/to/monorepo"

# 注入发送者身份到每条消息
# inject_sender = true   # 放在 [[projects]] 下

# bot 对 bot 中继超时（relay send 用它）
# [relay]
# timeout_secs = 120      # 0=禁用中继；默认 120

# 以指定系统用户运行 agent（权限隔离；启动时 preflight 会检查，失败则拒启）
# [projects]
# run_as_user = "someuser"        # 用 sudo -n -iu 切用户运行
# run_as_env = ["PGSSLMODE"]      # 跨 sudo 白名单透传的环境变量名

# legacy 兼容字段：quiet = true 等价 display 隐藏 thinking/tool（新配置建议用 [display]）
# quiet = true
```

> `run_as_user` 的隔离诊断用 `heron-connect doctor user-isolation`。
> 顶部也有进程级 `--observe`/`--observe-channel` flag 可开观察模式（见 cli.md）。
