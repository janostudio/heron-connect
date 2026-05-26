# cc-connect-qhn

这是一个基于 `cc-connect` 的个人 fork 仓库，仅用于个人练手、自用和实验，不是上游官方发布，也不承诺兼容性、稳定性或长期维护支持。

原始项目见：`https://github.com/chenhg5/cc-connect`

fork 基线说明：当前仓库以 `cc-connect` 上游发布版本 `v1.3.3-beta.2` 为基础导入；本仓库内首次导入该项目的提交为 `c099ce699e44d74a9f2018244375a4ff410cd7eb`。

当前仓库核心用途仍然是把本地 AI 编码 Agent 桥接到聊天平台，例如飞书、Telegram、Discord、Slack、钉钉、企业微信、微信个人号、QQ、LINE、微博等。当前 README 只保留开发相关内容，不再保留赞助、宣传和多语言入口。

## 项目定位

- 后端服务使用 Go 实现，入口在 `cmd/cc-connect`
- Agent 适配器位于 `agent/`
- 平台适配器位于 `platform/`
- 核心编排、会话、消息渲染、权限流转位于 `core/`
- 配置解析位于 `config/`
- Web 管理界面位于 `web/`，构建后嵌入 Go 二进制
- 守护进程和系统服务相关逻辑位于 `daemon/`

## 仓库结构

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
└── docs                # 协议、接入、使用文档
```

## 开发环境要求

- Go `1.25.0` 或更高版本，版本声明见 [go.mod](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/go.mod)
- Node.js 和 npm，用于构建 `web/` 前端资源
- 如果要联调具体 Agent，还需要本地安装对应 CLI，例如 Claude Code、Codex、Gemini CLI、Cursor Agent 或 ACP 兼容 Agent

## 首次准备

先安装前端依赖：

```bash
cd web
npm install
```

如果你只做 Go 后端开发，不碰前端，也建议至少先执行一次，避免 `make build` 时因为缺依赖失败。

## 如何构建

在仓库根目录执行：

```bash
make build
```

这个命令会先构建 `web/`，再生成根目录下的 `./cc-connect` 二进制。

构建完成后可以直接检查版本：

```bash
./cc-connect --version
```

如果你当前只想调后端，不想每次都重新打包前端：

```bash
make build-noweb
```

这个命令会使用 `no_web` 标签构建，不包含嵌入式 Web UI。

## 如何本地运行

如果当前目录已有 `config.toml`：

```bash
./cc-connect -config ./config.toml
```

如果你希望直接使用默认配置位置：

```bash
./cc-connect
```

配置文件查找顺序如下：

1. `-config <path>`
2. `./config.toml`
3. `~/.cc-connect/config.toml`

常用配置参考：

- [config.example.toml](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/config.example.toml)
- [INSTALL.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/INSTALL.md)
- [docs/usage.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/docs/usage.md)

## 如何单独调试前端

如果你在修改 `web/`：

```bash
cd web
npm run dev
```

正式嵌入后端前，仍然需要回到仓库根目录执行一次：

```bash
make build
```

## 常用命令

以下命令来自 [Makefile](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/Makefile)：

- `make build`：构建前端并编译 `cc-connect`
- `make build-noweb`：跳过内嵌 Web UI，仅编译后端
- `make build-local`：构建开发版并覆盖全局 npm 安装里的包装脚本和真实二进制
- `make run`：构建后直接运行
- `make test`：基础 Go 测试
- `make test-fast`：带 `-race` 的单元测试加 smoke 测试
- `make test-full`：完整单测、smoke、regression
- `make test-release-local`：不依赖真实平台凭据的本地发布门禁
- `make lint`：执行 `golangci-lint`

## 本地验证建议

提交前至少执行：

```bash
go test ./...
```

如果改动涉及核心会话流、平台适配器或发版逻辑，建议额外执行：

```bash
make test-fast
make test-release-local
```

## 本地安装与替换

关于本地构建后如何覆盖 npm 全局安装、为什么需要同时替换包装层文件，以及 `make build-local` 的行为说明，见：

- [docs/local-dev-install.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/docs/local-dev-install.md)
## 精简构建

仓库支持按 Agent 或平台裁剪编译，示例：

```bash
make build AGENTS=claudecode PLATFORMS_INCLUDE=feishu,telegram
make build EXCLUDE=discord,dingtalk,qq,qqbot,line
```

如果你只做某一条接入链路，这种裁剪方式更适合本地调试。

## 相关文档

- [AGENTS.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/AGENTS.md)
- [CLAUDE.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/CLAUDE.md)
- [CONTRIBUTING.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/CONTRIBUTING.md)
- [docs/bridge-protocol.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/docs/bridge-protocol.md)
- [docs/management-api.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/docs/management-api.md)
- [docs/usage.md](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/docs/usage.md)

## 许可证

仓库和 npm 包当前都声明为 MIT，npm 元数据见 [npm/package.json](/Users/jahweijiang/Documents/agent-qhn/projects/cc-connect-qhn/npm/package.json)。
