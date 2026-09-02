---
name: heron-connect-config
description: 解释并生成 heron-connect 的 TOML 配置。当用户需要配置/修改 heron-connect（config.toml / config.example.toml），或询问任何配置项怎么写时使用——project、agent、platform、provider、display、management、bridge、webhook、cron、heartbeat、commands/aliases、banned_words、speech/tts、auto_compress、multi-workspace、多 project、dashboard/项目大盘（业务数据产出 insights.json、HTML 看板、reports 归档、no_delivery 无投递 cron、{{dashboard.*}} 模板变量）等。
metadata:
  project: heron-connect
  category: config
  version: 1.1.8
---

# Heron Connect 配置 Skill

heron-connect 把本地 AI 编码 Agent（Claude Code / CodeBuddy / Codex / Gemini 等）
连接到聊天平台（飞书 / Telegram / Discord / Slack / 钉钉 / 企微 / QQ / LINE 等），
**所有配置由一个 TOML 文件描述**。

本 Skill 对应 heron-connect **v1.1.8**（版本号与 heron-connect 同步，见本文件 frontmatter 的 `metadata.version`，由 `scripts/sync-version.sh` 维护）。

## 能力索引（给模型的导航）

> **读取方式**：先看下表，按用户需求定位到**唯一一个** `references/*.md`，只加载那个文件，
> 不要一次读全部。下表覆盖 heron-connect 全部配置能力。

| # | 能力 / 用户问什么 | 加载文件 |
|---|------------------|---------|
| 1 | 基础规则、最小配置、环境变量、**从零开始** | `references/quickstart.md` |
| 2 | `[[projects]]`、agent type/options/mode、**多 project**、`inject_sender` | `references/project-agent.md` |
| 3 | 接入 IM 平台（飞书/Telegram/Discord/Slack/钉钉/企微/QQ/LINE…）、**虚拟 `web` 平台** | `references/platforms.md` |
| 4 | API Provider、模型切换（全局 `[[providers]]` / 项目内 / `/provider`） | `references/providers.md` |
| 5 | 消息展示 `[display]`、thinking/tool、覆盖优先级、限流/流式预览 | `references/display.md` |
| 6 | Web 后台、Bridge、Webhook、`[[commands]]`/`[[aliases]]`/`banned_words`、`[speech]`/`[tts]`、cron、heartbeat、多用户 ACL、admin_from、auto_compress/observe/multi-workspace/relay | `references/advanced.md` |
| 7 | 排查报错、常见坑、排错命令 | `references/gotchas.md` |
| 8 | 项目大盘 `[dashboard]`、业务数据产出（insights.json/HTML 看板/reports 归档）、`no_delivery` 无投递 cron、`{{dashboard.*}}` 模板变量、`/dashboard` 命令 | `references/dashboard.md` |

**脚本**：
- 后台启/停/重启某份 toml：`scripts/service.sh --config <path> <start|stop|restart|status|logs>`
- 同步版本号（发版后跑）：`scripts/sync-version.sh`

## 极简快速上手（Claude Code + 飞书）

```bash
# 1. 首次运行生成默认配置
heron-connect
# 生成 ~/.heron-connect/config.toml，编辑后启动
heron-connect --config ./config.toml
```

最简 `config.toml`：

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"
[projects.agent.options]
work_dir = "/absolute/path/to/project"
mode = "default"

[[projects.platforms]]
type = "feishu"
[projects.platforms.options]
app_id = "cli_xxx"
app_secret = "${FEISHU_APP_SECRET}"
```

详见 `references/quickstart.md`。

## 权威参考

- 仓库根目录 `config.example.toml` —— 所有字段的最权威来源
- `docs/usage.md`、`INSTALL.md`、`docs/management-api.md`、`docs/bridge-protocol.md`
- 本 Skill 版本同步：`scripts/sync-version.sh`（从 heron-connect 读版本）
- 后台运行配置：`scripts/service.sh`（封装 `heron-connect daemon`，start/stop/restart/status/logs）
