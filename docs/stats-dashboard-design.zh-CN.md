# 每日/每周对话统计看板 — 设计文档

> 状态：Draft v1.1（待评审）
> 日期：2026-08-31（v1.1 增补"业务报告中心"轨道）
> 目标：回答"今天/本周干了什么"——多少会话、多少轮对话、token 消耗、谁在哪些平台/项目上活跃、每个会话大致在做什么；并为业务自产报告（如 auto_bugfix 每日复盘的 HTML/MD 产物）提供统一的 Web 托管与入口。

## 1. 背景与现状缺口

| 能力 | 现状 | 缺口 |
|---|---|---|
| token 用量 | 已采集进 `AgentEvent.InputTokens/OutputTokens`（core/message.go:210），turn 结束仅打一行 slog（core/engine_turn.go:1274） | **无持久化** |
| 会话数据 | JSON 快照有 CreatedAt/UpdatedAt/History（core/session.go） | 缺 turn 级维度（平台/用户/工具数/时长不在快照里） |
| 定时推送 | CronJob 基础设施完备（core/cron.go），prompt 型 job + 主动推送可用 | prompt 是静态文本，**无法注入统计数据** |
| Web 展示 | Dashboard 只有 version/uptime/平台数 + 最近会话 | 无统计页 |
| 业务报告（auto_bugfix 每日复盘产物 `reports/<日期>/*.html|md`） | cron 能生成；`GET /api/v1/files/...` 已带鉴权托管项目工作目录文件（mgmt_server.go:472） | **无索引、无 Web 入口**；现状靠企微群播全文，HTML 无法在群内展示、链接不可点 |

## 2. 设计原则：引擎通用、业务外置

引擎**不内置任何日报文案，也不理解业务报告内容**。两条轨道、每层契约独立：

```
┌──────────────────────── engine（通用，无业务语义）────────────────────────┐
│                                                                            │
│  turn 完成 ──► ① 采集层 TurnRecorder ──► metrics/turns-YYYY-MM-DD.jsonl   │
│  会话创建 ──┘     (append-only，按天滚动)                                     │
│                                                                            │
│  ② 聚合层 StatsAggregator（纯函数）                                          │
│     JSONL × 时间窗 × 范围 ──► UsageReport（versioned 规范 JSON）             │
│           │                                                                │
├───────────┼────────── 轨道 A：内置统计（引擎视角）──────────────────────────┤
│           ├──► REST API      ──► ③a Web 项目大盘 /projects/:name（原生统计+业务iframe）│
│           ├──► IM 命令 /stats ──► ③b Card 卡片（CardSender / 文本降级）     │
│           └──► cron 模板变量 {{dashboard.*}}                                    │
│                 └─► ④ 注入业务 prompt ──► agent 写叙事报告 ──► 正常消息推送  │
│                                                                            │
├──────────── 轨道 B：业务报告（内容引擎不理解）──────────────────────────────┤
│  业务 cron / 分析脚本 ──► ⑤ 报告产物落地 <work_dir>/reports/（约定目录）     │
│        └─► 引擎报告中心：⑥ 索引 API + Web /reports 页 + 推送链接化           │
│            （文件本体复用现有 GET /api/v1/files/<project>/<path>）           │
└────────────────────────────────────────────────────────────────────────────┘
```

业务方（写 prompt 的人）决定**什么时候出报告、报告怎么写、报告长什么样**；引擎只保证三件事：

1. **统计契约稳定**：`UsageReport` 带 version 字段，格式演进向后兼容；
2. **数据注入通道**：cron prompt 中的 `{{dashboard.*}}` 模板变量；
3. **报告托管契约**：约定目录 + 可选 manifest，引擎提供索引、预览、链接推送。

## 3. 数据格式（轨道 A：内置统计）

### 3.1 原始记录 TurnRecord（JSONL，按天分文件）

存储：`<data_dir>/metrics/turns-YYYY-MM-DD.jsonl`，append-only，每行一条 JSON。
记录**只含维度和计数，不含消息正文**（隐私最小化，见 §10）。

```jsonc
// kind=turn：一轮对话完成（含 cron 触发的轮次）
{
  "ts": "2026-08-31T14:30:05+08:00",   // turn 开始时间
  "kind": "turn",
  "project": "default",                 // 所属项目（engine 名）
  "session_key": "feishu:ou_xxx:oc_yyy",
  "session_id": "conv-abc123",           // 引擎会话 ID（Web 跳转键）
  "agent_session_id": "cli-uuid-…",      // agent 后端会话 ID（业务脚本手里的钥匙，如 CodeBuddy .jsonl 文件名）
  "session_name": "修复登录 bug",         // 记录时刻的会话名（自动标题随轮更新）
  "platform": "feishu",
  "agent": "claudecode",
  "user_id": "ou_xxx",
  "user_name": "张三",
  "trigger": "user",                    // "user" | "cron"
  "duration_ms": 45000,
  "input_tokens": 12000,
  "output_tokens": 3500,
  "cache_read_tokens": 8000,            // 可选：SDK 回报的缓存命中 token（未回报恒为 0，不参与估算兜底）
  "cache_write_tokens": 500,            // 可选：缓存写入 token
  "tokens_estimated": false,            // true = SDK 未回报，用上下文估算兜底
  "tool_calls": 8,
  "tools": { "Bash": 3, "Read": 4, "Edit": 1 },
  "response_chars": 2100,
  "silent": false,                      // 静默回复仍计入（有真实消耗）
  "error": ""
}

// kind=session_created：新会话创建（每会话一条，用于"新会话数"统计）
{
  "ts": "2026-08-31T09:00:01+08:00",
  "kind": "session_created",
  "project": "default",
  "session_key": "feishu:ou_xxx:oc_yyy",
  "session_id": "conv-abc123",
  "platform": "feishu",
  "user_id": "ou_xxx",
  "user_name": "张三"
}
```

### 3.2 聚合报告 UsageReport（规范 JSON，versioned）

聚合器为纯函数：`Aggregate(records, window, scope) → UsageReport`。这是轨道 A 对外的**唯一数据契约**，Web / IM / LLM 注入三端共用。

```jsonc
{
  "version": 1,
  "period": {
    "type": "day",                      // "day" | "week" | "month" | "custom"
    "start": "2026-08-31T00:00:00+08:00",
    "end": "2026-09-01T00:00:00+08:00",
    "label": "2026-08-31"               // week 为 "2026-W36"
  },
  "scope": { "project": "all" },        // "all" 或具体项目名
  "generated_at": "2026-08-31T18:00:00+08:00",
  "totals": {
    "sessions_active": 12,              // 窗口内有 ≥1 轮的会话数
    "sessions_new": 5,                  // 窗口内新建会话数
    "turns": 87,
    "turns_user": 80,
    "turns_cron": 7,
    "input_tokens": 1234567,
    "output_tokens": 234567,
    "cache_read_tokens": 800000,        // agent 未回报的字段恒为 0，前端按非零才展示
    "cache_write_tokens": 50000,
    "total_tokens": 1469134,
    "tokens_estimated": false,          // 窗口内任一轮为估算即 true
    "active_ms": 3900000,               // Σ duration_ms
    "tool_calls": 234,
    "errors": 2
  },
  // 时间分布：day → 24 个小时桶（"00:00".."23:00"）；week/month/custom → 按天桶（custom 上限 90 天）
  "timeline": [
    { "label": "08:00", "turns": 3, "tokens": 12345, "tool_calls": 12 }
  ],
  // 分组维度统一形状：name + sessions + turns + total_tokens + tool_calls + errors + active_ms
  "by_project":  [ { "name": "default", "sessions": 5, "turns": 50, "total_tokens": 900000, "tool_calls": 150, "errors": 1, "active_ms": 2400000 } ],
  "by_platform": [ { "name": "feishu", "...": "同上" } ],
  "by_agent":    [ { "name": "claudecode", "...": "同上" } ],
  // "干了什么"的素材：按轮次排序的 Top 会话（默认 10 条）
  "topics": [
    {
      "session_id": "conv-abc123",
      "agent_session_id": "cli-uuid-…",   // 业务 InsightPayload 的合并键之一（§5.5）
      "name": "修复登录 bug",
      "project": "default",
      "platform": "feishu",
      "user_name": "张三",
      "turns": 12,
      "total_tokens": 45000,
      "tool_calls": 34,
      "active_ms": 1800000,
      "first_message": "帮我看看登录为什么偶尔 502",  // 首条用户消息截断 100 字符，可配置关闭
      "last_active": "2026-08-31T16:20:00+08:00"
    }
  ],
  "top_tools": [ { "name": "Bash", "calls": 50 } ],
  "top_users": [ { "name": "张三", "turns": 40, "total_tokens": 60000 } ]
}
```

**数据来源说明**：

- 计数与维度全部来自 JSONL（自包含，聚合不依赖会话快照）；
- `topics.name` 取该会话最后一条 turn 记录里的 `session_name`（自动标题会随轮更新）；
- `topics.first_message` 优先从会话快照 `History` 首条用户消息取，取不到则留空；受 `[dashboard].include_message_excerpt` 开关控制。

### 3.3 LLM 注入格式（Markdown 渲染规范）

`{{dashboard.*}}` 注入 prompt 时默认渲染为 Markdown（token 效率友好、LLM 可读性最佳）。渲染模板为**规范的一部分**，保证 agent 看到的结构稳定：

```markdown
# 统计数据：今日 2026-08-31（范围：全部项目，截至 18:00）

## 总览
- 会话: 12（新会话 5）｜轮次: 87（用户 80 / 定时任务 7）
- Token: 输入 1,234,567 / 输出 234,567（合计 1.47M）※部分为估算值
- 工具调用: 234 次｜错误: 2 次｜累计耗时: 65 分钟

## 会话列表（按轮次排序）
| 会话 | 项目 | 平台 | 用户 | 轮次 | Token | 最后活跃 |
|---|---|---|---|---|---|---|
| 修复登录 bug | default | feishu | 张三 | 12 | 45k | 16:20 |
| 重构 API 层 | default | web | 李四 | 30 | 900k | 17:45 |

## 分组
- 项目: default 50轮/900k · proj-b 37轮/569k
- 平台: feishu 60轮 · web 27轮
- 工具 Top: Bash 50 · Read 40 · Edit 30
```

## 4. 展示方式（轨道 A）

### 4.1 Web 项目大盘（新路由 `/projects/:name/overview`，主看板）

导航变更：项目列表/首页点击项目卡片 → 进入**项目大盘**。现有 `/projects/:name`（平台/模型/心跳/设置配置台）保留不动，大盘右上角两个入口：【💬 进入聊天】（原卡片直达行为）与【⚙️ 项目设置】；`/chat/:name` 仍可直达/收藏。

**渐进增强（presence-driven）——不存在"新旧两版页面"**：大盘三区各自独立点亮，页面只探测"数据是否存在"，与谁产出无关；三区全无数据时，大盘退化为项目概览（等价现状 overview），**升级零风险、灰度自然**：

| 区 | 点亮条件 | 数据形态 |
|---|---|---|
| 标准统计区 | `/api/v1/dashboard` 窗口内有记录（`[dashboard].collect` 开启且已产生 TurnRecord）；collect=false 时该区恒不显示，汇总/单会话由业务 insights.json 承担 | 引擎 API——时间筛选需实时聚合，固定文件表达不了 |
| 业务结构化区 | `insights_path` 文件存在且可解析 | **固定地址文件**——cron/脚本/手写产出皆可，connect 不关心 |
| HTML 兜底区 | `html_path` 文件存在 | **固定地址文件**，同上 |

时间筛选器仅随标准统计区出现（驱动统计区 + iframe 参数透传）；纯业务数据场景下 insights 区自带 period 标注，独立展示。

```
┌─ auto-bugfix ───────────────────────────────────── [💬 进入聊天] ┐
│ [日] [周] [月] [自定义 ▾ 2026-08-01 ~ 2026-08-31]                  │
│ ┌───────┐┌───────┐┌──────────────┐┌────────────┐┌───────┐        │
│ │会话 12 ││轮次 87 ││Token 1.4M     ││缓存 8.0k/0.5k││错误 2 │        │
│ │新 5    ││u80 c7 ││in 1.2M/out 235k││读/写        ││       │        │
│ └───────┘└───────┘└──────────────┘└────────────┘└───────┘        │
│ 活跃分布（时间桶：日=小时；周/月/自定义=天）▁▂▃▅▇█▅▂…             │
│ 时段内会话列表（topics，点击跳 /chat/:project/:id）…                │
├───────────────────────────────────────────────────────────────────┤
│ ▼ 业务分析（结构化 insights.json，React 原生渲染，行可点跳会话）      │
│ ● 修复登录 502 [bugfix·high-cost] Token 45k · 32min  ✓ → 💬        │
├───────────────────────────────────────────────────────────────────┤
│ ▼ 业务深度分析（HTML 兜底，固定地址 iframe，引擎不解析内容）          │
│ ┌───────────────────────────────────────────────────────────────┐ │
│ │ /api/v1/files/<project>/dashboards/index.html                │ │
│ │   ?token=…&period=month&start=2026-08-01&end=2026-08-31      │ │
│ │ ← 筛选参数透传，业务 HTML 读 location.search 自行渲染           │ │
│ └───────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────┘
```

- **标准统计区**：React 原生渲染 `UsageReport`（纯 CSS 条形图，不引入图表库），时间筛选日/周/月/自定义；
- **业务结构化区**：渲染 `[dashboard].insights_path` 指向的 InsightPayload（§5.5），与引擎 topics 按 `session_id`/`agent_session_id` 合并成一列表，命中跳转键的行可点击直达聊天；
- **业务深度区（HTML 兜底）**：固定地址 iframe（契约与参数透传见 §5.4）；`[dashboard].html_path` 未配置或文件不存在则不显示该区块；
- Dashboard 首页保留全局"今日摘要"StatCard 行（`GET /api/v1/dashboard/summary`，跨项目视角）。

### 4.2 IM 卡片（`/dashboard` 命令，按需查询）

```
/dashboard            → 今日（当前项目范围）
/dashboard week      → 本周
/dashboard yesterday → 昨天
/dashboard lastweek  → 上周
```

渲染走现有卡片体系：`Card{ Header(蓝) "📊 今日统计 · 2026-08-31", Markdown(总览), Divider, Markdown(Top 会话), Note(数据截至/估算说明) }`：

- 实现 `CardSender` 的平台（飞书等）→ 原生交互卡片；
- 其余平台 → 现有 `Card.RenderText()` 文本降级，零适配成本。

### 4.3 定时报告（cron + 模板变量，业务定制入口）

**这是轨道 A 业务接入的唯一契约点**。CronJob 结构不变，仅在执行时解析 Prompt 中的模板变量：

| 变量 | 含义 | 默认渲染 |
|---|---|---|
| `{{dashboard.today}}` | 今日 | §3.3 Markdown |
| `{{dashboard.yesterday}}` | 昨日（早报场景） | 同上 |
| `{{dashboard.week}}` | 本周（周一至今） | 同上 |
| `{{dashboard.last_week}}` | 上周（周报对比场景） | 同上 |
| `{{dashboard.*:json}}` | 同名变量 | 原始 UsageReport JSON |

规则：

- 解析时机：`ExecuteCronJob` 构造合成消息前（core/engine_cron.go:135 之前）；
- 作用域：job 所属项目的 engine；cron 触发的这轮 turn 自身也会产生消耗，注入文案注明"截至 HH:MM"；
- **未识别的 `{{...}}` 原样保留**（不破坏既有 prompt，向后兼容）。

示例——业务完全自定义报告风格：

```
# 每个工作日 18:00 日报（叙事型，agent 自由发挥）
/cron add 0 18 * * 1-5 请基于以下统计写一份今日日报，按【完成事项】【进行中】【风险与异常】三段组织，突出 token 消耗异常的会话，语言简洁：{{dashboard.today}}

# 每周五 18:00 周报（多变量组合，自动对比）
/cron add 0 18 * * 5 请基于本周数据写周报，并与上周数据对比总结趋势：本周：{{dashboard.week}} 上周：{{dashboard.last_week}}

# 无 LLM 纯数据直推（现在就可行，不依赖本特性新增代码）
/cron addexec 0 9 * * * curl -s -H "Authorization: Bearer <token>" "http://127.0.0.1:<port>/api/v1/dashboard?period=day" | jq .
```

叙事报告的输出就是普通 Markdown 消息，经现有消息流推送，不需要新 payload 通道。

## 5. 业务报告中心（轨道 B：Report Artifacts）

### 5.1 动机与现状

auto_bugfix 的每日复盘（cron job `daily-workreport`，见 `scripts/cc-connect-qhn/crons/jobs.json`）是典型业务：agent 读 issue 库与知识库、跑分析脚本（token-usage-analyzer / session-analyzer）、落地 `reports/YYYYMMDD/token-daily.html`、`daily-summary.md` 等产物，最后把**文档全文粘进企微群**。痛点：HTML 在群里无法展示、全文播报又臭又长、历史报告无入口可回看。

关键事实：**文件托管已经存在，不需要另起 python http.server**（那只是多一个无鉴权暴露面）。`GET /api/v1/files/<project>/<relpath>`（management/mgmt_server.go:472）已具备：

- 带鉴权 + 路径穿越防护，根限定在项目工作目录；
- `.html` → `text/html`、`.md` → `text/markdown`，浏览器可直接预览；目录访问返回 JSON 列表（name/type/size/mtime）。

即 management server 运行时，`reports/20260816/token-day.html` 今天就能通过
`/api/v1/files/auto-bugfix/reports/20260816/token-day.html` 访问。真正缺的是四件事：**索引、Web 入口页、可点击链接、播报降载**。

### 5.2 业务契约（约定目录 + 可选 manifest）

```
<project work_dir>/reports/                 ← 约定根目录（[dashboard].reports_dir 可配）
  <yyyy-mm-dd>/<slug>.html|.md              ← 报告文件，业务自由生成
  <yyyy-mm-dd>/<slug>.manifest.json         ← 可选元数据（改善展示，非必需）
```

manifest（可选；无 manifest 也能列出——标题=文件名，日期=从 `yyyy-mm-dd` 目录名推断）：

```jsonc
{
  "title": "Token 消耗日报",
  "type": "token",                    // 业务自定义分类，报告中心按此过滤
  "generated_by": "daily-workreport"  // 可选，溯源 cron job
}
```

规则：

- 引擎**不解析、不理解**报告内容，原样托管预览；
- 既有非日期目录（`dashboards/`、`investigations/`、`knowledge-health/` 等）同样收录；
- 索引默认收录 `.html`/`.md`，递归限深 3 层、按 mtime 倒序、上限 500 条（防大目录拖垮扫描）。

### 5.3 引擎交付（四件事）

1. **索引 API**：`GET /api/v1/reports?project=&type=&limit=` → `[{path, title, type, format, date, size, mtime}]`（惰性扫描 + 短 TTL 缓存）；
2. **Web `/reports` 报告中心**：见 §5.4 壳与内容分离模型；
3. **推送链接化**：CronJob 执行前后各拍一次报告索引快照，结果消息尾部自动追加本轮新增报告链接（"📄 新报告：Token 消耗日报"）；
4. **播报降载**：业务 prompt 的"全文群播"改为"摘要 + 链接"——企微 12 行限制等痛点随之消失。

### 5.4 Web 与业务看板的结合：壳与内容分离

核心原则：**SPA 永远不把业务 HTML 吞进 React 构建**（那会要求业务参与前端编译，破坏通用性）。结合点是 URL，不是代码：

```
SPA（引擎的壳：导航/鉴权/主题，React）
 ├─ /reports                 列表页：React 原生渲染索引数据（manifest 提供标题/分类）
 └─ /reports/preview?project=&path=&fullscreen=
      └─ iframe src = /api/v1/files/<project>/<path>?token=<cc_token>
         （业务的地盘：任意 HTML/JS/图表，引擎当黑盒托管）
```

**鉴权事实（已核实，P0 零服务端改动）**：`authenticate()` 已支持 `?token=` query 参数（mgmt_server.go:361）；SPA 登录后 token 在 `localStorage.cc_token`（web/src/store/auth.ts:19），同源拼 iframe src 即可。MD 报告不走 iframe，直接复用 SPA 现有 markdown 渲染栈。

**结合深度分级**：

| 级别 | 能力 | 前提/改动 |
|---|---|---|
| P0 列表 + 预览 + 全屏 | iframe + query token；`fullscreen=1` 隐藏 SPA chrome，用户视角即"我的看板站"，URL 可收藏、企微链接直达 | 业务报告**自包含单文件 HTML**（inline JS/CSS——analyze_v2.py / py-echarts / plotly 等生成器默认如此）；零服务端改动 |
| P1 多文件看板 | HTML 引用同目录本地资源（`./echarts.min.js` 等）也能加载 | files 端点补 cookie 鉴权（登录时 Set-Cookie，HttpOnly + SameSite=Strict + Path=/api/v1/files），iframe 子资源自动携带；Bearer/query 并行保留 |
| P2 双向钩子（可选） | 业务看板 ↔ 壳通信：`postMessage({type:"heron:navigate", to:"/chat/..."})` 跳转会话详情；`{type:"heron:stats", period:"day"}` 由父页代理 `/api/v1/dashboard`（token 永不进 iframe） | 文档化一小段 postMessage 协议；纯增量，业务不用也不影响展示 |

**固定地址业务看板（项目大盘底部，兜底结合方式）**：结构化层（§5.5）无法表达的自由内容，走固定地址 HTML——每个项目可配置一个**固定地址**的深度分析看板（`[dashboard].html_path`，如 `dashboards/index.html`），项目大盘底部 iframe 直接嵌入。**筛选参数透传契约**：引擎把当前时间筛选以 query 参数追加到 iframe URL——`?period=day|week|month|custom&start=YYYY-MM-DD&end=YYYY-MM-DD`；files 端点只消费 `token` 参数（mgmt_server.go:361），其余参数原样抵达业务 HTML 的 `location.search`，业务自行解释渲染。

业务侧典型形态：固定地址放一个**一次性写好的参数驱动 shell**，每日 cron 只更新它引用的数据文件（如 `dashboards/data-YYYYMMDD.json`），shell 按 start/end 加载对应数据。⚠️ shell 若以相对路径 fetch 数据文件，依赖 P1 cookie 鉴权；纯自包含单文件则 P0 即可。历史报告归档浏览仍走报告中心列表页，两者互补。

**安全边界**：企微等 IM 推送的链接**永远指向 SPA 路由**（`/reports/preview?...`，靠浏览器登录态），不推裸 files URL——query token 不落聊天记录；`?token=` 只在 SPA 内部拼 iframe 时使用。

### 5.5 结构化业务数据（InsightPayload，HTML 之前的结构化层）

业务分析结论若只用 HTML 呈现，就失去了原生外观、主题/i18n 一致性和**点击跳转**等交互能力。项目大盘因此采用**结构化梯度**——HTML 是兜底，不是首选：

| 层 | 格式 | 引擎理解程度 | 交互能力 |
|---|---|---|---|
| UsageReport | 固定字段 JSON | 完全 | 全原生（图表/排序/跳转） |
| **InsightPayload** | **半固定字段 JSON**（字段名固定，语义业务定） | 结构已知，内容黑盒 | 原生渲染 + 会话行跳转聊天 |
| html_path HTML | 任意 | 黑盒 | 仅 iframe + 参数透传 |

业务把分析结论写成固定 schema 的 JSON，落到固定地址：

```jsonc
// <work_dir>/dashboards/insights.json（[dashboard].insights_path 可配）
{
  "version": 1,
  "generated_at": "2026-08-31T18:05:00+08:00",
  "generated_by": "daily-workreport",          // 溯源
  "period": { "start": "2026-08-30", "end": "2026-08-31" },  // 覆盖窗口（展示标注用，不参与引擎筛选）
  "cards": [                                    // 顶部自由 KPI 卡（可选，复用引擎 StatCard 组件）
    { "label": "修复 PR", "value": 3, "unit": "个", "tone": "good" },
    { "label": "分析后跳过", "value": 2, "unit": "个", "tone": "neutral" },
    { "label": "归档验收", "value": 5, "unit": "个" }
  ],
  "sessions": [                                 // 会话级业务分析（核心）
    {
      "agent_session_id": "cli-uuid-…",         // 跳转键①：CLI 会话 ID（业务脚本手里就有的）
      "session_id": "conv-…",                   // 跳转键②：引擎会话 ID（两个至少给一个）
      "title": "修复登录 502",
      "summary": "根因是网关超时未重试",          // 一句话结论
      "metrics": [                              // 业务自定义指标：label/value 通用渲染，语义引擎不管
        { "label": "Token", "value": 45000 },
        { "label": "耗时", "value": 32, "unit": "min" }
      ],
      "tags": ["bugfix", { "text": "已修复", "tone": "good" }, { "text": "已整理", "tone": "info" }],
                                                 // tag = 纯字符串（中性色）或 {text, tone}；tone 统一词表 good|info|warn|error
      "tone": "good",                           // 行级状态点颜色（可选，同词表）
      "detail": "reports/20260831/token-day.html"  // 可选：本行详细 HTML（经 files API 打开）
    }
  ]
}
```

**渲染与交互契约**：

- Web 拉取 insights.json（files API）与 UsageReport.topics，按 `session_id` / `agent_session_id` **合并成一个会话列表**：引擎行提供轮次/token/平台等基线列，业务行追加 summary/metrics/tags；业务 period 与当前筛选不一致时照常展示、标注其覆盖窗口；
- **跳转键命中**（两个 ID 任一能在引擎会话中找到）→ 整行可点击 → `/chat/:project/:session_id`；
- 未命中（纯 CLI 会话、未经引擎）→ 行不可跳转，仅 `detail` 链接可用；
- `metrics` 是 label/value 的开放集：业务定义指标语义，引擎定义画法（`tone` 决定颜色）——**字段名固定、指标语义自由**；
- **tags 渲染**：彩色 chip 徽章（tone 决定颜色，纯字符串 = 中性灰）。引擎不理解含义——"已修复/已整理"是 auto_bugfix 的工作流状态，别的业务写什么都只是展示；点击 chip 前端过滤列表；
- **合并行布局**（每行 = 引擎基线 + 业务增量，即"单会话维度信息"的完整呈现）：

  ```
  [●] 修复登录 502   [已修复] [已整理] [high-cost]              ← 整行可点跳聊天
  feishu · 张三 · 轮次 12 · Token 45k · 工具 34 · 16:20          ← 引擎列（topics）
  根因是网关超时未重试 · Token 45k · 耗时 32min · [详情]         ← 业务列（insights）
  └ 行展开：turn 级明细抽屉（单会话明细 API，§7）
  ```

- schema versioned、只增不改：前端忽略未知键，不破坏兼容。

**为什么需要两个跳转键**：业务脚本（analyze_v2.py 等）直读 CLI 会话日志，手里只有 CLI 会话 ID（`.jsonl` 文件名）；引擎会话 ID 与之是映射关系。TurnRecord 已同时记录两者（§3.1），双键设计让业务零成本对齐引擎。

### 5.6 业务引入示例（auto_bugfix daily-workreport）

cron job 结构与触发不变，prompt 仅微调第 4/5 步：

```
第 4 步（落地，增补）：除现有产物外——
        a. 汇总当日会话级分析结论，写 dashboards/insights.json（InsightPayload §5.5：
           cards + sessions，session 行带 agent_session_id/summary/metrics/tags）；
        b. 为每个 HTML 报告写 <slug>.manifest.json（title/type/generated_by）。
第 5 步（群播报，改写）：不再全文播报。改为三段摘要（各 1-2 句 + 关键数字），
        末尾注明"完整报告见 Web 报告中心 reports/<日期>/"。
        （引擎会自动追加本轮新增报告的可点击链接，见 §5.3-3）
```

口径互补说明：业务脚本（analyze_v2.py 直读 CLI 会话日志，含子 agent 细节）与引擎统计（`{{dashboard.today}}`，平台入口的 turn/token 基线）是两个口径，报告中可并列引用、互为校验。

## 6. 配置设计

```toml
# ── [dashboard]：项目大盘（全局，唯一配置段；路径相对各项目 work_dir 解析）──
[dashboard]
enabled = true    # 主开关——用户只回答一个问题：要不要统计数据。
                  #   true  = 全家桶：采集 + 项目大盘 + 报告中心 + cron 推送链接
                  #   false = 整体关闭，页面回到现状，零风险
collect = true    # 数据来源（两种模式）：
                  #   true  = connect 自己采集（TurnRecorder → 引擎侧汇总 + 单会话）
                  #   false = connect 不采集，统计展示完全依赖业务产出
                  #           （insights.json 的 cards 承担汇总、sessions 承担单会话）

# ↓ 以下均有默认值，零配置可全部省略
retention_days = 90                         # metrics JSONL 保留天数（collect=true 时有效）
include_message_excerpt = true              # topics.first_message 首条消息摘要（隐私开关）
max_topics = 10                             # UsageReport.topics 截断条数
insights_path = "dashboards/insights.json"  # 业务结构化数据固定地址；空 = 该区永不显示
html_path = "dashboards/index.html"          # 业务 HTML 看板固定地址；空 = 该区永不显示
reports_dir = "reports"                     # 历史报告归档目录（报告中心扫描）
public_base_url = ""                        # IM 推送链接前缀；空 = management 监听地址
```

**数据职责：connect 只产出 stats（汇总 + 单会话），其他字段引擎只管展示、数据靠业务补充**：

| 维度 | connect 产出（collect=true） | 业务产出（固定地址文件，谁产出都行） |
|---|---|---|
| 汇总 | UsageReport 总览/时间分布/分组 | insights.json `cards`（KPI 卡） |
| 单会话 | topics（轮次/token/平台/时长） | insights.json `sessions` |
| 补充字段 | — | summary / tags / metrics / tone / detail（引擎不理解含义，纯展示） |

**效果矩阵**：

| enabled | collect | 效果 |
|---|---|---|
| true | true（默认） | 引擎采集 + 大盘三区渐进增强（合并行 = 引擎列 + 业务列）+ 报告中心 + `{{dashboard.*}}` |
| true | false | **纯展示模式**：无引擎统计区/`/dashboard` 命令/`{{dashboard.*}}`；业务区与报告中心照常——汇总和单会话数据由 insights.json 承担 |
| false | — | 整体关闭，页面 = 现状 |

补充规则：

- **文件存在性独立于 collect**：业务区只看文件在不在，不需要注册、不需要重启感知（Web 每次加载探测）；
- collect=true 时引擎数据与业务数据**并存互补**（合并行），不是二选一；
- **per-project 覆盖**：P0 不做，全局唯一；
- metrics 文件为实例级共享（多项目 engine 写同一 `metrics/`，TurnRecord 带 project 字段区分），collect=false 或 enabled=false 后已有文件保留可手动清理。

报告类 cron 任务完全复用现有 `CronJob` 体系（`<data_dir>/crons/jobs.json`），零新增任务字段。

## 7. API 设计

```
GET /api/v1/dashboard?period=day|week|month|custom&date=2026-08-31&start=&end=&project=all|<name>
  → UsageReport JSON（date 缺省 = 今天；week 时 date 取任意一天定位所在周；
    month 为 date 所在自然月；custom 必传 start/end，跨度上限 90 天）

GET /api/v1/dashboard/summary
  → { "today": UsageReport, "week": UsageReport }   # Dashboard 摘要，一次请求

GET /api/v1/dashboard/sessions/<project>/<session_id>?start=&end=
  → 单会话 turn 级明细：[{ts, trigger, duration_ms, input/output/cache_tokens, tools, error}]
  （会话行展开抽屉用——回答"这个会话为什么这么贵"；实现 = TurnRecord JSONL 按 session 过滤，成本极低）

GET /api/v1/reports?project=&type=&limit=           # 业务报告索引（§5.3）
（报告文件本体复用现有 GET /api/v1/files/<project>/<path>）
```

stats 路由挂 `ManagementServer.buildHandler`（management/mgmt_server.go:236），跨项目聚合遍历 `m.engines`（每项目一个 engine，mgmt_server.go:50）——与现有 handleProjects 同模式。

## 8. 实现要点（挂点已核实）

### Phase 1 — 采集底座
1. 新增 `core/stats_recorder.go`：`TurnRecorder`（`[dashboard].collect` 控制），O_APPEND 写当天文件 + 启动清理过期文件；
2. 挂点①：turn 完成处（core/engine_turn.go:1274，现有 slog 点位字段全齐：session/agent_session/p.Name()/toolCount/turnDuration/event.InputTokens/OutputTokens/isSilent，含 cache token 则一并记录）；
3. 挂点②：会话创建处记 `session_created`；
4. token 兜底：`sdkPlausible`（engine_turn.go:1193）不成立时用 `contextEstimate`（:1199）记入 input_tokens，`tokens_estimated=true`；
5. trigger 判定：cron 合成消息（engine_cron.go:130-138，UserID=="cron"）→ `"cron"`，否则 `"user"`；
6. 顺带新增 hook 事件 `HookEventTurnComplete`（core/hooks.go，Extra 携带 tokens/duration/tool_calls）——外部系统可经 hooks 自行消费，生态收益。

### Phase 2 — 聚合与消费
7. `core/stats_aggregate.go`：`Aggregate()` 纯函数 + `RenderReportMarkdown()`；
8. management 新增 `/api/v1/dashboard`、`/api/v1/dashboard/summary` 路由；
9. engine 新增 `/dashboard` 命令（注册模式参考 cmdCron*）；
10. cron prompt 模板变量解析（engine_cron.go:135 前替换）；
11. management 新增 `/api/v1/reports` 索引路由（扫描 `[dashboard].reports_dir` 约定目录，惰性 + TTL 缓存）；
12. 单会话明细路由 `/api/v1/dashboard/sessions/<project>/<id>`（TurnRecord 按 session 过滤，输出 turn 级 token/时长/工具序列）。

### Phase 3 — Web 前端
13. `web/src/api/dashboard.ts` + 新建项目大盘页 `web/src/pages/Projects/ProjectOverview.tsx`（路由 `/projects/:name/overview`；时间筛选日/周/月/自定义 + 标准统计区 + 业务结构化区：InsightPayload 渲染、与 topics 双键合并、tags 徽章/过滤、会话行跳转 /chat/:project/:id、行展开 turn 级明细抽屉 + HTML 兜底区 iframe 参数透传 + 右上角【💬 进入聊天】【⚙️ 项目设置】；三区按数据存在性渐进增强，全无数据退化为项目概览）+ i18n（zh/en）；
14. Dashboard.tsx 顶部"今日摘要"条（跨项目全局视角）；
15. `web/src/pages/Reports.tsx`：报告中心页（日期分组 + type 过滤）+ `/reports/preview` 预览路由（HTML iframe 拼 query token、MD 走现有渲染栈、`fullscreen=1` 全屏模式）；
16. cron 结果消息自动追加新报告链接：engine_cron.go 执行前后各拍一次报告索引快照，diff 出新增项拼入结果消息尾部（链接指向 SPA 路由，不携带 token）。

### Phase 4 — 可选增强（非承诺）
- 叙事报告卡片化：约定 agent 输出首行 `<!-- report:day:2026-08-31 -->`，Web 渲染为特殊卡片并附"查看完整统计"跳转（复用 progress payload 协商思路）；
- `period=month`、多周趋势、CSV 导出；报告中心按 type 聚合的订阅推送。

## 9. 边界与约定

- **时区**：窗口按 engine 本地时区切分；跨午夜长 turn 按 turn 开始时间归窗；
- **存量数据**：上线前无从统计，看板自启用日起累计（不回填）；
- **体积**：每轮约 300B，日均 500 轮 ≈ 150KB/天，90 天 ≈ 13MB，可忽略；
- **多实例**：metrics 落各自 `data_dir`，聚合范围 = 单个 management server 所辖项目（跨机聚合为非目标）；
- **mute/silent turn** 仍计入统计（有真实消耗）；
- **报告中心依赖 management server 常驻**（daemon 模式运行）；daemon 未运行时仅 IM 查询可用；
- **推送链接**用 `[dashboard].public_base_url` 组装；外网暴露（域名/TLS/额外鉴权）属部署层，交反向代理解决，引擎不管；
- **报告产物不入 metrics、不受 retention 约束**（生命周期业务自管）；
- **业务看板默认按自包含单文件 HTML 约定**（P0）；引用本地资源的多文件看板依赖 P1 cookie 鉴权；
- **IM 推送链接只指向 SPA 路由**（登录态承担鉴权），`?token=` 仅限 SPA 内部拼 iframe，不进聊天记录。

## 10. 隐私

- metrics **不落消息正文**，只记维度与计数；
- `topics.first_message` 是唯一文本字段（截断 100 字符），受 `include_message_excerpt` 开关控制；
- 叙事型报告若需更多上下文，业务 prompt 可指示 agent 自行读取会话快照（agent 本就运行在项目环境内），该路径与 metrics 无关；
- 报告中心沿用 management server 统一鉴权，报告文件不额外暴露。

## 11. 非目标

- 不做实时监控/告警（hooks 生态已覆盖）；
- 不做跨实例/跨机聚合；
- 不在引擎内置任何日报文案或模板（示例 prompt 仅进 docs）；
- metrics 不替代会话快照（不做审计用途）；
- 不解析、不理解业务报告内容，不做报告全文检索；
- 不做按报告粒度的权限控制（沿用 management 统一鉴权）。
