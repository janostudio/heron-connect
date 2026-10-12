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
                                    # ⚠️ 未设置时**代码默认 = 360 分钟**
                                    #   （config.example.toml 里写的"默认 30 分钟"已过时）
                                    # 也可在单个 [[projects.platforms]] 上覆盖（含虚拟 web，0=该平台永久保留）
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

> **`mode` 大小写与别名**：值会被归一化（`ToLower`），`bypassPermissions` /
> `bypasspermissions` / `yolo` / `bypass` / `dangerously-skip-permissions` 等价；
> `dontAsk` / `dont-ask` 等价。

#### ⚠️ codebuddy 的 `mode` 必须显式声明（headless 不读 settings.json）

codebuddy 适配器走 headless 路径（`-p --input-format stream-json`），**不会**回落读取
`.codebuddy/settings.json` 的 `permissions.defaultMode` —— 该字段只对交互式 TUI 生效。
所以想让 codebuddy 免确认，**必须在 config.toml 显式写**：

```toml
[projects.agent]
type = "codebuddy"
[projects.agent.options]
mode = "bypassPermissions"      # 不写就退回 default，每个工具调用都要人工确认
```

- 对外标准名是 `bypassPermissions`（`codebuddy.PermissionModes()` 暴露的 Key），
  显示名仍为 YOLO，与 claudecode 对齐。
- 此设置**不影响** `.codebuddy/settings.json` 里的 `permissions.deny`，deny 优先级最高。
- 历史坑（v1.3.16 修复）：`normalizeMode` 曾先 `ToLower` 再匹配驼峰 case，导致
  `bypassPermissions` 永远匹配不上而静默失效 —— 若你用的是旧版二进制，写对了也没用，
  先确认版本（见 `references/gotchas.md` 的部署版本校验）。

### `interruptible`：忙时发消息是打断还是排队

当某会话正在执行、用户又发来一条消息时，是否**打断当前轮**取决于 agent 能力：

| agent | `interruptible` | 行为 |
|---|---|---|
| codebuddy | **恒为 true**（常驻进程模式） | 忙时发消息 → 打断当前轮，新消息前插优先执行 |
| claudecode | 需显式配置 | 同上 |
| 其它/未实现 | false（默认） | 忙时发消息 → 排队，等当前轮结束后再处理 |

```toml
[projects.agent.options]
interruptible = true            # 显式开启（codebuddy 已默认开启，无需写）
```

> **交互语义提示（v1.3.17 起）**：打断与排队**不再由 heron-connect 发送任何提示气泡**
> （此前的「⏸️ (已中断)」和「📬 消息已收到，将在当前任务完成后处理。」已移除）——
> 内容一律由 agent CLI 自己产出，避免引擎回复与 CLI 输出重复。
> Web 端执行中会显示发送键（悬停提示「发送并打断当前回复」或「发送（排队…）」）。

### `work_dir`：相对路径按 **config.toml 所在目录** 锚定

`work_dir` 是 agent 进程的工作目录。**推荐写绝对路径**；若写相对路径（如 `"../.."`），
自 v1.3.15 起统一以 **config.toml 所在目录** 为基准锚定为绝对路径，与**启动进程的 cwd 无关**。

> 历史坑（v1.3.15 修复）：相对 `work_dir` 曾在加载阶段不做归一化，直接透传给
> `exec.Cmd.Dir`，由操作系统按**当前进程 cwd** 解析 —— 于是常驻 daemon 下看起来正常，
> 前台手动启动时却漂到祖辈目录，agent 在错误目录工作、读不到项目级
> `.codebuddy/settings.json`，`permissionMode` 从 `bypassPermissions` 回退为 `default`。
> 若你用的是旧版二进制且用了相对 `work_dir`，请改成绝对路径或升级。
> 同理，`dashboard.insights_path` / `html_path` / `reports_dir` 这些「相对于 work_dir」
> 的配置项基准也随之稳定。
