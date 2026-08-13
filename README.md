# heron-connect

一个桥接服务，把本地 AI 编码 Agent 连接到聊天平台，让你在飞书、Telegram、Discord、Slack 等消息应用里直接和 AI 编程助手对话。

```
你发送消息
  → 聊天平台（飞书 / Telegram / Discord / Slack 等）
    → heron-connect（桥接层）
      → 本地 AI Agent（Claude Code / Codex / Gemini CLI 等）
        → 处理代码任务，结果回传到聊天
```

---

## 快速上手

### 1. 安装

```bash
npm install -g @qinghuangniao/heron-connect
```

安装后可用命令为 `heron-connect`。

### 2. 安装 AI Agent

以 Claude Code 为例：

```bash
npm install -g @anthropic-ai/claude-code
claude --version
```

### 3. 创建配置文件

```bash
heron-connect
```

首次运行会自动生成 `~/.heron-connect/config.toml`，按提示编辑填入 Agent 和平台凭据。

配置文件查找顺序：`--config <path>` → `./config.toml` → `~/.heron-connect/config.toml`

最简配置示例（Claude Code + 飞书）：

```toml
[log]
level = "info"

[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"

[projects.agent.options]
work_dir = "/absolute/path/to/your/project"
mode = "default"

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_xxxxxxxxxxxx"
app_secret = "xxxxxxxxxxxxxxxxxxxxxxxx"
```

完整配置见 [config.example.toml](config.example.toml)，平台接入见 [INSTALL.md](INSTALL.md)。

### 4. 启动

```bash
heron-connect --config ./config.toml
```

正常启动日志：

```
level=INFO msg="heron-connect is running" projects=1
```

### 5. 聊天命令

向 Bot 发送消息即可对话，可用斜杠命令：

| 命令 | 说明 |
|------|------|
| `/new [name]` | 开始新会话 |
| `/list` | 查看所有会话 |
| `/switch <id>` | 切换到指定会话 |
| `/history [n]` | 查看最近 n 条消息 |
| `/mode [name]` | 查看或切换权限模式 |
| `/stop` | 停止当前执行 |
| `/help` | 查看帮助 |

Agent 请求工具权限时回复 `allow` / `deny` / `allow all`。

---

## 支持的 Agent 和平台

**AI Agent**

| Agent | 安装方式 |
|-------|---------|
| Claude Code | `npm install -g @anthropic-ai/claude-code` |
| Codex | `npm install -g @openai/codex` |
| Gemini CLI | `npm install -g @google/gemini-cli` |
| Cursor Agent | [官方文档](https://docs.cursor.com/agent) |
| OpenCode | [官方文档](https://github.com/opencode-ai/opencode) |
| Qoder CLI | `curl -fsSL https://qoder.com/install \| bash` |
| iFlow CLI | `npm install -g @iflow-ai/iflow-cli` |
| ACP 兼容 Agent | 任意 ACP 协议实现 |

**聊天平台**

| 平台 | 连接方式 | 需要公网 IP |
|------|---------|-----------|
| 飞书 / Lark | WebSocket | 否 |
| 钉钉 | Stream 模式 | 否 |
| Telegram | Long Polling | 否 |
| Slack | Socket Mode | 否 |
| Discord | Gateway WebSocket | 否 |
| QQ（NapCat/OneBot） | WebSocket | 否 |
| 微信个人号（ilink） | Long Polling | 否 |
| 企业微信（WeCom） | HTTP Webhook | **是** |
| LINE | HTTP Webhook | **是** |

---

## 主要功能

### Web 管理界面

内嵌 React 前端，提供项目管理、会话管理、定时任务编辑、在线聊天：

```bash
heron-connect web     # 打开管理界面
```

配置：

```toml
[management]
enabled = true
port = 9820
token = "your-secret-token"
```

访问 `http://localhost:9820`，用 token 登录。

### 多项目支持

一个进程管理多个项目，各自独立 Agent 和平台：

```toml
[[projects]]
name = "backend"
[projects.agent]
type = "claudecode"
[projects.agent.options]
work_dir = "/path/to/backend"

[[projects]]
name = "frontend"
[projects.agent]
type = "codex"
[projects.agent.options]
work_dir = "/path/to/frontend"
```

### 定时任务

```bash
heron-connect cron add --cron "0 6 * * *" --prompt "汇总 GitHub trending" --desc "每日 Trending"
```

或直接在聊天中告诉 Agent："每天早上 6 点汇总 GitHub trending"。

### 后台守护进程

```bash
heron-connect daemon install --config ~/.heron-connect/config.toml
heron-connect daemon start
heron-connect daemon logs -f
```

支持 Linux systemd / macOS launchd / Windows Task Scheduler。

### 语音消息

- STT：发送语音自动转文字后交给 Agent
- TTS：Agent 回复自动合成为语音（飞书）

需要 `ffmpeg` 和 OpenAI/Groq API Key，见 [docs/usage.md](docs/usage.md)。

### Agent 隔离运行

以不同 Unix 用户运行 Agent，通过文件权限隔离：

```toml
run_as_user = "agent-user"
```

### Bridge 外部接入（Beta）

WebSocket + REST 接口，供自定义 UI 或脚本接入：

```toml
[bridge]
enabled = true
port = 9810
token = "your-bridge-secret"
```

---

## 开发

### 环境要求

- Go 1.25+，见 [go.mod](go.mod)
- Node.js + npm（构建前端）

### 构建

```bash
cd web && npm install       # 首次
make build                  # 构建前端 + 编译二进制
make build-noweb            # 跳过前端，仅编译
make run                    # 构建后直接运行
```

精简构建（按需裁剪，减小体积）：

```bash
make build AGENTS=claudecode PLATFORMS_INCLUDE=feishu,telegram
```

### 测试

```bash
go test ./...
make test-fast
```

### 项目结构

```text
.
├── cmd/heron-connect      # CLI 入口和子命令
├── core                # engine、session、hooks、i18n
├── config              # TOML 配置加载
├── agent               # Claude Code、Codex、Gemini、ACP 等
├── platform            # 飞书、Telegram、Discord、Slack、QQ、微信等
├── daemon              # launchd / systemd / Windows 服务
├── tests               # e2e 和 release-local 测试
├── web                 # React + Vite 管理后台
├── npm                 # npm 包发布配置
└── docs                # 协议、接入、使用文档
```

---

## 相关文档

- [INSTALL.md](INSTALL.md) — 安装与平台接入
- [docs/usage.md](docs/usage.md) — 完整功能说明
- [docs/session-isolation.zh-CN.md](docs/session-isolation.zh-CN.md) — IM 群聊会话隔离与共享规则
- [config.example.toml](config.example.toml) — 配置完整示例
- [AGENTS.md](AGENTS.md) — 开发指南
- [docs/bridge-protocol.md](docs/bridge-protocol.md) — Bridge 协议
- [docs/management-api.md](docs/management-api.md) — Management API

---

## 许可证

MIT
