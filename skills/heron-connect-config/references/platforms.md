# 平台接入配置（[[projects.platforms]]）

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

## 支持的 platform type

| type | 连接方式 | 需公网 IP |
|------|---------|:---------:|
| `feishu` | WebSocket 长连接 | 否 |
| `lark` | WebSocket / Webhook | 否 |
| `dingtalk` | Stream 模式 | 否 |
| `telegram` | Long Polling | 否 |
| `slack` | Socket Mode | 否 |
| `discord` | Gateway WebSocket | 否 |
| `qq` | NapCat/OneBot WebSocket | 否 |
| `qqbot` | QQ 官方机器人 | 视实现 |
| `weixin` | ilink Long Polling | 否 |
| `wecom` | HTTP Webhook | **是** |
| `max` | Long Poll / Webhook | 可选 |
| `line` | HTTP Webhook | **是** |
| `weibo` | — | — |
| `web` | **虚拟条目**，不建立真实连接 | 否 |

## 通用安全/会话字段（各平台通用）

- `allow_from`：允许的用户 ID，逗号分隔；`"*"`=全部。用 `/whoami` 获取自己的 ID。
- `allow_chat`（部分平台）：允许的群 chat_id。
- `group_only`：true 则只响应群聊，忽略私聊。
- `share_session_in_channel`：true 则群内所有用户共享一个 agent 会话。
- `thread_isolation`：按话题/根消息隔离会话（每个 thread/root 一个会话）。例如 Discord
  论坛帖、飞书话题场景，`thread_isolation = true` 后同一频道里不同帖子各自独立会话，互不串号。

## 虚拟 `type = "web"` 平台（Web 管理后台专用）

`web` 是一个**不建立真实连接**的虚拟平台类型（由 `platform/webnoop` 注册），它不连接任何
IM，专门用来给「Web 管理后台的会话」附加配置。**Web 后台本身不走这个平台**——它通过
全局 `[bridge]` WebSocket 服务接入（进程级、跨项目共享，见 `references/advanced.md`），
桥接消息的 `Platform` 名就是 `"web"`。

⚠️ 若项目里写了 `type = "web"` 而二进制未注册该类型，heron-connect **启动时会直接退出**
（未知平台类型 → `os.Exit(1)`）。默认二进制已含 webnoop，一般不会触发。

它只有两个实际用途：

**1. 给 Web 后台会话单独设显示模式**（优先级：平台 > 项目 > 全局 > 默认）：

```toml
[[projects]]
name = "auto-bugfix"
[projects.display]
mode = "quiet"              # 默认：平台无覆盖时走这个

[[projects.platforms]]
type = "wecom"              # 真实 IM ...
# ...

[[projects.platforms]]
type = "web"                # 虚拟条目，不建立真实连接
[projects.platforms.display]
mode = "full"               # 仅 Web 后台显示完整思考/工具过程
```

**2. 让 Web 后台会话永不被空闲切换**（`reset_on_idle_mins` 平台级覆盖）：

```toml
[[projects.platforms]]
type = "web"
reset_on_idle_mins = 0      # 0 = 该平台禁用空闲切换，Web 会话永久保留
```

此时同项目真实 IM（如企微）仍沿用项目级 `reset_on_idle_mins`（如 720），两者互不影响。

## 各平台 options

### 飞书 feishu（最常用，WebSocket 长连接免公网）

```toml
[[projects.platforms]]
type = "feishu"
[projects.platforms.options]
app_id = "cli_xxxx"
app_secret = "${FEISHU_APP_SECRET}"
# enable_feishu_card = true    # 交互式卡片，false 则纯文本
# allow_from = "*"
# allow_chat = "*"
# group_only = false
# share_session_in_channel = false
# thread_isolation = false
# group_reply_all = false      # true 则群聊无需 @ 也响应
# reaction_emoji = "OnIt"      # 收消息表情；"none" 禁用
# done_emoji = "none"          # 回复完成表情
# progress_style = "legacy"    # legacy | compact | card
# resolve_mentions = false
```
配置步骤：open.feishu.cn 建应用 → 开启机器人 → 加 `im.message.receive_v1` 事件，
选 WebSocket 长连接。可用 `heron-connect feishu setup --project <name>` 快速配置。

### Lark 国际版

`type = "lark"`，`domain = "https://open.larksuite.com"`。只有显式配 `encrypt_key`
时才用 Webhook 模式（需 `port` + `callback_path`）。

```toml
# [projects.platforms.options]
# app_id = "your-lark-app-id"
# app_secret = "your-lark-app-secret"
# domain = "https://open.larksuite.com"
# port = "8080"
# callback_path = "/feishu/webhook"
# encrypt_key = ""
# enable_feishu_card = true
```

### 钉钉 dingtalk

```toml
# [projects.platforms.options]
# client_id = "AppKey"          # 开放平台 > 应用功能 > 机器人 > Stream 模式
# client_secret = "AppSecret"
# allow_from = "*"
# share_session_in_channel = false
```

### Telegram

@BotFather 发 `/newbot` 建机器人，复制 token。

```toml
# [projects.platforms.options]
# token = "${TELEGRAM_BOT_TOKEN}"
# allow_from = "*"
# group_reply_all = false
# share_session_in_channel = false
# enable_reactions = false
```

### Slack（Socket Mode，免公网）

需 Bot Token（`xoxb-...`）+ App Token（`xapp-...`）。

```toml
# [projects.platforms.options]
# bot_token = "xoxb-..."
# app_token = "xapp-..."
# allow_from = "*"
# share_session_in_channel = false
```

### Discord

Bot + `applications.commands` scope，需开 Message Content Intent。

```toml
# [projects.platforms.options]
# token = "discord-bot-token"
```

### 企业微信 wecom（HTTP Webhook，**需公网 IP**）

```toml
# [projects.platforms.options]
# 依据 docs/wecom.md 与 config.example.toml 填写 webhook 相关字段
```
> 注意流式输出阈值要低于物理上限，预留展示安全余量。

### MAX

```toml
# [projects.platforms.options]
# token = "your-max-bot-token"
# api_base = "https://platform-api.max.ru"   # 可选覆盖
# # Webhook 模式（高流量必需，MAX 限长轮询 2 RPS）：
# webhook_url    = "https://bot.example.com/webhook"
# webhook_listen = "127.0.0.1:8090"
# webhook_path   = "/webhook"
# webhook_secret = "long-random-string"
```
详见 `docs/max-webhook.md`。

### QQ（via NapCat / OneBot v11）

```toml
[[projects.platforms]]
type = "qq"
[projects.platforms.options]
ws_url = "ws://127.0.0.1:3001"   # NapCat Forward WebSocket URL
token = ""                        # 可选，须与 NapCat access_token 一致
allow_from = "*"                  # 或 "12345,67890"
```
需先部署 NapCat（Docker/本体），开 WebUI 启用 Forward WebSocket 端口 3001。

### QQ 官方机器人（qqbot）

```toml
[[projects.platforms]]
type = "qqbot"
[projects.platforms.options]
# 见 config.example.toml 对应章节（app_id / secret 等）
```

### 微信个人版（weixin，ilink，免公网）

```toml
[[projects.platforms]]
type = "weixin"
[projects.platforms.options]
token = "your-ilink-bot-bearer-token"
# base_url = "https://ilinkai.weixin.qq.com"   # 可选
# cdn_base_url = "https://novac2c.cdn.weixin.qq.com/c2c"  # 可选 CDN 根路径
# allow_from = "*"              # 或 "user1@im.wechat,user2@im.wechat"
# account_id = "default"        # 可选：区分多账号状态目录
# long_poll_timeout_ms = 35000
```
快速配置：`heron-connect weixin setup --project <name>`（扫码登录写 token）。
入站图片/文件/视频/语音从微信 CDN 拉取并 AES 解密。状态文件在 `<data_dir>/weixin/<project>/<account_id>/`。

### LINE（HTTP Webhook，需公网）

```toml
[[projects.platforms]]
type = "line"
[projects.platforms.options]
channel_secret = "your-line-channel-secret"
channel_token = "your-line-channel-access-token"
port = "8080"
callback_path = "/callback"
allow_from = "*"
```
需在 LINE 控制台建 Messaging API channel，webhook URL 设为 `https://<公网域名>:<port>/callback`。

### 微博（weibo，WebSocket 免公网）

```toml
[[projects.platforms]]
type = "weibo"
[projects.platforms.options]
app_id = "your-weibo-app-id"
app_secret = "your-weibo-app-secret"
allow_from = "*"
# token_endpoint = ""   # 可选：自定义 token 接口
# ws_endpoint = ""      # 可选：自定义 WebSocket 地址
```
通过微博开放平台（龙虾助手）注册应用，经 WebSocket（open-im.api.weibo.com）收发私信。
