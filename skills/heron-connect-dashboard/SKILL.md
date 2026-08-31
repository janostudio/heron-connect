---
name: heron-connect-dashboard
description: 为 heron-connect 项目大盘产出业务数据与报告。当用户需要生成每日/每周工作总结、写 dashboards/insights.json（InsightPayload）、生成 HTML 看板、落 reports/ 归档报告、写 manifest，或配置 /dashboard 统计与 {{dashboard.*}} cron 模板变量时使用。
metadata:
  project: heron-connect
  category: reporting
  version: 1.1.0
---

# Heron Connect 项目大盘 Skill

heron-connect 的项目大盘（Web → 项目卡片 → `/projects/<name>/overview`）采用**结构化梯度**展示：

1. **引擎统计区**——connect 自动采集（会话数/轮次/Token/工具调用），零配置；
2. **业务结构化区**——你（业务侧）把分析结论写成 `dashboards/insights.json`（本 Skill 的核心产出）；
3. **HTML 兜底区**——自由看板 `dashboards/index.html`（iframe 嵌入，接收时间筛选参数）。

引擎不理解业务字段含义，只负责展示——**字段名固定，语义自由**。

## 何时产出什么（决策表）

| 任务 | 产出文件 | 说明 |
|---|---|---|
| 每日/每周工作总结 | `dashboards/insights.json` | InsightPayload，见下 |
| 深度分析看板（图表/交互） | `dashboards/index.html` | 自包含单文件 HTML；需读 URL 参数 `period/start/end` 自行渲染 |
| 归档报告（日报 HTML/MD） | `reports/<yyyy-mm-dd>/<slug>.html\|.md` + `<slug>.manifest.json` | 报告中心自动收录 |
| 定时生成 | `/cron add ...`（现有 cron 体系） | prompt 里可用 `{{dashboard.today}}` 等模板变量注入引擎统计 |

## InsightPayload 契约（dashboards/insights.json）

```jsonc
{
  "version": 1,
  "generated_at": "2026-08-31T18:05:00+08:00",
  "generated_by": "daily-workreport",          // 溯源：cron job id 或脚本名
  "period": { "start": "2026-08-30", "end": "2026-08-31" },  // 覆盖窗口，展示标注用
  "cards": [                                    // 顶部 KPI 卡（可选）
    { "label": "修复 PR", "value": 3, "unit": "个", "tone": "good" },
    { "label": "分析后跳过", "value": 2, "unit": "个", "tone": "neutral" }
  ],
  "sessions": [                                 // 会话级分析（核心）
    {
      "agent_session_id": "<CLI 会话 ID>",       // 跳转键①：你从 CLI 日志/会话列表能拿到的 ID
      "session_id": "<引擎会话 ID>",             // 跳转键②：两者至少给一个，命中即可点击跳聊天
      "title": "修复登录 502",
      "summary": "根因是网关超时未重试",          // 一句话结论
      "metrics": [                              // 自定义指标：label/value 通用渲染
        { "label": "Token", "value": 45000 },
        { "label": "耗时", "value": 32, "unit": "min" }
      ],
      "tags": ["bugfix", { "text": "已修复", "tone": "good" }, { "text": "已整理", "tone": "info" }],
      "tone": "good",                           // 行状态色 good|info|warn|error（可选）
      "detail": "reports/20260831/token-day.html"  // 本行详细 HTML，相对项目根（可选）
    }
  ]
}
```

规则：

- **tags 语义中立**：`已修复/已整理/归档验收/跳过` 是业务工作流状态，写什么引擎就展示什么；纯字符串=中性灰，`{text, tone}`=彩色徽章；
- **两个跳转键至少给一个**：业务脚本通常只有 CLI 会话 ID（`.jsonl` 文件名）→ 填 `agent_session_id` 即可；
- schema 只增不改；多余字段被前端忽略；文件存在即生效，**无需注册、无需重启**；
- 每次全量覆盖写（不是追加）。

## 报告归档契约（reports/）

```
reports/<yyyy-mm-dd>/<slug>.html|.md       ← 报告本体
reports/<yyyy-mm-dd>/<slug>.manifest.json  ← 可选元数据
```

manifest：`{"title": "Token 消耗日报", "type": "token", "generated_by": "daily-workreport"}`。
无 manifest 也能列出（标题=文件名，日期从目录名推断）；`type` 供报告中心过滤。

## HTML 看板契约（dashboards/index.html）

- **自包含单文件**（inline JS/CSS——py-echarts/plotly 默认输出即是）；
- 引擎会把时间筛选透传为 URL query：`?token=…&period=day|week|month|custom&start=YYYY-MM-DD&end=YYYY-MM-DD`——你的 JS 读 `location.search` 自行渲染（服务端忽略除 token 外的参数）；
- 需要引用外部数据文件时注意：iframe 内相对路径请求不带 token，P0 请内联数据。

## cron 模板变量（prompt 里注入引擎统计）

| 变量 | 含义 |
|---|---|
| `{{dashboard.today}}` / `{{dashboard.yesterday}}` | 今日 / 昨日统计（Markdown 表格） |
| `{{dashboard.week}}` / `{{dashboard.last_week}}` | 本周 / 上周 |
| `{{dashboard.*:json}}` | 同名变量的原始 JSON |

示例：
```
/cron add 0 18 * * 1-5 请基于以下统计写今日日报，按【完成事项】【风险与异常】组织：{{dashboard.today}}
```

## 注意事项

- 所有路径相对**项目工作目录**（agent 的 work_dir）；
- Web 页面靠文件存在性点亮区域：insights.json/index.html 缺失 = 对应区域隐藏，不报错；
- IM 里 `/dashboard [today|week|yesterday|lastweek]` 可随时查询引擎统计；
- 统计口径：insights.json 是你的业务口径；`{{dashboard.*}}` 是引擎入口口径（只含经平台进来的轮次），两者互补可并列引用。
