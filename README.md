# cc-connect-qhn

这是一个基于 [`cc-connect`](https://github.com/chenhg5/cc-connect) 的个人 fork，用于个人练手、自用和实验。不是官方发布，不承诺兼容性、稳定性或长期维护支持。

fork 基线：上游 `v1.3.3-beta.2`，首次导入提交为 `c099ce699e44d74a9f2018244375a4ff410cd7eb`。

---

## 这是什么

cc-connect-qhn 是一个桥接服务，把本地 AI 编码 Agent 连接到聊天平台，让你在飞书、Telegram、Discord、Slack 等消息应用里直接和 AI 编程助手对话。

```
你发送消息
  → 聊天平台（飞书 / Telegram / Discord / Slack 等）
    → cc-connect-qhn（桥接层）
      → 本地 AI Agent（Claude Code / Codex / Gemini CLI 等）
        → 处理代码任务，结果回传到聊天
```

---

## 与上游 cc-connect 的区别

| 方面 | cc-connect（官方） | cc-connect-qhn（本仓库） |
|------|-------------------|------------------------|
| 定位 | 正式发布，面向大众 | 个人 fork，自用实验 |
| 稳定性 | 承诺稳定性和兼容性 | 不承诺，可能有破坏性改动 |
| 安装命令 | `npm install -g cc-connect` | `npm install -g @qinghuangniao/cc-connect-qhn` |
| 二进制名称 | `cc-connect` | `cc-connect-qhn` |
| npm 包名 | `cc-connect` | `@qinghuangniao/cc-connect-qhn` |
| 功能范围 | 以上游功能为准 | 包含个人实验特性，可能超前也可能缺失某些功能 |
| 维护支持 | 有 issue 跟踪和发布节奏 | 无维护承诺，不建议生产使用 |
| 文档 / 宣传 | 多语言 README，含赞助信息 | 精简为开发相关内容 |

如果你想要稳定使用，请用官方 [cc-connect](https://github.com/chenhg5/cc-connect)。

---

## 支持的 Agent 和平台

**AI Agent（任选其一安装）**

| Agent | 安装方式 |
|-------|---------|
| Claude Code | `npm install -g @anthropic-ai/claude-code` |
| Codex | `npm install -g @openai/codex` |
| Gemini CLI | `npm install -g @google/gemini-cli` |
| Cursor Agent | 参考 [官方文档](https://docs.cursor.com/agent) |
| OpenCode | 参考 [官方文档](https://github.com/opencode-ai/opencode) |
| Qoder CLI | `curl -fsSL https://qoder.com/install \| bash` |
| iFlow CLI | `npm install -g @iflow-ai/iflow-cli` |
| ACP 兼容 Agent | 兼容任意实现 ACP 协议的 Agent |

**聊天平台（可同时配置多个）**

| 平台 | 连接方式 | 需要公网 IP |
|------|---------|-----------|
| 飞书 / Lark | WebSocket 长连接 | 否 |
| 钉钉 | Stream 模式（WebSocket） | 否 |
| Telegram | Long Polling | 否 |
| Slack | Socket Mode（WebSocket） | 否 |
| Discord | Gateway WebSocket | 否 |
| QQ（via NapCat/OneBot） | WebSocket | 否 |
| 微信个人号（via ilink） | Long Polling | 否 |
| 企业微信（WeCom） | HTTP Webhook | **是** |
| LINE | HTTP Webhook | **是** |
| 微博 | HTTP Webhook | 参考文档 |

---

## 快速上手

### 1. 安装

```bash
npm install -g @qinghuangniao/cc-connect-qhn
```

安装后可用命令为 `cc-connect-qhn`。

或者从源码构建（见[开发相关](#开发相关)一节）。

### 2. 安装 AI Agent

以 Claude Code 为例：

```bash
npm install -g @anthropic-ai/claude-code
claude --version  # 验证安装
```

### 3. 创建配置文件

运行一次会自动生成配置模板：

```bash
cc-connect-qhn
```

配置文件查找顺序：

1. `-config <path>` 命令行参数
2. `./config.toml`（当前目录）
3. `~/.cc-connect-qhn/config.toml`（全局，推荐）

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
mode = "default"   # default | acceptEdits | auto | plan | bypassPermissions(yolo)

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_xxxxxxxxxxxx"
app_secret = "xxxxxxxxxxxxxxxxxxxxxxxx"
```

完整配置示例见 [config.example.toml](config.example.toml)，各平台接入指南见 [INSTALL.md](INSTALL.md)。

### 4. 启动服务

```bash
cc-connect-qhn -config ./config.toml
```

正常启动时日志类似：

```
level=INFO msg="platform started" project=my-project platform=feishu
level=INFO msg="engine started" project=my-project agent=claudecode platforms=1
level=INFO msg="cc-connect is running" projects=1
```

> **注意**：如果你在 Claude Code 会话内运行，需要先 `unset CLAUDECODE`，否则 Claude Code 会拒绝作为子进程启动。建议在独立终端窗口运行。

### 5. 使用聊天命令

启动后向你的 Bot 发送消息，可用斜杠命令：

| 命令 | 说明 |
|------|------|
| `/new [name]` | 开始新会话 |
| `/list` | 查看所有会话 |
| `/switch <id>` | 切换到指定会话 |
| `/current` | 查看当前会话信息 |
| `/history [n]` | 查看最近 n 条消息 |
| `/mode [name]` | 查看或切换权限模式 |
| `/provider [...]` | 管理 API Provider |
| `/model [switch <alias>]` | 切换模型 |
| `/dir [path]` | 切换 Agent 工作目录 |
| `/stop` | 停止当前执行 |
| `/quiet` | 切换思考过程显示 |
| `/help` | 查看帮助 |

Agent 请求工具权限时回复 `allow` / `deny` / `allow all`（中文 `允许` / `拒绝` / `允许所有` 同样有效）。

---

## 主要功能

### Web 管理界面

内嵌 React 前端，提供项目管理、会话管理、定时任务编辑、在线聊天等功能：

```bash
cc-connect-qhn web     # 打开管理界面（不启动服务）
cc-connect-qhn        # 启动服务
```

手动配置：

```toml
[management]
enabled = true
port = 9820
token = "your-secret-token"
```

访问 `http://localhost:9820`，用 token 登录。

### 多项目支持

一个进程管理多个项目，每个项目有独立的 Agent、工作目录和平台：

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

自然语言描述定时任务，Claude Code 自动创建：

> "每天早上 6 点汇总 GitHub trending"

或手动创建：

```bash
cc-connect-qhn cron add --cron "0 6 * * *" --prompt "汇总 GitHub trending" --desc "每日 Trending"
```

聊天命令：`/cron add 0 6 * * * 汇总 GitHub trending`

### 语音消息（STT / TTS）

- **STT**：发送语音消息，自动转文字后交给 Agent（支持飞书、企业微信、Telegram、LINE、Discord、Slack）
- **TTS**：Agent 回复自动合成为语音（支持飞书）

需要 `ffmpeg` 和 OpenAI/Groq/通义千问 API Key，参考 [docs/usage.md](docs/usage.md)。

### Agent 隔离运行（`run_as_user`）

以不同 Unix 用户运行 Agent，通过系统文件权限实现隔离：

```toml
[[projects]]
name = "sandboxed"
run_as_user = "agent-user"
```

参考 [docs/usage.md](docs/usage.md)。

### Bridge 外部接入（Beta）

WebSocket + REST 接口，供自定义 UI 或脚本接入：

```toml
[bridge]
enabled = true
port = 9810
token = "your-bridge-secret"
```

协议文档见 [docs/bridge-protocol.md](docs/bridge-protocol.md)。

### 后台守护进程

注册为系统服务（Linux systemd / macOS launchd / Windows Task Scheduler）：

```bash
cc-connect-qhn daemon install --config ~/.cc-connect-qhn/config.toml
cc-connect-qhn daemon start
cc-connect-qhn daemon logs -f
```

---

## 开发相关

### 环境要求

- Go `1.25.0` 或更高，见 [go.mod](go.mod)
- Node.js 和 npm（用于构建前端）

### 首次准备

```bash
cd web
npm install
```

### 构建

```bash
make build          # 构建前端 + 编译二进制（输出 ./cc-connect）
make build-noweb    # 跳过前端，仅编译后端（加 no_web tag）
make build-local    # 构建并覆盖 npm 全局安装
make run            # 构建后直接运行
```

### 精简构建

按需裁剪编译，减少二进制体积：

```bash
# 只编译 claudecode agent + feishu/telegram 平台
make build AGENTS=claudecode PLATFORMS_INCLUDE=feishu,telegram

# 排除不需要的平台
make build EXCLUDE=discord,dingtalk,qq,qqbot,line
```

可用 build tag：`no_acp`, `no_claudecode`, `no_codex`, `no_cursor`, `no_gemini`, `no_iflow`, `no_opencode`, `no_qoder`, `no_feishu`, `no_telegram`, `no_discord`, `no_slack`, `no_dingtalk`, `no_wecom`, `no_weixin`, `no_qq`, `no_qqbot`, `no_line`, `no_weibo`

### 本地运行

```bash
./cc-connect -config ./config.toml
./cc-connect --version
```

### 前端开发

```bash
cd web
npm run dev    # 本地热更新
# 修改完毕后回根目录重新 make build 嵌入
```

### 测试

```bash
make test                  # 基础 Go 测试
make test-fast             # 单元测试 + race detector + smoke
make test-full             # 完整单测 + smoke + regression
make test-release-local    # 本地发布门禁（不需要真实平台凭据）
make lint                  # golangci-lint
```

提交前至少运行：

```bash
go test ./...
```

如果改动涉及核心会话流、平台适配器或发版逻辑：

```bash
make test-fast
make test-release-local
```

---

## 项目结构

```text
.
├── cmd/cc-connect      # CLI 入口和各类子命令
├── core                # engine、接口、session、hooks、i18n、渲染
├── config              # TOML 配置加载与校验
├── agent               # Claude Code、Codex、Gemini、Cursor、ACP 等
├── platform            # 飞书、Telegram、Discord、Slack、QQ、微信等
├── daemon              # launchd / systemd / Windows 服务支持
├── tests               # e2e 和 release-local 测试
├── web                 # React + Vite 管理后台
├── npm                 # npm 包发布配置
└── docs                # 协议、接入、使用文档
```

---

## 相关文档

- [INSTALL.md](INSTALL.md) — 安装与各平台接入指南
- [docs/usage.md](docs/usage.md) — 完整功能使用说明
- [config.example.toml](config.example.toml) — 配置文件完整示例
- [AGENTS.md](AGENTS.md) — 开发指南（架构、规范、如何添加 Agent/平台）
- [docs/bridge-protocol.md](docs/bridge-protocol.md) — Bridge 协议文档
- [docs/management-api.md](docs/management-api.md) — Management API 文档
- [docs/local-dev-install.md](docs/local-dev-install.md) — 本地构建覆盖 npm 安装说明

---

## 许可证

MIT，见 [npm/package.json](npm/package.json)。
