# IM 群聊会话隔离与共享

本页说明 cc-connect-qhn 如何为不同 IM 消息选择对话上下文，以及群聊中不同成员是否共享 Agent / ACP 会话。

## 核心规则

平台适配器为每条入站消息生成 `SessionKey`。同一项目或工作区内：

- 相同 `SessionKey` 使用同一个活动 cc-connect 会话，并复用其 Agent / ACP 上下文；
- 不同 `SessionKey` 使用独立会话、独立上下文和独立排队状态；
- Core 不会再按群 ID 或频道 ID 合并不同的 `SessionKey`。

会话边界会影响：

- 对话历史和底层 Agent / ACP session；
- `/new`、`/switch`、`/list` 操作的当前会话；
- 同一会话内的消息排队与工具审批交互状态。

本文中的 key 形式用于解释组成维度，不应视为稳定的外部 API。

## 群聊规则速查

默认情况下，多数平台采用“群或频道 + 发送用户”隔离。可配置平台设置 `share_session_in_channel = true` 后，会省略发送用户维度，让同一群或频道共享一个会话。

| 平台 | 默认群聊边界 | `share_session_in_channel = true` | 线程 / 话题行为 |
|---|---|---|---|
| 飞书 / Lark | 群 + 用户 | 群共享 | `thread_isolation = true` 时按根消息共享，优先于共享开关 |
| 钉钉 | 群会话 + 用户 | 群共享 | 无额外 thread 隔离 |
| Telegram | chat + 用户 | chat 共享 | Forum topic 自动成为额外维度；普通群回复不会切分会话 |
| Slack | channel + 用户 | channel 共享 | Slack 回复线程不改变会话边界 |
| Discord | channel + 用户 | channel 共享 | `thread_isolation = true` 时同一 Discord thread 共享 |
| QQ OneBot | group + 用户 | group 共享 | 无额外 thread 隔离 |
| QQ Bot | group + 用户 | group 共享 | 无额外 thread 隔离 |
| 企业微信 WebSocket 智能机器人 | chat + 用户 | 当前没有共享开关 | 无额外 thread 隔离 |
| LINE | group / room | 固定共享 | 群或 room 是唯一边界 |
| MAX | chat + 用户 | 当前没有共享开关 | 未单独建模群类型 |

微信个人号 iLink 和微博当前主要是私聊模型，不适用群聊会话规则。

## 企业微信

企业微信有两种接入模式，边界不同：

- **WebSocket 智能机器人**：群聊按“群 + 用户”隔离。同一群的不同用户会使用不同 Agent / ACP 会话；回复仍发送到同一群。
- **HTTP 回调自建应用**：当前按发送用户隔离，入站模型不以群 ID 构造会话边界。

因此，企业微信 WebSocket 群中用户 A 与用户 B 的上下文默认不会互相看到。

## 共享会话配置

支持该选项的平台可在对应平台配置段启用：

```toml
[platforms.feishu]
share_session_in_channel = true
```

启用后，同一群或频道内所有成员会共享：

- 对话上下文；
- 当前命名会话；
- `/new`、`/switch` 等会话命令的效果；
- 同一 Agent 会话内的排队状态；
- 会话内尚未完成的审批交互。

适合需要共同推进同一任务的固定小组；不适合多个成员并行提问、上下文需要互相隔离的群。

默认的隔离配置如下：

```toml
[platforms.feishu]
share_session_in_channel = false
```

对于钉钉、Telegram、Slack、Discord、QQ 和 QQ Bot，字段名称相同，配置在各自的平台段中。

### 常用配置示例

**多人独立上下文（默认）**

```toml
[platforms.feishu]
share_session_in_channel = false

[platforms.telegram]
share_session_in_channel = false

[platforms.discord]
share_session_in_channel = false
```

同一群内每位用户拥有独立上下文，适合答疑、支持和多人并行提问。

**固定小组共享一个上下文**

```toml
[platforms.slack]
share_session_in_channel = true

[platforms.dingtalk]
share_session_in_channel = true
```

同一频道或群内的成员会共同使用一个上下文。建议同时配置 `allow_from` 或 `admin_from`，避免不受信任成员改变共享任务状态。

**飞书按讨论串组织任务**

```toml
[platforms.feishu]
share_session_in_channel = false
thread_isolation = true
```

每个根消息讨论串使用一个共享上下文；不同讨论串彼此隔离。

**Discord 按 thread 组织任务**

```toml
[platforms.discord]
share_session_in_channel = false
thread_isolation = true
```

同一 Discord thread 内共享一个上下文；`thread_isolation` 启用后不会继续按用户拆分。

## 线程和话题

### 飞书 / Lark

群聊中启用 `thread_isolation = true` 后，同一根消息下的回复使用一个共享会话。该规则优先于 `share_session_in_channel` 和按用户隔离。

```toml
[platforms.feishu]
thread_isolation = true
share_session_in_channel = false
```

这适合让每个讨论串对应一个任务上下文；但同一讨论串内的不同用户会共享上下文。

### Discord

Discord 启用 `thread_isolation = true` 后，同一 Discord thread 使用一个共享会话。即使 `share_session_in_channel = false`，thread 内也不会继续按用户拆分。

### Telegram

Telegram 没有 `thread_isolation` 开关。Forum 群的 topic 会自动加入会话边界，因此不同 topic 使用不同会话；普通群中对某条消息的回复不会自动创建新会话，以避免上下文过度碎片化。

## 不等同于会话边界的配置

以下配置会影响消息是否触发、权限或工作目录，但通常不直接改变平台 `SessionKey`：

- `group_reply_all`：决定群内是否必须提及机器人才能触发；
- `allow_from`、`allow_chat`、`group_only`：决定允许谁、哪些群或哪些消息类型触发；
- `admin_from`：限制特权命令；
- Agent `mode`：决定工具审批与执行策略；
- `work_dir`、`mode = "multi-workspace"`、`base_dir`：决定工作目录和工作区路由。

multi-workspace 会为工作区选择独立 Agent 和 SessionManager，但不会把平台原本不同的用户会话自动合并为群共享会话。

## 选型建议

| 场景 | 建议 |
|---|---|
| 群内每个人独立提问 | 保持 `share_session_in_channel = false` |
| 固定小组共同推进一项任务 | 开启 `share_session_in_channel = true`，并限制可触发用户 |
| 飞书或 Discord 中按讨论串协作 | 启用对应的 `thread_isolation`，并明确 thread 内共享上下文 |
| 群成员较多或包含外部用户 | 使用默认隔离；配置 `allow_from`、`allow_chat` 和 `admin_from` |
| 多项目群或频道 | 使用 multi-workspace 绑定工作目录；再按需要选择群共享或用户隔离 |

## 会话生命周期

`reset_on_idle_mins` 到期后，下一条消息会为对应会话边界创建新的活动会话。修改共享配置不会自动合并或迁移已经存在的会话；后续消息会按新的 key 规则进入相应会话。
