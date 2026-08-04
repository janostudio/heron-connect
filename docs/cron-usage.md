# cc-connect-qhn 定时任务 (Cron) 使用指南

## 概述

cc-connect-qhn 内置了完整的定时任务系统，支持在指定时间自动执行 AI 提示词或 Shell 命令，并将结果推送到聊天平台。

核心能力：

- 支持标准 Cron 表达式（5 字段：分 时 日 月 星期）
- 支持 AI 提示词任务 和 Shell 命令任务
- 支持复用已有会话 或 每次创建独立会话
- 支持静默执行（不发送开始通知）或 完全静默（不发送任何消息）
- 支持超时控制（默认 30 分钟）
- 支持权限模式覆盖

## 架构

```
创建方式               调度器                   执行
─────────────────────────────────────────────────────
CLI 命令           →                       ┌→ AI 提示词任务
聊天命令 /cron     →  CronScheduler        ┤
管理 API           →  (robfig/cron/v3)     └→ Shell 命令任务
Web UI             →
```

## 持久化存储

所有通过运行时方式（聊天命令 / CLI / 管理 API / Web UI）创建的定时任务都会持久化到本地 JSON 文件，下次启动时自动恢复。

### 存储位置

```
<dataDir>/crons/jobs.json
```

- `dataDir` 取自 `config.toml` 的顶层 `data_dir` 配置，默认值为 `~/.cc-connect-qhn`。
- 因此默认路径通常是 `~/.cc-connect-qhn/crons/jobs.json`。
- 文件不存在或为空时不会报错，调度器按空任务列表启动；首次创建任务时目录和文件会自动生成。

### 文件格式

整个文件是一个 JSON 数组，每个元素是一个任务对象，字段使用 snake_case。示例：

```json
[
  {
    "id": "01J9X3F2K7Q4Z2N8R1V0H6M3P5",
    "project": "default",
    "session_key": "feishu:ou_abc123:oc_def456",
    "cron_expr": "0 9 * * 1",
    "prompt": "帮我总结上周的工作内容并发送到群里",
    "exec": "",
    "work_dir": "",
    "description": "每周工作总结",
    "enabled": true,
    "silent": false,
    "mute": false,
    "session_mode": "reuse",
    "mode": "default",
    "timeout_mins": 30,
    "created_at": "2026-07-21T09:12:35+08:00",
    "last_run": "2026-07-21T09:00:00+08:00",
    "last_error": ""
  }
]
```

字段含义：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 任务唯一 ID（ULID），创建时自动生成 |
| `project` | string | 所属项目名，对应配置文件中的 `[projects.xxx]` |
| `session_key` | string | 会话标识，格式 `平台:用户ID:会话ID` |
| `cron_expr` | string | 标准 5 字段 Cron 表达式 |
| `prompt` | string | AI 提示词（与 `exec` 二选一） |
| `exec` | string | Shell 命令（与 `prompt` 二选一） |
| `work_dir` | string | Shell 命令的工作目录，空表示进程当前目录 |
| `description` | string | 任务描述，显示在开始通知中 |
| `enabled` | bool | 是否启用，`false` 时不参与调度 |
| `silent` | bool | 隐藏"任务开始"通知，仍推送结果 |
| `mute` | bool | 完全静默，不发送任何消息 |
| `session_mode` | string | `reuse` 复用会话 / `new_per_run` 每次新建 |
| `mode` | string | 权限模式覆盖：`default`/`bypassPermissions`/`acceptEdits`/`plan`/`auto`/`dontAsk` |
| `timeout_mins` | int | 超时分钟数，`0` 表示不限制 |
| `created_at` | string | 创建时间（RFC 3339） |
| `last_run` | string | 上次执行时间（RFC 3339，未执行过为空） |
| `last_error` | string | 上次执行错误信息，成功时为空字符串 |

### 读写行为

- **写入**：每次 Add / Remove / Update / Enable / Disable / Mute / MarkRun 都会把整个任务列表重新序列化（`json.MarshalIndent`，2 空格缩进）后原子写入文件，避免半写损坏。
- **读取**：仅在进程启动时加载一次到内存，运行期间不再从磁盘重读。
- **并发**：调度器在内存里维护任务状态，磁盘文件只是持久化镜像；不要在进程运行时手动编辑 `jobs.json`，否则修改会被下一次内存写回覆盖。

### 直接查看与备份

- **查看全部任务**：直接 `cat ~/.cc-connect-qhn/crons/jobs.json`，或用 `jq` 过滤：

  ```bash
  # 只看启用的任务
  jq '.[] | select(.enabled == true) | {id, cron_expr, prompt, exec}' ~/.cc-connect-qhn/crons/jobs.json

  # 只看某个项目的任务
  jq '.[] | select(.project == "default")' ~/.cc-connect-qhn/crons/jobs.json
  ```

- **备份/迁移**：直接复制 `jobs.json` 即可。新机器放到对应 `dataDir/crons/` 目录下重启进程即可恢复。
- **批量导入**：可以先停掉进程，编辑 `jobs.json` 追加任务对象（确保 `id` 唯一、`cron_expr` 合法），再启动。

> ⚠️ **不要在进程运行时手改 `jobs.json`**。运行期间内存中的任务状态会覆盖磁盘文件。如需手动编辑，务必先停止 cc-connect-qhn 进程。

### TOML 配置的关系

`config.toml` 的 `[cron]` 段**只能**设置两个全局默认值（`silent`、`session_mode`），**不能**在 TOML 里静态声明任务。任务定义只存在于 `jobs.json`，所有创建方式最终都落到这个文件。如果未来需要 TOML 静态任务声明，需要扩展 `CronConfig` 结构并补充 `[[cron.jobs]]` 表结构（当前版本未实现）。

## 创建方式

共有 4 种方式创建定时任务，按使用场景选择：

### 方式一：聊天命令（推荐，最简单）

在任意连接的聊天平台（飞书、Telegram、Discord 等）中直接发送命令：

#### 创建 AI 提示词任务

```
/cron add <分> <时> <日> <月> <星期> <提示词>
```

**示例**：

```
/cron add 0 9 * * 1 帮我总结上周的工作内容并发送到群里
/cron add 30 10 * * * 检查今天的天气并给出穿衣建议
/cron add 0 0 * * * 帮我运行 git pull 并检查有没有冲突
```

创建后任务会**自动绑定到当前会话**（SessionKey 取当前消息的 SessionKey）。

#### 创建 Shell 命令任务（仅管理员）

```
/cron addexec <分> <时> <日> <月> <星期> <shell命令>
```

**示例**：

```
/cron addexec 0 2 * * * cd /path/to/project && git pull && make build
/cron addexec */5 * * * * df -h | grep /dev/sda1
```

#### 其他管理命令

```
/cron list              # 查看所有定时任务
/cron del <任务ID>       # 删除指定任务
/cron enable <任务ID>    # 启用任务
/cron disable <任务ID>   # 禁用任务
/cron mute <任务ID>      # 完全静默（不发送任何消息）
/cron unmute <任务ID>    # 取消静默
/cron setup             # 显示配置指引
/cron                   # 显示任务列表（支持卡片渲染的平台会显示卡片）
```

### 方式二：CLI 命令行

在运行 cc-connect-qhn 的机器上使用命令行管理：

```bash
# 添加 AI 提示词任务
cc-connect-qhn cron add \
  --project default \
  --session-key feishu:user_xxx:chat_xxx \
  --cron "0 9 * * 1" \
  --prompt "帮我总结上周的工作" \
  --desc "每周工作总结"

# 添加 Shell 命令任务
cc-connect-qhn cron add \
  --project default \
  --session-key feishu:user_xxx:chat_xxx \
  --cron "0 2 * * *" \
  --exec "cd /path/to/project && git pull && make build" \
  --desc "每日自动构建"

# 指定会话模式（每次新建独立会话）
cc-connect-qhn cron add \
  --project default \
  --session-key feishu:user_xxx:chat_xxx \
  --cron "0 9 * * 1" \
  --prompt "帮我总结上周的工作" \
  --session-mode new_per_run \
  --timeout-mins 60

# 列出所有任务
cc-connect-qhn cron list

# 查看任务详情
cc-connect-qhn cron info <任务ID>

# 编辑任务
cc-connect-qhn cron edit <任务ID> --enabled false

# 删除任务
cc-connect-qhn cron del <任务ID>
```

**CLI 参数说明**：

| 参数 | 必填 | 说明 |
|---|---|---|
| `--project`, `-p` | 是 | 项目名称（对应配置文件中的 [projects.xxx]） |
| `--session-key`, `--session`, `-s` | 是 | 会话标识，格式为 `平台:用户ID:会话ID`，决定任务在哪个平台和会话中执行 |
| `--cron`, `-c` | 是 | Cron 表达式，5 字段 |
| `--prompt` | 二选一 | AI 提示词 |
| `--exec` | 二选一 | Shell 命令（与 `--prompt` 互斥） |
| `--desc`, `--description` | 否 | 任务描述，会显示在开始通知中 |
| `--session-mode` | 否 | 会话模式：`reuse`（复用）或 `new_per_run`（每次新建） |
| `--timeout-mins` | 否 | 超时时间（分钟），默认 30，设为 0 表示不限制 |

> **提示**：如果通过 AI Agent 内部调用（如 Claude Code 中的 `cc-connect-qhn cron add`），`--project` 和 `--session-key` 可以通过环境变量 `CC_PROJECT` 和 `CC_SESSION_KEY` 自动填充。

### 方式三：管理 API（REST）

管理服务器提供 RESTful API，适合集成到外部系统：

```bash
# 获取任务列表
curl http://localhost:8080/api/v1/cron

# 创建任务
curl -X POST http://localhost:8080/api/v1/cron \
  -H "Content-Type: application/json" \
  -d '{
    "project": "default",
    "session_key": "feishu:user_xxx:chat_xxx",
    "cron_expr": "0 9 * * 1",
    "prompt": "帮我总结上周的工作",
    "description": "每周工作总结",
    "session_mode": "reuse"
  }'

# 更新任务（部分更新）
curl -X PATCH http://localhost:8080/api/v1/cron/<任务ID> \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'

# 删除任务
curl -X DELETE http://localhost:8080/api/v1/cron/<任务ID>
```

### 方式四：Web UI

启动管理服务器后，访问 Web 界面 `http://localhost:8080`，在左侧导航中找到 **Cron** 页面，可以：

- 查看所有定时任务列表（含下次执行时间、上次运行状态）
- 创建新任务（填写 Cron 表达式、提示词/命令、会话 Key 等）
- 编辑任务（启用/禁用、修改表达式等）
- 删除任务

> **注意**：当前 Web UI 的创建表单暂未暴露 `session_mode`、`mute` 和 `timeout_mins` 字段。如果需要设置这些参数，请使用 CLI 或管理 API。

---

## 会话 (Session) 详解

### 什么是 SessionKey

`SessionKey` 是定时任务与具体聊天会话的绑定标识，格式为：

```
平台前缀:用户ID:会话ID
```

示例：
- `feishu:ou_abc123:oc_def456` — 飞书用户 ou_abc123 在群 oc_def456 中的会话
- `telegram:123456:789012` — Telegram 用户 123456 在聊天 789012 中的会话
- `wecom:user001:chat001` — 企微用户 user001 在群 chat001 中的会话

**SessionKey 决定了两个关键信息**：
1. **通过哪个平台发送消息** — 从 SessionKey 前缀解析平台名称
2. **绑定哪个会话** — 决定任务执行时是复用已有会话还是创建新会话

### 两种会话模式

| 模式 | 值 | 行为 |
|---|---|---|
| **复用模式**（默认） | `reuse` 或不填 | 每次执行时复用当前活跃的 AI 会话，AI 能看到之前的对话历史 |
| **独立模式** | `new_per_run` 或 `new-per-run` | 每次执行时创建一个全新的隔离会话，AI 没有历史上下文 |

#### 复用模式 (`reuse`)

```
配置：session_mode = "reuse"
触发时：获取当前活跃会话 → 注入 cron 提示词 → AI 在完整上下文中回答
适用场景：
  - 需要 AI 了解之前对话的任务（如"继续上次的分析"）
  - 需要累积上下文的长期跟踪任务
  - 同一个群里的日常定时汇报
```

#### 独立模式 (`new_per_run`)

```
配置：session_mode = "new_per_run"
触发时：创建隔离的临时会话 → 注入 cron 提示词 → AI 独立执行 → 执行完毕清理
适用场景：
  - 每次独立执行的查询任务（如"检查今天天气"）
  - 不希望污染主会话的批量任务
  - 需要严格隔离的 Shell 命令执行
```

### 在聊天命令中指定会话模式

通过聊天创建的 cron 任务默认绑定到**当前会话**且使用**复用模式**。如需指定会话模式，请使用 CLI 方式。

---

## Cron 表达式参考

cc-connect-qhn 使用标准 5 字段 Cron 表达式：

```
┌────────── 分钟 (0-59)
│ ┌──────── 小时 (0-23)
│ │ ┌────── 日 (1-31)
│ │ │ ┌──── 月 (1-12)
│ │ │ │ ┌── 星期 (0-6, 0=周日)
│ │ │ │ │
* * * * *
```

**常用示例**：

| 表达式 | 含义 |
|---|---|
| `0 9 * * 1` | 每周一早上 9:00 |
| `0 9 * * *` | 每天早上 9:00 |
| `0 9 * * 1-5` | 工作日早上 9:00 |
| `*/5 * * * *` | 每 5 分钟 |
| `0 */2 * * *` | 每 2 小时 |
| `30 10 1 * *` | 每月 1 号 10:30 |
| `0 0 1 1 *` | 每年 1 月 1 日 00:00 |
| `0 9,18 * * *` | 每天 9:00 和 18:00 |

> 在线验证工具：https://crontab.guru/

---

## 静默模式：silent vs mute

| 设置 | 开始通知 "⏰ 任务描述" | 执行结果/输出 |
|---|---|---|
| 默认（都不设置） | 发送 | 发送 |
| `silent = true` | **不发送** | 发送 |
| `mute = true` | **不发送** | **不发送** |

- **silent**：只隐藏"定时任务开始执行"的通知消息，执行结果仍然正常推送
- **mute**：完全静默，不发送任何消息（适合纯后台任务）

设置方式：
- CLI: `cc-connect-qhn cron edit <ID> --mute` （通过管理 API 设置 `mute` 字段）
- 聊天命令: `/cron mute <任务ID>` / `/cron unmute <任务ID>`
- 全局默认 silent: 在 `config.toml` 中设置 `[cron] silent = true`

---

## 超时控制

- **默认超时**: 30 分钟
- **设为 0**: 无超时限制
- **设为 N**: N 分钟后超时

超时后任务会被标记为失败，`LastError` 记录为 `"job timed out after <duration>"`。

设置方式：
- CLI: `cc-connect-qhn cron add --timeout-mins 60 ...`
- 管理 API: `{"timeout_mins": 60}`

---

## 全局配置

在 `config.toml` 的 `[cron]` 段中设置全局默认值：

```toml
[cron]
silent = false          # 是否全局静默开始通知（默认 false）
session_mode = "reuse"  # 全局默认会话模式："reuse" 或 "new_per_run"
```

这些配置会作为所有 cron 任务的默认值，单个任务可以通过任务级设置覆盖。

---

## 权限模式覆盖

每个 cron 任务可以指定独立的权限模式（`mode` 字段），覆盖项目默认设置：

| 值 | 含义 |
|---|---|
| `default` | 使用项目默认 |
| `bypassPermissions` | 跳过权限检查 |
| `acceptEdits` | 自动接受编辑 |
| `plan` | 需要计划审批 |
| `auto` | 自动模式 |
| `dontAsk` | 不询问 |

适用于需要在定时任务中放宽或收紧权限控制的场景。

---

## AI Agent 自动创建

在连接了 AI Agent（如 Claude Code）的场景下，Agent 的系统提示中已包含 cron 创建指令。用户可以直接用自然语言描述需求，Agent 会自动调用 `cc-connect-qhn cron add` 命令：

> **用户**: "每天早上 9 点帮我查一下今天的天气"
>
> **Agent**: 自动执行 `cc-connect-qhn cron add --cron "0 9 * * *" --prompt "查一下今天的天气" --desc "每日天气查询"`

---

## 常见问题

### Q: 任务数据存在哪里？能直接看文件吗？
A: 所有任务持久化在 `<dataDir>/crons/jobs.json`（默认 `~/.cc-connect-qhn/crons/jobs.json`），JSON 数组格式，可直接 `cat` 或用 `jq` 查看。详见上文「持久化存储」小节。注意进程运行期间不要手改这个文件，修改会被内存覆盖。

### Q: 能在 config.toml 里直接写死定时任务吗？
A: 不能。`[cron]` 段只支持 `silent` 和 `session_mode` 两个全局默认字段，没有 `[[cron.jobs]]` 之类的任务表。任务只能通过运行时方式创建并落到 `jobs.json`。

### Q: 如何查看某个任务的下次执行时间？
A: 使用 CLI `cc-connect-qhn cron info <任务ID>`，输出中会包含 `next_run` 字段。

### Q: 聊天命令创建的 cron 任务能跨会话执行吗？
A: 不能。聊天命令创建的任务会绑定到**当前聊天会话**。如果需要跨会话，请使用 CLI 并指定 `--session-key`。

### Q: 任务执行失败了怎么排查？
A: 
1. 使用 `cc-connect-qhn cron list` 查看 `LastError` 字段
2. 检查 cc-connect-qhn 的运行日志
3. 对于 Shell 任务，检查命令是否可执行、路径是否正确

### Q: 如何实现"每 30 分钟执行一次"？
A: 使用 Cron 表达式 `*/30 * * * *`。

### Q: Web UI 创建的任务可以设置 session_mode 吗？
A: 当前版本 Web UI 的创建表单暂不支持。请使用 CLI 或管理 API 创建，然后在 Web UI 中管理。
