# cc-connect

这是一个面向开发和自部署的 `cc-connect` 仓库，核心用途是把本地 AI 编码 Agent 桥接到聊天平台，例如飞书、Telegram、Discord、Slack、钉钉、企业微信、微信个人号、QQ、LINE、微博等。当前 README 只保留开发相关内容，不再保留赞助、宣传和多语言入口。

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

## 怎么替换本地已经安装的 cc-connect

先确认你当前系统里 `cc-connect` 命令来自哪里：

```bash
which cc-connect
```

然后根据安装方式替换。

### 场景 1：你本来就是手动安装到 PATH

例如输出是 `/usr/local/bin/cc-connect` 或 `/opt/homebrew/bin/cc-connect`，可以直接用你刚构建出来的二进制覆盖：

```bash
sudo install -m 755 ./cc-connect "$(which cc-connect)"
```

替换后验证：

```bash
cc-connect --version
```

推荐直接使用：

```bash
make build-local
```

这个目标现在会覆盖两类内容：

1. npm 包目录里的包装层文件：`package.json`、`run.js`、`install.js`、`README.md`
2. 真正执行的二进制：`$(npm root -g)/cc-connect/bin/cc-connect`

这样做的原因是，单独替换二进制还不够。npm 全局命令实际先经过包装脚本，而包装脚本会读取 npm 包版本；如果仓库构建出来的版本号和已发布 npm 版本不一致，它会误判为“过期”，然后尝试重新下载安装官方版本。

`make build-local` 会把本地 npm 包元数据和二进制一起替换，避免这个回退行为。

注意，`which cc-connect` 显示的通常是 npm 放到 PATH 里的入口脚本，例如你当前的：

```bash
/Users/jahweijiang/.nvm/versions/node/v24.11.0/bin/cc-connect
```

这个路径本身是一个软链接，实际指向全局 npm 包目录里的 `run.js`；真正需要覆盖的是该包目录中的包装文件和 `bin/cc-connect`。

### 场景 2：你之前是通过 npm 全局安装

`npm install -g cc-connect` 实际上会在全局包目录里放一个包装脚本，真正执行的二进制通常在全局 `node_modules/cc-connect/bin/cc-connect`。

可以这样替换：

```bash
NPM_CC_DIR="$(npm root -g)/cc-connect"
sudo mkdir -p "$NPM_CC_DIR/bin"
sudo install -m 755 ./cc-connect "$NPM_CC_DIR/bin/cc-connect"
```

然后检查：

```bash
cc-connect --version
```

如果你不想继续保留 npm 包装层，更直接的做法是先卸载 npm 全局包，再把你自己编译的二进制放到 PATH：

```bash
npm uninstall -g cc-connect
sudo install -m 755 ./cc-connect /usr/local/bin/cc-connect
```

### 场景 3：你想临时优先使用当前仓库里编出来的版本

不替换系统文件，直接在当前 shell 里把仓库根目录放到 PATH 前面：

```bash
export PATH="$PWD:$PATH"
cc-connect --version
```

这种方式适合临时调试，不会改动系统安装。

## 替换前后的注意事项

- 如果 `cc-connect` 正在运行，先停掉旧进程再替换，避免覆盖后仍在跑旧版本
- 如果你用的是守护进程或系统服务，替换二进制后需要重启对应服务
- 替换完成后优先执行 `cc-connect --version` 和一遍最小启动验证

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
