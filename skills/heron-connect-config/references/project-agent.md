# [[projects]] 与 Agent 配置

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

## 支持的 agent type（`[projects.agent].type`）

| type | 说明 |
|------|------|
| `claudecode` | Claude Code |
| `codebuddy` | CodeBuddy Code |
| `codex` | OpenAI Codex |
| `gemini` | Gemini CLI |
| `cursor` | Cursor Agent |
| `opencode` | OpenCode |
| `qoder` | Qoder CLI |
| `iflow` | iFlow CLI |
| `kimi` | Kimi |
| `devin` | Devin |
| `pi` | Pi |
| `heron` | Heron 自身 agent |
| `acp` | 任意 ACP 协议实现 |

> 实际可用 agent 取决于编译时的选择性编译（`no_*` build tag，默认全部编译）。
> 判断方式：查看 `cmd/heron-connect/plugin_agent_*.go` 是否存在对应文件。

## [[projects]] 常用字段

```toml
[[projects]]
name = "my-backend"
# run_as_user = "agent-user"        # 以不同 Unix 用户运行 agent（文件权限隔离）
# admin_from = "user_id_1,user_id_2"  # 谁可执行特权命令；"*"=全部（高风险）
# disabled_commands = ["restart", "upgrade", "cron"]
# show_context_indicator = false    # 隐藏回复末尾 "[ctx: ~N%]"
# reply_footer = false              # 隐藏 Codex 式底部状态行
# auto_session_title = true         # 会话自动生成标题（默认 true）
# filter_external_sessions = false  # /list 只显示 heron-connect 建的会话
# queued_messages = "merge"         # merge(默认) | serial
# reset_on_idle_mins = 720          # 会话空闲 N 分钟后自动切换新会话；0=禁用
                                    # 也可在单个 [[projects.platforms]] 上覆盖（含虚拟 web）
```

> `admin_from` 风险提示：`admin_from = "*"` 会给所有允许用户完整 shell 访问，
> 仅限个人单用户部署。`/whoami` 可查用户 ID。

## 多 project 配置（一个 toml 多个项目）

**核心语义（最容易写错）**：`[[projects]]` 每出现一次，就**开启一个新数组元素**。
它后面的所有相对表头 `[projects.agent]` / `[projects.agent.options]` /
`[projects.platforms]` 都属于**最近的那一个** `[[projects]]`。所以顺序必须严格是
`[[projects]] → [projects.xxx] → [[projects]] → [projects.xxx]`。

每个 project 完全独立：独立的 `name`、agent type、`work_dir`、平台列表、权限、心跳等。
它们共享同一个 heron-connect 进程与同一个 `[log]`/`[management]`/`[bridge]` 等全局设置。

```toml
[log]
level = "info"                      # 全局：所有 project 共用

[[projects]]                        # ── 第 1 个项目
name = "backend"                    #    name 唯一标识，CLI/管理后台按它寻址
[projects.agent]
type = "claudecode"
[projects.agent.options]
work_dir = "/path/to/backend"
mode = "acceptEdits"

[[projects.platforms]]
type = "feishu"
[projects.platforms.options]
app_id = "cli_xxx"
app_secret = "${FEISHU_APP_SECRET}"

[[projects]]                        # ── 第 2 个项目（新的 [[projects]]）
name = "frontend"
[projects.agent]
type = "codex"
[projects.agent.options]
work_dir = "/path/to/frontend"
mode = "plan"

[[projects.platforms]]
type = "telegram"
[projects.platforms.options]
token = "${TELEGRAM_BOT_TOKEN}"
```

**多项目之间避免重复**：
- 各 project 都要独立的 `work_dir`，不能共用同一个代码目录。
- 想让多个 project 用同一套 API 服务商（不重复填 Key）：全局定义 `[[providers]]`，
  各 project 用 `[projects.agent]` 的 `provider_refs = [...]` 按名引用，
  见 `references/providers.md`。
- `[[commands]]` / `[[aliases]]` / `[[hooks]]` / `banned_words` 是**进程级全局**的，
  对所有 project 生效；`[[projects]]` 里的 `disabled_commands` 可对单 project 裁剪。

**典型用法**：`backend` + `frontend` 各用一个 agent 与平台；或同一份配置里一个项目给
真实 IM（企微），另一个是虚拟 `type = "web"` 项目单独面向 Web 后台（见
`references/platforms.md`）。

## agent options（`[projects.agent.options]`）

以 `claudecode` 为例（多数对 codebuddy/codex 也通用）：

```toml
[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/path/to/project"
mode = "default"                # default | acceptEdits | plan | auto | bypassPermissions | dontAsk
# reasoning_effort = "high"     # low|medium|high|max（传给 claude --effort）
# allowed_tools = ["Read", "Grep", "Glob", "Bash", "Edit", "Write"]   # 预授权工具
# disallowed_tools = ["WebSearch", "WebFetch"]                        # 禁用工具
# system_prompt = "You are ..."                                        # 自定义系统提示
# model = "claude-sonnet-4-6"                                          # 指定模型

# 注入 agent 会话的环境变量（不同项目用不同模型供应商，无需改全局 settings）
# [projects.agent.options.env]
# ANTHROPIC_BASE_URL = "https://api.kimi.com/coding"
# ANTHROPIC_AUTH_TOKEN = "sk-xxx"
# ANTHROPIC_MODEL = "K2.6"

# Claude Code Router 集成（路由到不同模型提供商）
# router_url = "http://127.0.0.1:3456"
# router_api_key = "your-router-api-key"

# 当前激活 provider 与引用全局 provider
# provider = "anthropic"
# provider_refs = ["minimaxi", "dashscope"]
```

### mode 含义

| mode | 行为 |
|------|------|
| `default` | 每次工具调用都要用户确认 |
| `acceptEdits` (edit) | 文件编辑自动通过，其他仍需确认 |
| `plan` | 只规划不执行，审批后再执行 |
| `auto` | Agent 自动判断何时需要确认 |
| `bypassPermissions` (yolo) | 全部自动通过（谨慎） |
| `dontAsk` (dont-ask) | 未预授权的工具自动拒绝（安全推荐） |
