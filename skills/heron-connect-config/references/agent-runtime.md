# Agent 运行时指南（会话内反向调用 heron-connect）

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。
> 权威来源：`core/engine.go` 的 env 注入、`cmd/heron-connect/` 各子命令实现。

本文件面向**在 heron-connect 会话内运行的 Agent**：说明它被注入了哪些环境变量、如何中途主动给用户发消息、以及有哪些反向调用能力。

---

## 一、为什么 Agent 需要这个

heron-connect 把 CLI Agent 接到 IM 平台上。默认情况下 Agent 是**叶子节点**——只能被动接收用户消息、在被调用时返回一次回复。

但实际上 Agent **可以主动说话**：heron-connect 在启动 agent 子进程时注入了一组环境变量，并保证 `heron-connect` 二进制在 `PATH` 里。因此 Agent 只要执行 shell 命令，就能反过来驱动 heron-connect 把消息推给用户。

```
用户 ──消息──▶ heron-connect ──prompt──▶ CLI Agent
                    ▲                        │
                    │                        │ Agent 主动执行
                    │                        │ heron-connect send
                    └────独立消息──────────────┘
                           ▲
                    （不挂在原消息下，是新气泡）
```

---

## 二、注入的环境变量

这些变量由 heron-connect 在**每个会话启动时**注入到 agent 子进程（`core/engine.go`；relay 场景另见 `core/engine_relay.go`）。

| 变量 | 含义 | 示例值 |
|------|------|--------|
| `HERON_PROJECT` | 当前 project 名（`[[projects]].name`） | `auto-bugfix` |
| `HERON_SESSION_KEY` | 当前会话 key，格式 `<platform>:<chatID>:<userID>` | `wecom:T1226...:zhangsan` |
| `PATH` | 追加了 heron-connect 二进制所在目录，因此可直接敲 `heron-connect` | — |

### 关于 `HERON_SESSION_KEY`

平台与会话识别的关键。格式为 `<platform>:<chatID>:<userID>`：

| 平台 | 示例 |
|------|------|
| wecom | `wecom:<chatid>:<userid>`（单聊时 chatid 等于 userid） |
| 飞书 | `feishu:<chat_id>:<user_id>` |
| Telegram | `telegram:<chat_id>:<user_id>` |
| Discord | `discord:<channel_id>:<user_id>` |
| relay 场景 | `relay:<from_project>:<chat_id>` |

heron-connect 用它反查「该把消息发给谁」——无需 `--project` / `--session-key`，命令会自动读取。

> ⚠️ **不要硬编码 session key**。它随会话变化，永远从环境变量读或省略让 CLI 自取。

### 不属于本类的变量

以下是 heron-connect **自身**读的运维变量，不是注入给 agent 的，Agent 一般不需要关心：

`CC_LOG_FILE` / `CC_LOG_MAX_SIZE` / `CC_LOG_RETENTION_DAYS`（日志）、`CC_CONFIG_PATH`（doctor）、`CC_HOOK_*`（hook 事件上下文）。

---

## 三、主动给用户发消息

### 3.1 基本用法

```bash
# 单行
heron-connect send -m "已定位到问题，正在修复，稍后给结论"

# 多行 / 含特殊字符（推荐 --stdin，避免转义问题）
heron-connect send --stdin <<'EOF'
第一阶段完成：
- 已拉取最新代码
- 单元测试通过
接下来跑集成测试
EOF

# 回传图片 / 文件
heron-connect send -m "图表生成了" --image /tmp/chart.png
heron-connect send --file /tmp/report.pdf
```

每次调用都会**新增一条独立消息**给用户——不是替换、不是追加到原回复，而是一个新的消息气泡。

### 3.2 与「正常回复」的关系

| 路径 | 何时用 | 投递方式 |
|------|--------|---------|
| **正常回复**（直接输出文本） | 最终答案 | heron-connect 自动投递，挂在用户消息下 |
| **`heron-connect send`** | 执行中途想主动通知 | 独立新消息 |

两者可以并存。但注意一个去重机制：heron-connect 会记住你通过 `send` 发出的最后一条文本（`core/engine_reply.go` 的 `state.sideText`），如果**最终回复与它完全相同**，最终回复会被抑制。所以：

- ✅ 中途说「正在跑测试」，最终说「测试通过，共 12 个用例」——两条都送达
- ❌ 中途说「测试通过」，最终回复还是「测试通过」——最终回复被吃掉

**最终答案走正常回复，不要用 send 发。**

### 3.3 什么时候发

主动通知的价值在于**用户等待期间有反馈**。推荐场景：

- 任务预计耗时较长（超过一两分钟），先回一句确认已收到并开始处理
- 完成了一个明显的阶段性里程碑，且用户会关心
- 后台/长时任务（编译、批量测试、大文件处理）结束
- 发现了需要用户立刻知晓的异常

**不要做的事**：

- 不要每个工具调用都发一条——那是噪音，用户会关掉通知
- 不要发无信息量的"正在处理中"（heron-connect 已有 `instant_reply` 做这件事）
- 遵守平台频率限制，例如**企业微信 30 条/分钟、1000 条/小时**（每个会话）

### 3.4 可选参数

```bash
heron-connect send -m "..." -p <project> -s <session-key>   # 显式指定（一般不需要）
heron-connect send -m "..." --data-dir <path>               # 非默认数据目录时
heron-connect send --help                                    # 完整用法
```

正常情况下 `-p` / `-s` **都不需要**——CLI 会自动读 `HERON_PROJECT` / `HERON_SESSION_KEY`。

### 3.5 前置条件

- Agent 必须能执行 shell（即所用 CLI 允许 Bash 工具）
- heron-connect **实例正在运行**（命令通过 Unix socket `~/.heron-connect/run/api.sock` 通信）。若未运行会报 `heron-connect is not running (socket not found: ...)`
- 若 CLI 每次跑命令都弹权限确认，需把 `mode` 调成 `auto` / `bypassPermissions`(yolo) 之类，否则体验会被打断。这与 send 的能力无关，只是体验问题

---

## 四、其他反向调用能力

同一个 env 注入机制也支撑以下命令，用法见 `cli.md`：

| 命令 | 用途 | 读取 env |
|------|------|---------|
| `heron-connect send` | 主动发消息 / 回传附件 | ✅ |
| `heron-connect cron add\|list\|edit\|info\|del` | 让用户用自然语言设置定时任务 | ✅ |
| `heron-connect relay send --to <project> "<msg>"` | 与另一个 bot 通信 | ✅ |
| `heron-connect agent-sid` | 查询当前会话对应的 agent session id（用于 `--resume`） | ✅ |

---

## 五、快速自检

在会话内执行：

```bash
echo "project=$HERON_PROJECT key=$HERON_SESSION_KEY"
which heron-connect
heron-connect send -m "自检：主动通知链路正常"
```

- 前两条应分别打印 project 名、session key 和二进制路径
- 第三条应让用户在 IM 里收到一条**独立的新消息**

若 `HERON_*` 为空，说明该 agent 类型未实现 `SessionEnvInjector`（见 `core/interfaces.go`），或会话不是由 heron-connect 启动的。

---

## 六、相关文件

| 关注点 | 位置 |
|--------|------|
| env 注入（普通会话） | `core/engine.go` 的 `SetSessionEnv` 调用处 |
| env 注入（relay） | `core/engine_relay.go` |
| 注入接口定义 | `core/interfaces.go` 的 `SessionEnvInjector` |
| Agent 系统提示词 | `core/interfaces.go` 的 `AgentSystemPrompt()` |
| send 实现 | `cmd/heron-connect/send.go` |
| socket API | `api/local_api.go` 的 `handleSend` |
| 引擎侧发送 | `core/engine_reply.go` 的 `SendToSessionWithAttachments` |
