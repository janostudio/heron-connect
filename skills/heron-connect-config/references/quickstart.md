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

## 环境变量参考

### 配置内替换

任意字符串值支持 `${VAR_NAME}`，如 `token = "${TELEGRAM_BOT_TOKEN}"`。不要明文写密钥。

### heron-connect 自身读取的环境变量

| 变量 | 用途 |
|------|------|
| `CC_LOG_FILE` | 日志文件路径（**最高优先级**，覆盖 `[log].file`） |
| `CC_LOG_MAX_SIZE` | 日志单文件大小阈值（MB），覆盖 `[log].max_size_mb` |
| `CC_LOG_RETENTION_DAYS` | 归档保留天数，覆盖 `[log].retention_days` |
| `CC_CONFIG_PATH` | 诊断（doctor）时用它定位 config |

优先级：**`CC_*` 环境变量 > config `[log]` > 默认值**（见 advanced.md 日志段）。

### 注入给 agent / CLI 自动读取的变量

这些变量由 heron-connect 启动 agent 时**自动注入**（`CC_PROJECT` / `CC_SESSION_KEY` /
`cc_data_dir` / `cc_project`），agent 内部跑 `heron-connect cron/send/relay/agent-sid`
等命令时会自动读取它们补齐 `--project` / `--session-key`，无需手动传：

| 变量 | 说明 |
|------|------|
| `CC_PROJECT` | 当前 project 名 |
| `CC_SESSION_KEY` | 当前会话 key（`平台:用户:会话`） |
| `cc_data_dir` / `cc_project` | 内部注入 agent options，一般无需关心 |

### 启动相关

| 变量 | 说明 |
|------|------|
| `CLAUDECODE` | 在 Claude Code 会话内启动子进程前需 `unset CLAUDECODE`，否则 Claude Code 拒绝作为子进程启动（见 INSTALL / gotchas） |

## 最小可运行配置（Claude Code + 飞书）

```toml
[log]
level = "info"                     # debug | info | warn | error
# file = "logs/app.log"            # 日志文件路径；空 = 前台打 stdout（daemon 默认写文件）
# max_size_mb = 10                 # 单文件大小阈值（MB），超限切分归档
# retention_days = 7               # 归档保留天数（按天切分 + 自动清理）

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
