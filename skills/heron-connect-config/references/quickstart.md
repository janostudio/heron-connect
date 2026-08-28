# 快速上手与基础规则

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源始终是仓库根目录
> `config.example.toml`。

## 配置文件基本规则

- **查找顺序**：`--config <path>` → `./config.toml` → `~/.heron-connect/config.toml`
- **默认数据目录**：`~/.heron-connect`（会话历史、session 状态以 JSON 保存）
- **环境变量替换**：任意字符串值支持 `${VAR_NAME}` 替换，例如
  `token = "${TELEGRAM_BOT_TOKEN}"`。**不要在配置里明文写死密钥**，用环境变量。
- 一个进程可管理**多个项目**：多个 `[[projects]]` 并列，各自独立 agent 与平台。
  `[[projects]]` 的数组表语义、完整示例、避免重复配置见 `references/project-agent.md`
  的「多 project 配置」。

## 最小可运行配置（Claude Code + 飞书）

```toml
[log]
level = "info"                     # debug | info | warn | error

[[projects]]
name = "my-project"                # 项目名，唯一标识

[projects.agent]
type = "claudecode"                # 见 references/project-agent.md 的 agent type 表

[projects.agent.options]
work_dir = "/absolute/path/to/your/project"   # 必填：代码目录绝对路径
mode = "default"                   # default | acceptEdits | plan | auto | bypassPermissions | dontAsk

[[projects.platforms]]
type = "feishu"

[projects.platforms.options]
app_id = "cli_xxxxxxxxxxxx"
app_secret = "${FEISHU_APP_SECRET}"
```

启动：`heron-connect --config ./config.toml`，看到
`msg="heron-connect is running" projects=1` 即成功。

## TOML 结构总览

```
全局设置
  language / data_dir / attachment_send / provider_presets_url / idle_timeout_mins
  [log]   [display]   [speech] [tts]   [stream_preview]   [instant_reply]
  [rate_limit] [outgoing_rate_limit] [cron] [queue] [relay]
  [webhook] [bridge] [management]
  [[providers]]  [[commands]]  [[aliases]]  [[hooks]]  banned_words

[[projects]]         一个「代码目录 + 独立 agent + 平台」的绑定单元
  name / run_as_user / admin_from / disabled_commands / inject_sender
  show_context_indicator / reply_footer / auto_session_title / mode(多工作区) / base_dir
  filter_external_sessions / queued_messages / reset_on_idle_mins
  [projects.agent]      绑定哪个 Agent
  [projects.agent.options]   agent 专属参数
  [projects.agent.providers] 该项目 API Provider 列表
  [projects.platforms]   一个平台接入
  [projects.platforms.options] 平台专属参数
  [projects.users]   [projects.heartbeat]   [projects.auto_compress]   [projects.observe]
  [projects.display]   [projects.references]
```

> 更细的字段分别见：`project-agent.md`、`platforms.md`、`providers.md`、
> `display.md`、`advanced.md`、`gotchas.md`。
