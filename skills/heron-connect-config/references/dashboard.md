# 项目大盘（[dashboard]）与业务报告托管

heron-connect 的项目大盘（Web → 项目卡片 → `/projects/<name>/overview`）采用**结构化梯度**展示：

1. **引擎统计区**——connect 自动采集（会话数/轮次/Token/工具调用），零配置；
2. **业务结构化区**——业务侧把分析结论写成 `dashboards/insights.json`（InsightPayload）；
3. **HTML 兜底区**——自由看板 `dashboards/index.html`（iframe 嵌入，接收时间筛选参数）。

引擎不理解业务字段含义，只负责展示——**字段名固定，语义自由**。本功能完全通用，适用于任何项目（代码助手、客服 bot、数据管道监控等），不是某个业务专用。

> ⚠️ **字段级权威契约见 `docs/dashboard-contract.md`**（随源码同步维护）。本文件是概要，实现前以契约文档为准。

## 两套独立的产出机制（务必分清）

业务侧有两个**互不依赖、用途不同**的产出通道，不要混淆：

| | ① 业务结构化区 | ② 报告中心 |
|---|---|---|
| 数据文件 | `dashboards/insights.json`（**单个** JSON） | `reports/` 下**多个** `.html`/`.md` |
| 配置键 | `insights_path` | `reports_dir` |
| 数据形态 | 结构化（session 列表 + cards） | 自由文档 |
| 引擎理解度 | 结构已知，渲染成**可交互会话行**（跳聊天/标签/指标列） | 黑盒，只做**列表 + 预览** |
| 展示入口 | 大盘页业务区 | 侧边栏独立 `/reports` 页 |
| 更新方式 | 每次全量覆盖写**一个文件** | 每次追加落**新文件**（按日期目录） |
| 用途 | 「哪些会话在干什么」的结构化总览 | 「历史报告」的归档浏览 |

**判断该产哪个**：要「引擎读得懂、能跳转、能聚合」的结构化数据 → 写 insights.json；要「保存一份人类可读的文档/图表」→ 落 reports/。两者可以同时产，各走各的入口。

## 配置（[dashboard]）

```toml
[dashboard]
enabled = true                 # 总开关；false = 整体关闭（采集/大盘/报告中心）
collect = true                 # true = connect 自己采集；false = 纯展示（业务产出数据）
retention_days = 90            # metrics JSONL 保留天数
include_message_excerpt = true # 会话行带首条消息摘要（隐私开关）
max_topics = 10                # 报告中会话列表截断条数
insights_path = "dashboards/insights.json"  # 业务结构化数据（存在即显示）
html_path = "dashboards/index.html"         # 业务 HTML 看板（iframe 兜底）
reports_dir = "reports"                      # 报告归档目录（报告中心扫描）
public_base_url = ""                         # IM 推送链接前缀；空 = management 监听地址
```

所有路径相对项目 work_dir；全部字段有默认值，零配置可用。

## 业务产出什么（决策表）

| 任务 | 产出文件 | 说明 |
|---|---|---|
| 每日/每周工作总结 | `dashboards/insights.json` | InsightPayload，见下 |
| 深度分析看板（图表/交互） | `dashboards/index.html` | 自包含单文件 HTML；读 URL 参数 `period/start/end` 渲染 |
| 归档报告（日报 HTML/MD） | `reports/<yyyy-mm-dd>/<slug>.html\|.md` + `<slug>.manifest.json` | 报告中心自动收录 |
| 定时生成 | `/cron add ...` | prompt 可用 `{{dashboard.today}}` 等模板变量注入统计 |
| 只产数据、不推送 | cron `no_delivery: true` + `session_key: ""` | agent 执行产出文件，但不发任何消息 |

## InsightPayload 契约（dashboards/insights.json）

```jsonc
{
  "version": 1,
  "generated_at": "2026-09-02T01:00:00+08:00",
  "generated_by": "daily-report",              // 溯源：cron id / 脚本名，业务自定义
  "period": { "start": "2026-09-01", "end": "2026-09-02" },
  "cards": [                                   // KPI 卡（可选，label 业务自定义）
    { "label": "完成任务", "value": 3, "unit": "个", "tone": "good" }
  ],
  "sessions": [                                // 会话级分析（核心）
    {
      "agent_session_id": "<CLI 会话 UUID>",   // 跳转键①：jsonl 文件名（去掉 .jsonl）
      "session_id": "<引擎会话 ID>",           // 跳转键②：两者至少给一个
      "title": "修复登录超时",                  // 【必填】不允许 null / 空串
      "summary": "根因是网关超时未重试",         // 可选
      "metrics": [                             // 可选，InsightMetric[] 数组（不是对象！）
        { "label": "input",  "value": 52394789 },
        { "label": "耗时",    "value": 32, "unit": "min" }
      ],
      "tags": ["urgent", { "text": "已完成", "tone": "good" }],  // 语义中立，业务自定义
      "tone": "good",                          // 可选，good|info|warn|error
      "detail": "reports/20260901/report.html"  // 可选，本行详细报告相对路径
    }
  ]
}
```

> 上面的「完成任务」「已完成」「urgent」等都是**示例占位**——标签/卡片语义完全由业务自定义，引擎只渲染不解释。你的业务用什么词就写什么词。

### 关键规则

- **两个跳转键至少给一个**：业务脚本通常只有 CLI 会话 UUID（jsonl 文件名）→ 填 `agent_session_id`。
- **`agent_session_id` 禁止混入 subagent 假会话**：CodeBuddy 子 agent 的 jsonl 文件名形如 `agent-xxxx`，不是 CLI 主会话，引擎侧无对应会话，跳转必然失效。扫描 jsonl 时**必须过滤 `agent-` 前缀的文件**。
- `title` **必填**，绝不允许 null；`metrics` 是 **`[{label, value, unit}]` 数组**，不是 `{input, cached, output}` 对象（写成对象会导致前端 `.map` 失败、整行 metrics 静默不显示）。
- `tags` 语义中立：写什么引擎就渲染什么（`已完成`/`待处理`/`urgent` 等业务自定义状态）；纯字符串=中性灰，`{text, tone}`=彩色徽章（good/info/warn/error）。点击 tag 可过滤列表。
- 文件存在即生效，无需注册/重启；每次全量覆盖写（不是追加）。

## 报告归档契约（reports/）

```
reports/<yyyy-mm-dd>/<slug>.html|.md       ← 报告本体
reports/<yyyy-mm-dd>/<slug>.manifest.json  ← 可选：{"title","type","generated_by"}
```

无 manifest 也能列出（标题=文件名，日期从目录名推断）；`type` 供报告中心过滤。

## HTML 看板契约（dashboards/index.html）

- **自包含单文件**（inline JS/CSS）；
- 引擎把时间筛选透传为 URL query：`?token=…&period=day|week|month|custom&start=YYYY-MM-DD&end=YYYY-MM-DD`——JS 读 `location.search` 渲染（服务端忽略除 token 外的参数）；
- 引用外部数据文件时注意：iframe 内相对路径请求不带 token，P0 请内联数据。

## cron 模板变量

| 变量 | 含义 |
|---|---|
| `{{dashboard.today}}` / `{{dashboard.yesterday}}` | 今日 / 昨日统计（Markdown） |
| `{{dashboard.week}}` / `{{dashboard.last_week}}` | 本周 / 上周 |
| `{{dashboard.*:json}}` | 同名变量原始 JSON |

## no_delivery 模式（只产数据、不推送）

cron 任务只需产出文件、不需向任何群发消息时，用 `no_delivery: true` + 空 `session_key`：

```jsonc
{
  "id": "daily-report",
  "project": "my-project",
  "session_key": "",      // 留空，no_delivery 下忽略
  "no_delivery": true,    // 关键：无投递模式
  "cron_expr": "0 1 * * *",
  "prompt": "...",
  "session_mode": "new_per_run"
}
```

- agent 正常执行（spawn session → Send prompt → 产出文件），outbound 全静默；
- **不要**用 `NEVER:SEND:TO:USER` 这类假 session_key 占位——引擎第一步按前缀找 platform，找不到直接报 `platform "NEVER" not found`，任务根本不执行；
- 需 heron-connect ≥ v1.1.31。

## IM 查询命令

`/dashboard [today|week|yesterday|lastweek]` —— 卡片平台渲染卡片，其他平台 Markdown。

## 统计口径说明

insights.json 是业务口径；`{{dashboard.*}}` 是引擎入口口径（只含经平台进来的轮次），两者互补可并列引用。
