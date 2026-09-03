# heron-connect 项目大盘与统计 (Dashboard) 使用指南

## 概述

项目大盘回答"今天/本周干了什么"：多少会话、多少轮对话、token 消耗、谁在哪个平台活跃、每个会话在做什么——并托管业务自产的深度分析报告。

核心设计：**引擎通用、业务外置**。connect 只产出客观数据（引擎统计）并提供展示容器；报告怎么写、什么时候出，由业务通过 cron prompt 决定。

```
项目卡片 → 项目大盘（/projects/<name>/overview）
  ├─ 右上角：【💬 进入聊天】【⚙️ 项目设置】
  ├─ 区① 标准统计区（引擎自动采集：会话/轮次/Token/缓存/错误/活跃分布）
  ├─ 区② 业务结构化区（dashboards/insights.json，业务 cron 生成）
  └─ 区③ HTML 兜底区（dashboards/index.html，业务自由内容，iframe）
```

**渐进增强**：三区各自独立点亮，页面只探测"数据是否存在"；全无数据时大盘退化为项目概览，升级零风险。

## 配置

唯一配置段 `[dashboard]`（config.toml），全部字段有默认值，**零配置可用**：

```toml
[dashboard]
enabled = true    # 主开关：false = 整体关闭（采集、大盘、报告中心）
collect = true    # 数据来源：true = connect 自己采集；
                  #   false = 纯展示模式（不采集，数据完全由业务产出）
retention_days = 90
include_message_excerpt = true
max_topics = 10
insights_path = "dashboards/insights.json"
html_path = "dashboards/index.html"
reports_dir = "reports"
public_base_url = ""
```

| enabled | collect | 效果 |
|---|---|---|
| true | true（默认） | 引擎采集 + 大盘三区 + 报告中心 + `{{dashboard.*}}` |
| true | false | 纯展示：无引擎统计；业务区（insights/HTML）与报告中心照常 |
| false | — | 整体关闭，页面回到现状 |

## 指标采集

- 存储位置：`<data_dir>/metrics/turns-YYYY-MM-DD.jsonl`（append-only，按天滚动）；
- 每轮对话记一条（维度+计数，**不含消息正文**）：项目/会话/平台/agent/用户/触发来源（user/cron）/时长/输入输出 token/工具调用；
- token 兜底：agent 未回报 usage 时用上下文估算，标记 `tokens_estimated`；
- 保留期 `retention_days`（默认 90 天），启动时自动清理过期文件。

## IM 命令：/dashboard

```
/dashboard              → 今日统计（当前项目）
/dashboard week         → 本周
/dashboard yesterday    → 昨天
/dashboard lastweek     → 上周
```

卡片平台（飞书等）渲染交互卡片；其他平台渲染 Markdown 文本。

## cron 模板变量：{{dashboard.*}}

CronJob 的 prompt 中可注入统计数据（执行时替换，未识别变量原样保留）：

| 变量 | 含义 |
|---|---|
| `{{dashboard.today}}` | 今日（Markdown 表格） |
| `{{dashboard.yesterday}}` | 昨日（早报场景） |
| `{{dashboard.week}}` | 本周（周一至今） |
| `{{dashboard.last_week}}` | 上周（周报对比） |
| `{{dashboard.*:json}}` | 同名变量原始 JSON |

示例——每日 18:00 日报：

```
/cron add 0 18 * * 1-5 请基于以下统计写一份今日日报，按【完成事项】【进行中】【风险与异常】三段组织：{{dashboard.today}}
```

## 业务报告接入

业务侧有两套**独立、用途不同**的产出通道，接入前先分清：

| | ① 业务结构化区 | ② 报告中心 |
|---|---|---|
| 文件 | `dashboards/insights.json`（单个 JSON） | `reports/` 下多个 `.html`/`.md` |
| 形态 | 结构化（session 列表 + cards） | 自由文档 |
| 能力 | 可跳聊天/标签/指标列 | 列表 + 预览 |
| 用途 | 「哪些会话在干什么」的结构化总览 | 「历史报告」归档浏览 |

要结构化、可交互 → 写 insights.json；要存人类可读文档 → 落 reports/。两者可同时产，各走各的入口。

### 1. 结构化业务数据（dashboards/insights.json）

业务 cron 把会话级分析结论写成固定 schema 的 JSON，大盘"业务结构化区"原生渲染（与引擎会话列表合并、支持标签过滤、命中会话可点击跳聊天）。

> **⚠️ 字段级契约以 `docs/dashboard-contract.md` 为唯一权威规范**——字段类型、必填性、跳转键语义、subagent 过滤要求、no_delivery 用法都在那里，业务侧实现前必须先读它。下面只列要点，不重复完整契约。

要点：

- 会话行带**两个跳转键**（`agent_session_id` CLI 会话 ID / `session_id` 引擎会话 ID）至少一个，命中即可点击直达聊天记录；
- `metrics` 是 **`InsightMetric[]` 数组**（`{label, value, unit}`），**不是对象**；`title` **必填**，不允许 null；
- `tags` 语义中立（`已完成/待处理` 等业务自定义状态随便写，引擎只画徽章）；纯字符串中性色，`{text, tone}` 彩色（good/info/warn/error）；
- 文件存在即生效，无需注册、无需重启。

### 2. HTML 看板（dashboards/index.html）

自由深度分析：引擎以 iframe 嵌入，并把当前时间筛选透传为 URL query（`?period=day|week|month|custom&start=…&end=…`），业务 HTML 读 `location.search` 自行渲染。**自包含单文件 HTML**（inline JS/CSS）可直接工作。

### 3. 报告归档（reports/）

```
reports/<yyyy-mm-dd>/<slug>.html|.md        ← 报告本体
reports/<yyyy-mm-dd>/<slug>.manifest.json   ← 可选：{"title","type","generated_by"}
```

Web「报告中心」（侧边栏）自动收录，支持 type 过滤、HTML 预览/全屏、Markdown 查看。无 manifest 也能列出（标题=文件名）。

## REST API

```
GET /api/v1/dashboard?period=day|week|month|custom&date=&start=&end=&project=all|<name>
GET /api/v1/dashboard/summary?project=
GET /api/v1/dashboard/sessions/<project>/<session_id>     # 单会话 turn 级明细
GET /api/v1/dashboard/settings
GET /api/v1/reports?project=&type=&limit=
```

均需 management API 鉴权（Bearer token）。`collect=false` 时统计类端点返回 404（前端据此隐藏区域）。

## 隐私

- metrics 不落消息正文，只记维度与计数；
- `topics.first_message`（首条用户消息摘要，截断 100 字符）是唯一文本字段，`include_message_excerpt = false` 可关；
- 报告/看板文件沿用 management server 统一鉴权，不额外暴露。

## 设计文档

完整设计（数据格式契约、双轨道架构、展示分层）见 [stats-dashboard-design.zh-CN.md](stats-dashboard-design.zh-CN.md)。
