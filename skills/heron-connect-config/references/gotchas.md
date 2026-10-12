# 常见坑与排查

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

## 常见坑

1. **TOML 数组 vs 表**：多个 provider / 多个 platform / 多个 project 用
   `[[...]]`（数组），单个配置对象用 `[projects.agent]` 这类表。
   `[projects.agent.options]` 这种带后缀的是**表**，不是数组。
2. **字符串值全部支持 `${VAR}` 替换**——永远优先用环境变量存密钥，别明文。
3. **`work_dir` 推荐绝对路径**；写相对路径时自 v1.3.15 起按 **config.toml 所在目录**
   锚定（不再是进程 cwd）。旧版二进制相对路径会漂移，见 `references/project-agent.md`。
4. **允许的用户**：`allow_from`/`admin_from` 用的是 IM 侧 User ID，`/whoami` 查。
5. **流式安全余量**：企业微信等平台流式输出阈值要**低于物理上限**，预留展示余量。
6. **会话串号**：多 Web 客户端共用同一 `session_key` 会导致 agent_session 绑定被覆盖
   进而 mismatch → 回收 → kill → 空响应；每连接用独立 key。
7. **`wecom` / `line` 需要公网 IP**；其余大多走长连接无需公网。
8. **改动配置后重启生效**：`heron-connect --config ./config.toml`，或
   `heron-connect daemon restart`。
9. **codebuddy 的 `mode` 必须显式写**：headless 路径不读 `.codebuddy/settings.json`
   的 `permissions.defaultMode`，不写 config 就退回 `default`（每个工具都要确认）。
   见 `references/project-agent.md`。
10. **验证**：`heron-connect doctor`（二进制/认证/平台/系统/依赖/网络），`heron-connect --help`
    查看子命令。所有字段以仓库根 `config.example.toml` 为权威。

---

# 故障排查手册

## 0. 第一步：确认跑的是哪个版本

**排查任何"改了没用 / 行为对不上文档"的问题前，先做这一步。** 最常见的原因就是
部署机跑的二进制版本落后于你看的代码。

```bash
which -a heron-connect              # 可能有多份，看清实际执行的是哪一个
heron-connect --version             # 确认版本
```

- 启动日志里也会打印：`msg="heron-connect is running" ... version=vX.Y.Z`。
- ⚠️ **启动脚本走 PATH 的坑**：若后台脚本里写的是 `BIN_PATH="heron-connect"`（而非绝对路径），
  实际执行的是 **PATH 里全局安装的那份**，跟你本地仓库的代码无关。
  典型症状：本地已修 v1.3.17，线上还复现 v1.3.16 的行为。
- 升级办法：装新版 → 重启（`./start-bg.sh` 或 `heron-connect daemon restart`）。
  纯前台跑的直接 `heron-connect --config <path>` 重启即可。

## 1. 日志怎么读

- 格式：`time=... level=INFO msg="..." key=value key2=value2`（Go `log/slog` TextHandler）。
- 路径：`[log].file`（相对 config 目录）；默认 `~/.heron-connect/logs/heron-connect.log`。
- 轮转：按天归档 + 按大小轮转，归档名 `<stem>-YYYY-MM-DD[-seq].log`；
  按 `retention_days`（默认 7）清理。
- `[log].level`：`debug|info(默认)|warn|error`。
- 日志分段/轮转：日志是**按天 + 按大小**双双切分的，所以"某天的日志"可能是多个文件
  （`heron-connect.log` + `heron-connect-YYYY-MM-DD*.log`）。排查跨时段问题时**别只看
  当前那个文件**，`ls -lat logs/` 把相关日期的都拉上，否则会误判成"日志断了/没记录"。

**按会话追踪一次请求**（最常用）：

```bash
# 盯某个会话/用户的所有关键节点
grep -E "message received|processing message|session spawned|turn complete" \
     heron-connect.log | grep "T12260048A"

# 一次事件前因后果：看时间窗口内全部日志（去掉噪声）
awk '$0 >= "time=2026-10-12T16:07" && $0 <= "time=2026-10-12T16:41"' heron-connect.log \
  | grep -v "cron: \|connection lost\|reconnecting\|subscribed\|new connection\|adapter"
```

### 关键日志关键字速查

| 关键字 | 含义 | 位置 |
|---|---|---|
| `message received` | 消息进入 engine（带 platform/msg_id/session/content_len） | `core/engine.go` |
| `message admission` | 准入决策（stage/action/reason，如 observe_only/drop） | `core/message_pipeline.go` |
| `processing message` | 已拿到会话锁，即将起 goroutine 处理 | `core/engine.go` |
| `session spawned` | agent 会话已创建（is_resume/elapsed） | `core/engine.go` |
| `turn complete` | 一轮完成（tools/response_len/turn_duration/tokens/silent） | `core/engine_turn.go` |
| `slow agent first event` | 首个事件 >15s（agent 启动/模型慢） | `core/engine_turn.go` |
| `slow agent send` | `agent.Send` >2s | `core/engine.go` |
| `message queued for busy session` | 会话忙，消息入队（`queue_depth`） | `core/engine.go` |
| `interruptible: interrupting running turn` | 打断当前轮、新消息前插优先跑 | `core/engine.go` |
| `processing queued messages` | **队列真正开始被消费**（有它=排队消息在跑） | `core/engine_turn.go` |
| `agent session idle timeout` | 事件间隔超时 → 杀会话（`idle_timeout_mins`，未配时默认 **2h**；`0`=禁用） | `core/engine_turn.go` |
| `permission request` / `permission resolved` | 权限请求与解决 | `core/engine_turn.go` |
| `dropping stale event from a previous turn` | epoch 校验丢弃上一轮的陈旧事件 | `core/engine_turn.go` |
| `session auto-titled` | 会话自动生成标题 | `core/engine_turn.go` |
| `session: loaded from disk` | 启动时从磁盘恢复会话 | `core/session.go` |
| `bridge: adapter registered` / `disconnected` | Web/bridge 适配器上下线 | `bridge/bridge.go` |
| `bridge: message received` | Web/bridge 入站消息 | `bridge/bridge.go` |
| `wecom-ws: connection lost, reconnecting` | 企微 WS 断线重连（带 backoff） | `platform/wecom/websocket.go` |

## 2. 场景 playbook

### 2.1 消息发出去没有回复

按顺序 grep，定位卡在哪一环：

```bash
grep "message received"  <log> | tail     # ① 消息到了吗？没有=平台侧问题（断线/未订阅）
grep "message admission" <log> | tail     # ② 被准入拦了吗？action=drop/reject 看 reason
grep "processing message" <log> | tail    # ③ 拿到会话锁了吗？没有=会话被别的轮占着
grep "turn complete"     <log> | tail     # ④ 这一轮完成了吗？
```

| 断点 | 判定 | 处置 |
|---|---|---|
| 无 `message received` | 平台没收到 | 查 `wecom-ws: connection lost` / `bridge: adapter` 是否在重连；平台凭证/回调配置 |
| 有 `message admission` 但 action≠dispatch | 被 ACL/限流/命令拦 | 看 reason；查 `allow_from`、`[rate_limit]` |
| 有 `message received` 无 `processing message` | 会话锁被占（上一轮还没完） | 见 2.2 |
| 有 `processing message` 无 `turn complete` | 轮内卡住 | 看 `slow agent *`；agent/模型侧问题；见 2.6 |

### 2.2 连续发消息后会话"卡死"、后续消息永不执行

**现象**：会话忙时又发一条消息，之后该会话**再也不能处理任何消息**（新消息一直像"已收到"但不执行），
**只能 `/new` 新建会话或重启进程才能恢复**。

**判定**（日志特征）：

```bash
grep -E "message queued for busy session|interruptible: interrupting" <log>
grep "processing queued messages" <log>      # ← 如果只有前者、没有它，说明队列从未被消费
```

即：看到 `message queued for busy session queue_depth=1` 之后再无 `processing queued messages`
/ `turn complete`，则该会话已卡死。

**根因**（v1.3.17 修复）：打断当前轮后，排队的那条消息本应由被打断回合的 drain 循环消费，
但排队回合**未铸造新的 turn epoch**，继承了被打断回合的 epoch。被打断回合仍在异步吐出的
尾随事件（`result`/`error`，带旧 epoch）与新回合无法区分 → 新回合的 `EventResult`
被 epoch 校验当作"陈旧事件"丢弃 → 事件循环永远等不到结果 → 持有 session 锁不释放。

**为什么 `/cancel`、`/stop` 也救不回**：卡死时事件循环已退出、`cancelCh` 已被置 nil，
`/cancel` 找不到"进行中的回合"；`/stop` 虽能清状态，但释放不了那个 goroutine 栈里的 session 锁。
所以该会话在**本次进程生命周期内**无法再激活。

**处置**：
- **升级到 ≥ v1.3.17**（根因修复）。
- 临时恢复：`/new` 新建会话，或重启进程（重启后**磁盘上的会话本身是完好的**，
  重新 resume 同一 agent_session 即可继续用）。

**⚠️ 别指望 `agent session idle timeout` 兜底**：卡死时的阻塞发生在**事件循环退出之后**
（在排队 drain 里等一个被丢弃的 `EventResult`），`idle_timeout_mins` 的计时器已随事件循环退出，
不会触发。实测卡死 30+ 分钟无任何 `agent session idle timeout` 日志 —— 所以"等它超时自愈"
是无效的，必须升级或重启。

> 补充：`message queued` 与"已中断"提示在 v1.3.17 起已**不再由引擎发送**（由 CLI 自己产出内容）。
> 若你还看到「⏸️ (已中断)」「📬 消息已收到，将在当前任务完成后处理。」这两条气泡，
> 说明**跑的是旧版二进制** —— 回到第 0 节确认版本。

### 2.3 权限一直弹、agent 干活要人工确认

**现象**：已配 `mode = "bypassPermissions"`，但每个工具调用仍弹「Agent 想要使用 XX：允许/拒绝/允许所有」。

**判定与处置**：
1. 确认 `[projects.agent.options]` 里**显式写了** `mode = "bypassPermissions"`
   （codebuddy headless 不读 settings.json，见常见坑 #9）。
2. 确认二进制版本 ≥ **v1.3.16**（`normalizeMode` 大小写 bug 曾使该值静默失效）。
   旧版即使写对也不生效 —— 回到第 0 节查版本。
3. 相对 `work_dir` 在旧版会漂移导致项目级 settings 读不到、mode 回退 `default`
   （v1.3.15 修复）。若用相对路径，改成绝对路径或升级。
4. 注意 `permissions.deny`（`.codebuddy/settings.json`）优先级最高，会覆盖 bypass。

### 2.4 Web 端显示异常

| 现象 | 可能原因 | 对照版本 |
|---|---|---|
| 面板一直显示「执行中」不熄灭 | 后台读取协程被误判为运行中 | v1.3.9 修复 |
| 断开重连后丢回复 | 重连未补拉 history | v1.3.8 修复 |
| 本地文件路径不可点 | 普通消息未传 workspaceDir | v1.3.10 修复 |
| 页面白屏（React #130） | `react-markdown` 的 components 传了 `undefined` | 相关版本已修 |
| 子 Agent 执行完"只剩结果"、嵌套丢失 | row-merge 致 tool_use 被替换 | v1.3.18 修复 |

### 2.5 平台连接问题

```bash
grep -E "connection lost|reconnecting|subscribed successfully" <log> | tail
```

- `wecom-ws: connection lost ... unexpected EOF` 反复出现、但随后 `subscribed successfully`
  = 正常重连（企微会定期踢长连接）。**只看是否最终订阅成功**，偶发重连不必惊慌。
- 持续重连不成功：查网络/代理。⚠️ **关闭残留代理变量**：某些终端会带 `http_proxy` 等，
  导致 agent 子进程走已停的代理（`ECONNREFUSED`）。启动脚本里 `unset http_proxy https_proxy ...`
  是一道常见防线。

### 2.6 回合迟迟不结束、`heron-connect send` 进程存活数天

**现象**：某会话的回合长时间不结束，`ps` 里能看到一个 `heron-connect send` 进程已存活数天。

**判定**：

```bash
ps -eo pid,etime,cmd | grep "heron-connect" | grep -v grep   # etime 很大 = 挂住
grep "slow agent send" <log> | tail                           # 发送路径慢的先兆
```

**根因**（v1.3.19 修复）：所有访问本机实例的 CLI 子命令（`send` / `share` / `cron` / `relay`）
的 HTTP client 原本**都没有 `Timeout`、也不带 context 超时**。若服务端迟迟不返回
（如平台发送阻塞在已死的 socket 上），该进程永久阻塞 —— 而 `send` 是在 agent 回合内被调用的，
于是**整个回合被拖住**。

**处置**：
- **升级到 ≥ v1.3.19**（CLI 侧统一加 30s 超时）。
- 防御性做法：让 agent 调用时自行加壳超时，如
  `timeout 60 heron-connect send -m "..."` —— 即使某天再遇到别的阻塞点，也能自己收场。
- 临时清理：`kill` 掉僵住的进程；若该回合已因此永久卡住，`/new` 或重启。

## 3. 排查命令速查

```bash
# 版本与安装位置
which -a heron-connect && heron-connect --version

# 前台启动，直接看启动日志（最快定位配置错误）
heron-connect --config ./config.toml

# 健康诊断：agent 二进制 / 认证 / 平台 / 系统 / 依赖 / 网络
heron-connect doctor        # 会话内也可发 /doctor

# 守护进程
heron-connect daemon status
heron-connect daemon logs -f

# 在大盘看统计是否在采集
ls <data_dir>/metrics/          # 有 turns-YYYY-MM-DD.jsonl = 采集正常

# 看会话是否落盘（重启后能否恢复）
ls <data_dir>/sessions/         # <project>[_<hash>].json
```

## 4. 后台运行（daemon 服务）

**推荐直接用 `daemon install`，无需 shell 脚本**。一条命令完成安装/更新/重启（幂等）：

```bash
# 安装并启动（已装过则覆盖重装 = 先停旧再启新）
heron-connect daemon install --config /path/to/config.toml --force

# 可选：覆盖日志参数（优先级 CLI > TOML > 默认）
heron-connect daemon install --config /path/to/config.toml --force \
  --log-file /path/to/app.log --log-max-size 20 --log-retention-days 30
```

**cwd 说明**：daemon 模式下进程 cwd = config.toml 所在目录（`--config` 的父目录）。
所以 toml 里的相对路径（`work_dir`、`cron_data_dir`、`[log].file`）都基于该目录解析，
整个目录可整体迁移，无需写死绝对地址。

日常管理：

```bash
heron-connect daemon status          # 状态 / PID / 日志路径
heron-connect daemon logs -f         # 实时看日志（-f 跟随，-n N 看最近 N 行）
heron-connect daemon stop            # 停止
heron-connect daemon start           # 启动（需已 install）
heron-connect daemon restart --force # 重启（先杀旧进程）
heron-connect daemon uninstall       # 卸载（日志与会话数据保留）
```

支持 Linux systemd / macOS launchd / Windows Task Scheduler。

> 容器无 systemd 时用 `setsid`/`nohup` 后台化（注意上面第 0 节的 PATH 坑）。
> 另有 `scripts/service.sh` 脚本封装（`service.sh --config <path> <start|stop|...>`），
> 本质就是把任意文件名的 toml 软链成 `config.toml` 再走 `daemon install`。
> 若你的配置文件名就叫 `config.toml`，直接 `daemon install --config` 即可，无需脚本。

