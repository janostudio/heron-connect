# 项目大盘业务数据契约（InsightPayload）—— 权威规范

> 本文档是 heron-connect 项目大盘「业务结构化区」数据格式的**唯一权威规范**。
> 业务侧（如 auto_bugfix 的 `gen_insights.py`）产出 `dashboards/insights.json` 时，必须严格对照本文档的字段类型与必填性实现。
> 本文档与源码 `web/src/api/dashboard.ts` 的类型定义一一对应；若两者冲突，以源码为准（本文档随源码同步维护）。

## 1. 读取位置

- 文件：`<project work_dir>/dashboards/insights.json`（`[dashboard] insights_path` 可配，默认 `dashboards/insights.json`）
- 存在即生效，无需注册/重启；每次**全量覆盖写**（不是追加）
- 文件缺失 → 大盘业务区自动隐藏，不报错

## 2. 顶层结构（InsightPayload）

```jsonc
{
  "version": 1,                        // 可选，schema 版本，当前 1
  "generated_at": "2026-09-02T01:00:00+08:00",  // 可选，生成时间
  "generated_by": "daily-workreport",  // 可选，溯源（cron id / 脚本名）
  "period": { "start": "2026-09-01", "end": "2026-09-02" },  // 可选，覆盖窗口（展示标注用，格式 YYYY-MM-DD）
  "cards":  [ /* InsightCard[]，可选，见 §3 */ ],
  "sessions": [ /* InsightSession[]，可选，见 §4 */ ]
}
```

字段全可选（空对象 `{}` 也合法），但**至少要有 `cards` 或 `sessions` 之一**，否则业务区无内容。

## 3. cards（InsightCard[]）—— 顶部 KPI 卡

```jsonc
{
  "label": "修复PR",        // 必填，卡标题
  "value": 3,               // 必填，number 或 string
  "unit": "个",             // 可选，展示在 value 后
  "tone": "good"            // 可选，good | info | warn | error（决定卡色；未知值忽略）
}
```

## 4. sessions（InsightSession[]）—— 会话级分析行（核心）

```jsonc
{
  "agent_session_id": "b0d6bd33-...",   // 跳转键①（可选）：CLI 会话 UUID = jsonl 文件名
  "session_id": "conv-xxx",             // 跳转键②（可选）：heron-connect 引擎会话 ID
  "title": "修复登录 502",              // 【必填】行标题，绝不允许 null / 空字符串
  "summary": "根因是网关超时未重试",     // 可选，一句话结论
  "metrics": [                          // 可选，InsightMetric[] 数组（不是对象！）
    { "label": "input",  "value": 52394789 },
    { "label": "cached", "value": 50829968, "unit": "tokens" }
  ],
  "tags": [                             // 可选，InsightTag[]（string 或 {text,tone}）
    "dag",
    { "text": "已修复", "tone": "good" },
    { "text": "已整理", "tone": "info" }
  ],
  "tone": "good",                       // 可选，行级状态 good | info | warn | error
  "detail": "reports/20260901/token-daily.html"  // 可选，本行详细报告相对路径（经 files API 打开）
}
```

### 4.1 字段类型精确说明（对照 dashboard.ts）

| 字段 | TypeScript 类型 | 必填 | 说明 |
|---|---|---|---|
| `agent_session_id` | `string?` | 否 | 跳转键①。业务脚本手里的 CLI 会话 UUID（jsonl 文件名去掉 `.jsonl`） |
| `session_id` | `string?` | 否 | 跳转键②。引擎侧会话 ID。**两个键至少给一个**，命中即可点击跳聊天 |
| `title` | `string` | **是** | 行标题。**绝不允许 null 或空串**——前端直接用它当显示文本 |
| `summary` | `string?` | 否 | 一句话结论 |
| `metrics` | `InsightMetric[]?` | 否 | **数组**。元素 `{label, value, unit?}`。**不是 `{input, cached, output}` 对象** |
| `tags` | `InsightTag[]?` | 否 | 元素是 `string` 或 `{text, tone}` |
| `tone` | `string?` | 否 | `good \| info \| warn \| error`（未知值忽略，前端不崩） |
| `detail` | `string?` | 否 | 相对项目根的报告路径 |

### 4.2 跳转键语义（关键）

- 两个跳转键命中其一，该行可点击 → 跳 `/chat/<project>/<session_id>`（引擎侧会话）。
- **`agent_session_id` 必须填真实 CLI 会话 UUID**。**禁止混入 subagent 假会话**（CodeBuddy 子 agent 的 jsonl 文件名形如 `agent-xxxx`，不是 CLI 主会话）——这些在引擎侧没有对应会话，跳转必然失效。业务脚本扫描 jsonl 时必须过滤掉 `agent-` 前缀的文件。
- 两个键都未命中（纯 CLI 会话、未经过引擎）→ 行不可跳转，仅 `detail` 链接可用。这是合法场景，但**默认应尽量让 agent_session_id 对齐引擎真实会话**。

### 4.3 metrics 渲染

- 前端 `row.metrics.map((m) => ...)` 逐元素渲染 `{label}: {value}{unit}`。
- **必须是数组**。若写成对象，前端 `.map` 取到 undefined，整行 metrics 静默不显示。

### 4.4 tags 语义中立

- 引擎**不理解 tag 含义**，只渲染。纯字符串 = 中性灰徽章；`{text, tone}` = 彩色徽章（tone ∈ good/info/warn/error）。
- 点击 tag 可前端过滤列表。

## 5. 反例（auto_bugfix 实际踩的坑）

```jsonc
// ❌ 错误：metrics 是对象
"metrics": {"input": 52394789, "cached": 50829968, "output": 132682}

// ❌ 错误：title 是 null
"title": null

// ❌ 错误：agent_session_id 混入 subagent 假会话
"agent_session_id": "agent-a1b5aa1a"

// ❌ 错误：period 用了 window_days
"window_days": 1

// ✅ 正确
"metrics": [{"label":"input","value":52394789},{"label":"cached","value":50829968}],
"title": "修复登录 502",
"agent_session_id": "b0d6bd33-0acf-49d3-8532-f26709824845",
"period": {"start":"2026-09-01","end":"2026-09-02"}
```

## 6. no_delivery 模式（纯产出数据、不推送）

`CronJob` 支持 `no_delivery: true`：agent 正常执行并产出文件，但**不推送到任何平台**，`session_key` 留空即可（会被忽略）。

```jsonc
{
  "id": "daily-workreport",
  "project": "auto-bugfix",
  "session_key": "",        // 留空，no_delivery 下忽略
  "no_delivery": true,      // 关键：无投递模式
  "cron_expr": "0 1 * * *",
  "prompt": "...",
  "session_mode": "new_per_run"
}
```

**不要**用 `NEVER:SEND:TO:USER` 这类假 session_key 占位——引擎第一步就按前缀找 platform，找不到直接报错 `platform "NEVER" not found`，任务根本不执行。

## 7. 报告中心（reports 归档）契约

报告中心扫描 `[dashboard] reports_dir`（默认 `reports`），自动收录 `.html`/`.md`，可配可选 manifest：

```jsonc
// reports/20260901/token-daily.manifest.json（可选）
{ "title": "Token 消耗日报", "type": "token", "generated_by": "daily-workreport" }
```

- manifest 可选，无 manifest 也能列出（标题=文件名，日期从 `YYYY-MM-DD`/`YYYYMMDD` 目录名推断）
- 报告中心是「历史归档浏览」，与大盘业务区（insights.json）是两个独立入口
