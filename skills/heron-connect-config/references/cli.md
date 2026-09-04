# 顶层 CLI 命令参考

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。
> 权威来源：`cmd/heron-connect/main.go` 的 `main()` switch + 各 `runXxx` 实现。

`heron-connect` 除了无参启动（见 quickstart.md），还有一批子命令。运行
`heron-connect --help` / `heron-connect <subcmd> --help` 可看最新用法。

## 启动 flags（`heron-connect [flags]`）

| flag | 说明 |
|------|------|
| `--config <path>` | 指定配置文件；缺省按 `./config.toml` → `~/.heron-connect/config.toml` 查找 |
| `--force` | 先 kill 掉使用同一 config 的现有实例再启动（防重复进程；单实例锁见 gotchas） |
| `--version` | 打印版本 + commit + 构建时间 |
| `--observe` / `--observe-channel <id>` | 观察原生终端会话并转发到 Slack（等价 `[projects.observe]`，见 advanced.md） |

## 子命令速查

### 配置类

| 命令 | 说明 |
|------|------|
| `heron-connect config example` | 打印完整注释示例 config（`config-example` 是等价旧写法） |
| `heron-connect config format [path]` | 格式化 config 文件（`fmt` 等价） |
| `heron-connect config path` | 打印解析后实际使用的 config 路径 |

### 升级类

| 命令 | 说明 |
|------|------|
| `heron-connect check-update` | 检查是否有新版本 |
| `heron-connect update` | 升级二进制；`--pre` 拉 beta 版 |

### Provider（见 providers.md）

| 命令 | 说明 |
|------|------|
| `heron-connect provider list` | 列出 provider |
| `heron-connect provider add ...` | 添加 provider |
| `heron-connect provider remove <name>` | 删除 |
| `heron-connect provider import ...` | 从 cc-switch 等来源导入 |

### 发送消息（主动推送到会话）

```bash
heron-connect send -m "简短消息"                    # 单行
heron-connect send --stdin <<'EOF'                  # 多行/长消息（特殊字符安全）
...你的内容...
EOF
heron-connect send -m "看图" --image /path/x.png     # 回传图片
heron-connect send -m "附件" --file /path/x.pdf      # 回传文件
# 选项：-p/--project、-s/--session-key（缺省读 CC_PROJECT / CC_SESSION_KEY 环境变量）
```

> 图片/文件回传受 `attachment_send` 全局开关控制（off 则禁）。

### 会话管理

| 命令 | 说明 |
|------|------|
| `heron-connect sessions` | 交互式 TUI 浏览/切换会话 |
| `heron-connect sessions list` | 列出会话 |
| `heron-connect sessions show <project:session>` 或 `<#序号>` | 查看某会话详情 |
| `heron-connect agent-sid` | 打印当前会话对应的 agent 会话 ID（用于 agent 的 `--resume`） |

### 定时任务 / 中继（分别见 advanced.md 的 cron、relay）

```bash
heron-connect cron add|list|edit|info|del ...
heron-connect relay send ...      # bot 间中继（读 CC_PROJECT / CC_SESSION_KEY）
```

### 平台引导（见 platforms.md）

```bash
heron-connect feishu setup|new|bind --project <name>
heron-connect weixin setup|new|bind --project <name>
```

### 诊断

| 命令 | 说明 |
|------|------|
| `heron-connect doctor` | 运行诊断；子命令 `doctor user-isolation` 审计 `run_as_user` 的权限隔离（preflight + 探测 + JSON 报告） |

### 部署 / Web（分别见 deployment.md、advanced.md）

```bash
heron-connect daemon install|start|stop|restart|status|logs|uninstall
heron-connect web                # 开启 Web 管理后台（详见 advanced.md 的 web 段）
heron-connect web --no-browser   # 只打印 URL+token，不自动开浏览器
```
