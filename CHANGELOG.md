# Changelog

## v1.1.30 (2026-09-01)

### Fixed

- **修复空响应被静默吞掉（模型接口返回空时用户只看到"(空响应)"）**：所有 agent 适配器在模型返回空结果时，不再发出空 `EventResult`，改为发出明确的 `EventError`（携带 stderr 或诊断信息），用户看到的是具体错误而非占位符。覆盖 codebuddy/claudecode/cursor/qoder/codex（stdio+app-server）/gemini/kimi/opencode/pi/iflow/heron；acp/devin 保留已有的 `extractACPReturnError`（已覆盖模型错误主场景）。
- **engine 空响应加诊断日志**：`(空响应)` 占位符处新增 `slog.Warn`（含 session/agent/耗时/工具数上下文），空响应可追溯。
- **pendingSend 错误 Debug→Warn**：三处"已完成 turn 的异步 Send 尾音错误"从 Debug 级升级为 Warn 级，错误可查而不误导用户。

### 文档

- 新增 `docs/research/07-empty-response-analysis.md`（空响应根因分析）与 `docs/research/08-silent-error-audit.md`（静默吞错模式系统排查报告）。

## v1.1.29 (2026-09-01)

### Fixed

- **修复高并发下 queued-turn 的 nil pointer panic**：`processInteractiveEvents` 处理排队消息时，`state.agentSession` 可能被并发的会话清理（idle reaper / /new / 消息撤回）置为 nil，闭包直接解引用导致 SIGSEGV。现改为在解引用前将 `agentSession` 快照到局部变量并判空，清理后安全跳过该排队轮次。压测（200 并发会话持续收发）暴露并验证修复。

## v1.1.28 (2026-09-01)

### Changed

- **进度卡工具调用行级就地更新**（借鉴 openclaw 的 mergeChannelProgressDraftLine）：`ProgressCardEntry` 新增 `CorrelationKey` 字段，`compactProgressWriter` 按该键**就地更新已有行**而非追加新行——tool_use → tool_result 自动坍缩为一行状态流转（running→completed/failed），对所有卡片平台生效。仅当存在真实 ToolID 时才合并（不按工具名合并，避免误合并并行同名工具）。
- **cron 调度表达式扩展**：`CronExpr` 新增 `@every <dur>`（固定间隔）与 `@at <RFC3339>`（一次性定时，触发后自删）两种便捷形式，向后兼容标准 5 段 cron。
- **cron 可靠投递**：`CronJob` 新增 `retry_count`（默认 1）与 `notify_on_failure` 字段——执行失败自动重试（5s 退避），重试耗尽后可选推一条失败通知到任务目标会话。
- **入站消息显式准入决策**：新增 `MessageAdmission` 枚举（dispatch/observe_only/handled/drop/reject）与统一 `message admission` 日志，将 rate-limit/banned-word/命令/权限等早退路径的决策与观测收口（不改既有控制流，行为等价）。
- **会话绑定生命周期**：新增 `SessionBindingPolicy`（IdleTimeout + 硬上限 MaxAge），会话回收器读完整策略；IdleTimeout 按逻辑平台名解析——根治 web/bridge 会话被项目级 idle 误切的隐患（`SetBindingMaxAge` 可设硬上限，默认关闭向后兼容）。

### Fixed

- **修复 bridge 测试的 websocket 并发写 data race**：`TestBridge_WebThinkingToolPersist` 的 reader goroutine 与主 goroutine 并发写同一 `*websocket.Conn`（gorilla 不支持并发写），全量并行测试下稳定触发 race 检测。加 `connWriteMu` 互斥串行化。
- **修复 management 统计测试跨午夜 flaky**：`writeMetrics` 用 `now.Add(-1h)`，凌晨 00:00–01:00 跑测试时首条记录落昨天、文件名是今天，导致断言失败。改为 clamp 到当天。

## v1.1.27 (2026-08-31)

### Added

- **项目大盘（Web 项目统计与业务报告托管）**——回答"今天/本周干了什么"的新特性全家桶，唯一配置段 `[dashboard]`（零配置默认开启）：
  - **用量采集**：每轮对话记录一条 metrics（会话/平台/agent/用户/触发来源/时长/输入输出 token/工具调用，不含消息正文），按天落 `<data_dir>/metrics/turns-YYYY-MM-DD.jsonl`，`retention_days` 自动清理；agent 未回报 usage 时用上下文估算并标记 `tokens_estimated`。
  - **IM 命令 `/dashboard [today|yesterday|week|lastweek]`**：当前项目用量统计，卡片平台渲染交互卡片、其他平台 Markdown 文本。
  - **cron 模板变量 `{{dashboard.today|yesterday|week|last_week[:json]}}`**：cron prompt 执行时注入统计数据（Markdown 表格或原始 JSON），未识别变量原样保留；新增 `turn.complete` hook 事件。
  - **REST API**：`/api/v1/dashboard`（日/周/月/自定义窗口聚合，`collect=false` 返回 404 供前端隐藏区域）、`/dashboard/summary`、`/dashboard/sessions/<project>/<id>`（单会话逐轮明细）、`/dashboard/settings`、`/api/v1/reports`（业务报告索引 + manifest 元数据）。
  - **Web 项目大盘页**（`/projects/<name>/overview`，项目卡片新入口；右上角【进入聊天】【项目设置】，原配置台保留）：三区渐进增强——标准统计区（KPI 卡 + 活跃分布 + 会话行点击跳聊天 + 逐轮明细抽屉）、业务结构化区（`dashboards/insights.json` InsightPayload：KPI cards + 会话级 summary/metrics/tags，与引擎会话列表双键合并、标签过滤）、HTML 兜底区（固定地址 iframe，时间筛选参数透传）。三区各自按数据存在性点亮，全无数据退化为项目概览，升级零风险。
  - **报告中心**（`/reports`）：业务归档报告（`reports/<日期>/` html/md + 可选 manifest）自动收录、按类型过滤、HTML 预览/全屏、Markdown 查看。
  - **配套交付**：`skills/heron-connect-dashboard`（教业务 agent 产出 insights.json/manifest/HTML shell）、`docs/dashboard.md` 使用指南、`docs/stats-dashboard-design.zh-CN.md` 设计文档、`examples/dashboard*.toml/json/html` 三个示例。

### Changed

- **文件预览支持 HTML 渲染 + 手动刷新**：聊天页文件预览对 `.html` 文件提供双视图——沙箱 iframe 渲染效果（默认）与原始源码切换；工具栏新增手动刷新按钮（磁盘文件无推送机制，agent 重写文件后可一键拉取最新字节）。
- **文件预览绕过浏览器缓存**：files 端点不发送 Cache-Control，默认 fetch 可能命中启发式缓存导致 agent 重写文件后 UI 显示旧字节。预览请求改用 `cache: 'no-store'`。

### Fixed

- **统计文件窗口枚举边界 bug**：`MetricsFilesBetween` 原按时间戳逐天迭代，当窗口起止源自同一时钟 tick（纳秒相同，macOS 粗粒度时钟下常见）时末位当天文件被静默丢弃（单会话明细接口偶发空数据）。改为按日历天对齐枚举并补回归测试。

## v1.1.26 (2026-08-30)

### Fixed

- **进度卡工具调用按 id 聚合，不再一行行刷屏**：codebuddy 场景每个工具产生 tool_use + tool_result 两条记录逐条追加，一轮对话卡片被刷满。现在 `Event.ToolID` → `ProgressCardEntry.ID` 全链路透传（codebuddy/claudecode 适配器补齐 id），web 端将同一 id 的调用与结果**合并为一行**（带 ok/error 徽章，展开可见输入+输出两段），**连续同名工具聚合为 "Read ×N" 分组行**（组头汇总状态徽章，展开逐条查看）；无 id 的旧 payload / codex 场景退化为按工具名分组。
- **修复 /model 模型列表无法滚动**：模型选择用原生 `<select>`，移动端 webview 弹层滚不动、且滚动经过选项即触发 onChange 误切换模型。改为自定义 `SelectList` 内联列表（`max-h-64 overflow-y-auto` 自滚动、当前模型高亮勾选、点击即选），命令结果面板与消息流卡片两处统一替换。

## v1.1.25 (2026-08-28)

### Changed

- **文件浏览器目录下拉默认收起**：打开浏览器只显示面包屑 + 当前文件预览，不再自动弹出整个目录列表；点击面包屑才展开（openDir/goParent 导航时保持展开）。回退 v1.1.21 误读反馈引入的"默认展开"。
- **PC 端输入行对齐 + 底部不留空**：textarea `py-3`→`py-2`，单行高度精确等于按钮 `p-3` 的 42px，图标与输入框底对齐；删除消息区 `max-h-[calc(100dvh-…)]`（其 192px 桌面估值比实际 header+input 高 ~50px，导致 PC 输入区下方永久留空）——v1.1.24 已补全 `min-h-0` 收缩链，flex-1 自动贴底。

## v1.1.24 (2026-08-28)

### Fixed

- **【紧急】修复 PC 端聊天页高度回归（v1.1.22 引入）**：消息列表无法滚动、header/输入框被顶出视口。根因是 v1.1.22 删除消息区 `max-h` 后暴露了 Layout 包裹层（`main > div.flex-1.flex.flex-col`）缺失 `min-h-0`——其 automatic minimum size 等于全部消息内容高度，撑爆 main 的固定高度后被 `overflow-hidden` 裁剪。修复：恢复消息区 `max-h-[calc(100dvh-136px)] md:max-h-[calc(100dvh-192px)]`（dvh 基准对齐新高度体系）+ Layout 包裹层补 `min-h-0`（根因，保证 flex 收缩链完整，max-h 更宽时不再出现悬浮空隙）。
- **修复 markdown 文件预览点击即崩溃（v1.1.17 起的存量炸弹）**：预览含链接的 md 文件时整页白屏（React error #130，React 根卸载）。根因是 `RenderMarkdown` 的 `components.a` 在无 `onOpenFile` 时显式为 `undefined`——hast-util-to-jsx-runtime 会把 undefined 直接传给 `React.createElement`，markdown 一含 `<a>` 即抛 "Element type is invalid"。聊天消息不受影响（始终传 onOpenFile）；仅 md 文件预览（FileContentView）踩中。修复：fallback 从 `undefined` 改为字符串 `'a'`（渲染原生链接）。

## v1.1.23 (2026-08-28)

### Changed

- **聊天气泡 markdown 内联代码与超链接可辨识度优化**（纯前端）：
  - 内联代码（`` `code` ``）：深色模式下原 `bg-gray-800` 与气泡背景 `dark:bg-gray-800/80` 几乎同色，"框"完全看不出来。现改为 `dark:bg-black/35` + `dark:border-white/10`（比气泡明显更深）+ 更亮的 pink-300 文字；浅色模式同步微调为 slate 色系。
  - 超链接：hover 下划线加粗（`decoration-2`）并加大偏移（`underline-offset-2`）更易识别；新增 `prose-a:break-all` 让长 URL 自动换行不再溢出气泡。

## v1.1.22 (2026-08-28)

### Fixed

- **移动端输入框被 URL 栏/软键盘遮挡**。根因是全站布局高度基于 `100vh`（iOS 含 URL 栏高度），且无任何 dvh/visualViewport 适配：
  - 布局根改为 `h-screen supports-[height:100dvh]:h-[var(--app-height,100dvh)]`，并新增 `visualViewport` resize 监听（iOS 软键盘场景唯一可靠方案）把实际可视高度写入 `--app-height` CSS 变量。
  - viewport meta 补 `viewport-fit=cover, interactive-widget=resizes-content`（Android Chrome 键盘弹出时收缩布局视口，输入区自动抬到键盘上方）。
  - 所有 fixed 全屏抽屉（移动端侧边栏 / 会话抽屉 / 命令结果面板 / 文件预览 / 项目文件浏览器）`h-full` → `h-dvh`，底栏不再被 URL 栏裁掉。
  - 删除消息区魔法数 `max-h-[calc(100vh-136px)]`（flex-1 + overflow 已正确约束；魔法数反而让输入区悬浮高出真实底部 ~17px，键盘/URL 栏变化时全部失真）。
- **移动端输入行横溢与错位**（截图症状：输入框底部不对齐、右下角出现横向滚动指示器）。输入行包裹层缺 `min-w-0`，textarea 默认 20 列 intrinsic 宽 + 3 按钮在 320–360px 窄屏直接横溢；且 textarea 字号 <16px 触发 iOS 聚焦自动缩放。修复：包裹层与 textarea 加 `min-w-0`；字号 `text-base md:text-sm`（≥16px 阻止 iOS 缩放）。

## v1.1.21 (2026-08-28)

### Changed

- **移动端顶部交互简化**（纯前端）：
  - 收起态悬浮组去掉「⋯」工具按钮，只保留汉堡（三个横线）——聊天页移动端顶部状态恒定，不再有展开/收起循环；刷新/语言/主题/登出在非聊天页的展开态顶栏仍可用。
  - 聊天头部移动端隐藏「重命名」按钮（置顶/项目文件保留），右侧留白相应从 `pr-20` 调小为 `pr-12`（只需清开单个汉堡按钮）。
  - 聊天窗口移动端隐藏「已连接」bridge 状态徽章（连接异常仍通过输入区警告提示）；桌面端不变。
- **文件浏览器目录下拉默认展开**：`dropdownOpen` 初始值改为 `true`，每次打开必展开，不再依赖加载 effect 的 setState 时序。

### Fixed

- **移动端窄屏头部溢出**：会话选择按钮（自动标题取首条用户消息，可能很长）无截断导致头部被撑爆/换行。现在标题 span `truncate`、按钮 `min-w-0 max-w-full` 约束，长标题显示省略号。其余区域（消息气泡/输入区/ProgressCard/附件 chips）静态排查确认窄屏布局正常。

## v1.1.20 (2026-08-28)

### Fixed

- **修复 PC 预览右栏拖拽调宽不可用**。v1.1.19 把拖拽分隔条放在 `<Fragment>` 内、面板 div 外——`Fragment` 不产生 DOM，`absolute left-0` 分隔条失去定位父级而跑到页面最左侧，导致拖不到。现已把分隔条移入面板 div 内部，并把 `md:static` 改为 `md:relative`（保证绝对定位锚定到预览列）。鼠标移到预览面板左边缘（`cursor-col-resize`）即可拖宽。

### Changed

- **Web 左侧边栏收起时隐藏文字**。收起态（窄栏）下导航项只显示居中图标，隐藏文字（不再竖排文字与图标重叠），悬停显示 `title` tooltip 展示完整名称。

## v1.1.19 (2026-08-28)

### Changed

- **Web 文件预览三处交互优化**（纯前端）：
  - **文件浏览器记住浏览位置**：`ProjectFileBrowser` 按项目记住上次浏览的目录 + 选中文件（localStorage `cc_file_browser:<project>`），重开从上次位置继续而非项目根目录；文件名匹配恢复，文件不存在则回退到第一个。
  - **markdown 预览可滚动**：预览里的 `RenderMarkdown` 之前无 `max-h`/`overflow` 包裹导致长文档撑破容器不能滚动，现外包 `max-h-[70vh] overflow-auto`，并为预览面板 + 内部滚动容器补 `min-h-0`，修复 flex 子项被内容撑开、局部滚动失效的问题。
  - **PC 预览右栏可拖拽调宽**：FilePreview 与 ProjectFileBrowser 面板左侧新增拖拽分隔条，md(≥768px) 下可拖动改变预览列宽度（clamp 320px~70vw），宽度全局持久化（localStorage `cc_preview_width`），两个面板共享同一宽度；移动端保持全屏抽屉、不参与拖拽。新增 `lib/utils.ts` 的 `loadLS`/`saveLS`（沿用 `cc_` 前缀惯例）。

## v1.1.18 (2026-08-28)

### Fixed

- **codebuddy 适配器补齐 thinking 解析 + 修工具名回查**。此前 `handleAssistant` 只处理 `text`/`tool_use` 两类 content 块，thinking 块被静默丢弃导致 Web 端看不到思考过程；`handleUser` 处理 `tool_result` 时把 `ToolName` 错设为 `tool_use_id`（如 `tooluse_3MNyh1...`），导致工具结果显示成不可读的内部 ID。修复：assistant 阶段遇到 `tool_use` 时缓存 `id → name`，并加 `case "thinking"`（双字段防御 `Text`/`Thinking` 兼容 codebuddy 协议变体）；user 阶段 `tool_result` 从缓存反查可读名，cache miss 时 fallback 到原始 ID。新增 3 个回归用例。

### Added

- **Web 工具/思考/错误结构化展示**。Web 端 register metadata 增加 `supports_progress_card_payload: true`，bridge 透传 `__heron_connect_progress_card_v1__:` 结构化 payload（`core/progress_compact.go` 已具备），前端 `parseProgressCard` 检测前缀并用新的 `ProgressCard` 组件渲染：每个事件（thinking / tool_use / tool_result / error / info）成为独立可折叠 block，默认折叠、点击 header 展开，整体支持一键全部展开/折叠、状态徽章（success/error/pending）、末尾思考/spinner 动画。稳定 key 基于内容指纹（`kind|tool|text前32字符|idx`），后端 `maxEntries` 截断条目（丢弃最旧）时已展开的 block 不会因下标前移而错位。未升级的服务端仍 fallback 到现有 `RenderMarkdown` 渲染。

## v1.1.17 (2026-08-28)

### Fixed

- **Web 后台被项目级 `reset_on_idle_mins` 误切会话（平台级覆盖未生效）**。此前 `maybeAutoResetSessionOnIdle` 用 `p.Name()` 判定平台，而 Web 消息经 `BridgePlatform` 进入引擎时其 `Name()` 固定返回 `"bridge"`，永远匹配不上 config 里 `type = "web"` 条目配的 `reset_on_idle_mins = 0`，于是回落到项目级值（如 720 分钟）把 Web 会话误切成"新会话"。现改为按 `msg.Platform`（Web 前端注册上报 `"web"`）解析平台级覆盖；真实 IM（如 wecom）的 `p.Name()` 与 `msg.Platform` 均为 `"wecom"`，行为不变。新增回归测试 `TestHandleMessage_AutoResetOnIdle_PlatformOverrideZeroUsesMsgPlatform`（`p.Name()="bridge"` + `msg.Platform="web"` + 覆盖 `{"web":0}` 必须不旋转）。

### Changed

- **Web 文件预览：`.md`/`.markdown` 渲染为 markdown**。预览抽屉中 md 文件从纯文本 `<pre>` 改为复用 `RenderMarkdown` 排版渲染（标题/代码高亮/GFM 表格等），按扩展名与运行时 Content-Type 双重判断。
- **Web PC 端（≥768px）预览改为分栏推挤**。打开文件预览或项目文件浏览时，预览面板作为右侧内联列把聊天界面往左推，可一边看文档一边输入；移动端（<768px）保持原有全屏抽屉。

## v1.1.16 (2026-08-28)

### Fixed

- **Web 会话串号修复：每个会话独立 session_key + 显式按会话 id 路由**。此前 Web 把每次页面加载的 UUID 烘焙进 `session_key`（`bridge:web-admin:<project>:<WC_CONN_ID>`），同一标签页里所有会话共用同一个 key，叠加 12h idle auto-reset 后，点开历史会话（如 s89）发消息会跑到该 key 当前 active 的会话（如 s91）。现改为：
  - 前端：`webSessionKey.ts` 新增 `newConvKey(project)` 为每个会话铸造独立 key `bridge:web-admin:<project>:conv-<uuid>`；`ChatView` 不再回退共享 per-tab key，路由 key 永远取当前会话的 key；发送帧携带当前会话 `session_id`；回复过滤按 `session_key` 或 `session_id` 匹配（多标签页实时同步不受影响）。
  - 后端：`core.Message` 与 bridge 帧/replyCtx 透传 `SessionID`；引擎消息入口改为 `FindByID(msg.SessionID)` 优先路由，缺失才 `GetOrCreateActive(session_key)`——点开指定历史会话即落在该会话；`/new` 为 web 平台铸造全新 key（`core.MintWebSessionKey`），新会话从 key 层面独立；管理 API 对遗留共享 `wc-...` key 在返回时读时改写为 `conv-legacy-<id>`（存储不变），旧会话也立即隔离。
  - 测试：新增 `TestMintWebSessionKey_ShapeAndUniqueness`、`TestDistinctWebConversationKeysYieldDistinctSessions`。

## v1.1.15 (2026-08-28)

### Added

- **Web 聊天支持上传图片 / 文件**：聊天输入框新增「附件」按钮，可选择一张或多张图片/普通文件。图片通过 bridge `images` 帧发送（多模态 agent 如 claudecode / codex / pi / gemini 可直接视觉理解）；普通文件通过 bridge `files` 帧发送，落到 `<work_dir>/.heron-connect/attachments/` 并把绝对路径追加到 prompt（所有 agent 均可用工具读取）。支持纯附件发送（无文本仅附件）、已选附件缩略图/文件 chips 预览与移除，单个文件前端限制 10MB（超限提示并跳过）。纯前端改动，复用既有 bridge `message` 帧与后端落盘管线，无需新增配置项。补充 5 种语言（en/zh/zh-TW/es/ja）的上传相关文案。

## v1.1.14 (2026-08-27)

### Added

- **会话重命名 + 置顶/取消（聊天页 + 会话列表）**：
  - 后端：`core.Session` 新增 `Pinned` 字段并持久化到快照（同时修复 `saveLocked` deep-copy 漏拷 `NameAuto` 的潜在 bug）；新增 `SessionManager.SetSessionMeta(id, name, pinned)`（重命名会清除 `NameAuto` 标记，防止自动标题覆盖用户自定义名）；管理 API 新增 `PATCH /projects/{proj}/sessions/{id}`（body 支持 `name` / `pinned`），会话列表与详情响应均新增 `pinned` 字段。
  - 前端：`sessions.ts` 新增 `pinned` 字段与 `updateSession()` 封装，新增公共 `sortSessions()`（置顶会话排前，再按最近更新）；新增 `RenameSessionModal` 重命名弹窗；聊天页头部新增重命名 + 置顶/取消按钮；聊天页会话抽屉（`SessionDrawer`）与独立会话列表页（`SessionList`）的每行/每卡新增重命名与置顶操作；所有会话列表改用 `sortSessions` 排序，置顶会话置顶显示。

## v1.1.13 (2026-08-27)

### Added

- **文件浏览器支持把文件地址插入到输入框**：底部操作栏新增「插入地址」按钮（与 Download 并排），点击把当前预览文件的相对路径（相对项目 `work_dir`）插入到聊天输入框，方便在消息中引用 agent 生成的文件。相对路径与 agent 的工作目录一致，回复时 heron-connect 会自动链接化。

## v1.1.12 (2026-08-27)

### Fixed

- **切换预览文件时旧的文本/数据残留导致重复渲染**：`FileContentView` 的 `useEffect` 仅在开头重置 `state/error`，`text` / `dataUrl` / `contentType` 仍保留上一次的值。从 md 文件切到图片时，图片走 `dataUrl` 分支但旧 md 的 `text` 仍非空，触发 `<pre>` 与 `<img>` 同时渲染——在 `FilePreview` 抽屉中出现「图片预览 + 文件预览」两个区域。修复：在 effect 开头显式 `setText('')` / `setDataUrl('')` / `setContentType('')` 清空所有展示状态后再加载。同时修复项目文件浏览器详情视图的同源问题。

## v1.1.11 (2026-08-27)

### Fixed

- **文件浏览器 / 预览抽屉暗色亮色模式对比度优化**：暗色主题下抽屉背景由接近纯黑的半透明改为明确深灰（`#1f2228`），增强边框（`border-white/0.12`）与阴影，避免抽屉与页面背景撞色导致边界消失；下拉菜单暗色背景改为 `#2a2d34` 形成层级；代码区显式设置文字色（`text-gray-800 dark:text-gray-100`）提升可读性；图标/箭头/计数器/次要文字统一 `gray-500 dark:gray-400` 并加强 hover 反馈；空目录/不可预览/加载等提示补齐暗色文字色。

## v1.1.10 (2026-08-27)

### Added

- **项目文件浏览器**：聊天页头部新增「项目文件」入口，点开后从右侧滑出抽屉浏览整个项目 `work_dir`。主视图始终预览文件（复用文件阅读能力），顶部面包屑展示目录地址，点击弹出下拉菜单选择当前目录的文件/子目录/上一级；左右 `◀▶` 按钮仅在同目录的文件间切换（跳过目录），底部提供下载。后端 `GET /api/v1/files/<project>/<dir>` 扩展为：路径为目录时返回 JSON 目录列表（`path` + `entries`，目录在前文件在后按名排序，非递归），根目录（空路径）也可列出；复用既有 token 鉴权与路径穿越防护，仅暴露项目 workDir 内文件。

## v1.1.9 (2026-08-27)

### Added

- **Web 可点击阅读/下载 agent 生成的本地文件**：这是 Web 特有的能力——回复消息里的本地文件引用会被改写为可点击链接，点击后从右侧滑出预览抽屉：能阅读的类型就地渲染（文本/代码/markdown/json、图片、pdf、音频/视频），不能阅读的类型给出下载按钮。文件通过新增的带鉴权 + 路径穿越防护的管理路由 `GET /api/v1/files/<project>/<相对路径>` 提供（按 project 的 `work_dir` 解析根目录，`?download=1` 强制附件下载）。缺目录前缀的文件引用（如只写 `app.ts` 实际在 `src/app.ts`）会按「唯一同名文件」递归补全，歧义或不存在则不生成链接，避免死链。仅在 web/bridge 平台生效，wecom/feishu 等其它平台行为不变；无需新增配置项。

## v1.1.8 (2026-08-27)

### Added

- **平台级空闲会话切换覆盖**：`[[projects.platforms]]` 条目新增 `reset_on_idle_mins`，可仅对单个平台覆盖项目级空闲切换开关（`0` = 该平台禁用切换，其余平台沿用项目级值）。典型场景：虚拟 `type = "web"` 管理后台会话永久保留（`reset_on_idle_mins = 0`），而同一项目的企微 IM 仍保留项目级 `720` 分钟切换。keyed by 小写平台类型，与既有 `platformDisplayOverrides` 约定一致；启动与热重载均生效。新增 `config.PlatformConfig.ResetOnIdleMins`（含 `>= 0` 校验）、`Engine.SetPlatformResetOnIdleOverrides` / `resolveResetOnIdleForPlatform`，及 core/config 单测与配置示例文档。

## v1.1.7 (2026-08-26)

### Fixed

- **子 agent 会话 id 顶替父会话 id 致 resume 空转（根因修复）**：codebuddy stream-json 模式下，子 agent 派生时 CLI 会再发一条 `system/init` 事件（携带子会话 id），适配器无条件接受所有 init → 顶层会话 id 被子 id 覆盖 → turn 结束时引擎把子 id 持久化为 `agent_session_id` → 下一条消息 `--resume` 子 id，而子会话没有独立 `.jsonl`（ENOENT）→ CLI 静默退出，turn 空转结束（tools=0、tokens=0、仅 "(空响应)"）。现在 codebuddy 只接受**进程内第一条 init** 作为顶层会话 id（`shouldTrackInitSessionID`），子 agent 的 init 仅记 debug 日志；qoder 同样修复（原先任意事件的 session_id 均无条件覆盖）。ACP 路径不受影响（子事件按 parentToolCallId 标记，不改写跟踪 id）。
- **污染绑定自愈（兜底）**：适配器静默退出错误携带 `EventMetadataSessionUnrecoverable` 标记并标记会话 dead；引擎识别后 `DetachAgentSession()`（旧 id 记入 `past_agent_session_ids` 可追溯）→ 下一条消息自动从全新会话开始，不再永久卡死。存量被污染的会话升级后会失败一次随即自愈；普通瞬时错误不触发解除。回归测试以事故真实 id（顶层 `dc918b77` / 子 `d492df45`）为断言。

## v1.1.6 (2026-08-26)

### Fixed

- **codebuddy/qoder 零输出退出不再静默（诊断盲区修复）**：CLI 子进程干净退出（exit 0）且 stdout 零输出时，旧路径把 stderr 直接丢弃并发出空 EventResult——用户只看到 "(空响应)" 而 turn 看似成功，真实原因（`--resume` 失效、认证/网络静默失败等）无从排查。现在：stderr 内容记入日志；零输出退出上抛显式 `EventError`（有 stderr 时透传内容，经引擎 sanitize 后用户可见明确报错）。codebuddy 与 qoder 同构修复，新增 `exitFallbackEvent` 分支矩阵单测。

## v1.1.5 (2026-08-26)

### Fixed

- **Web 切换会话后工具进度失联**：turn 进行中切到其他会话再切回时，进度预览消息已随前端 state 被 history 覆盖，后续 `update_message` 因 `preview_handle` 失配被静默丢弃——新工具调用完全不可见，只能等 turn 结束收最终回复。现在失配时改用 `update_message` 携带的全量进度内容新建消息挂回该 handle：切回后下一个工具事件即重现完整进度（旧+新工具），并继续实时更新；turn 结束照常 finalize，已结束的进度不会被误复活。

## v1.1.4 (2026-08-26)

### Added

- **会话执行状态展示（多会话并行可见）**：管理 API 会话列表/详情新增 `running`（前台 turn 或后台 reader 活跃）与 `waiting_permission`（权限确认等待中）字段；Web 会话抽屉、聊天页头部、Sessions 页、项目对话卡片均显示「执行中 / 等待授权」状态徽标，5 秒轮询刷新。
- **Web 工具进度完整展示**：Web 注册时声明 `progress_max_entries: 0`（不限），解除后端 coalesced 进度默认 10 条截断——该上限本为 IM 平台消息尺寸所设，Web 走 WebSocket 无此限制。截断场景下可见条目按真实序号编号（带丢弃偏移），消除 "9. 🔧 工具 #18" 这类窗口序号与真实序号错位。
- **子代理工具标记**：`tool_messages` 开启时，子 agent（Agent 工具派生）的工具调用/结果显示 `↳` 前缀，与主 agent 动作一眼区分；标记嵌于工具名位置，不影响 WeCom/Feishu 的 `🔧 **` 前缀解析。

### Fixed

- **Web 消息复制在非安全上下文失效**：通过 `http://IP:端口` 访问时 `navigator.clipboard` 不可用，旧实现静默抛错、点击无任何效果。现降级为隐藏 textarea + `execCommand('copy')` 同步复制（保留用户手势），按钮改为诚实三态反馈（✓ 成功 / ✗ 失败），并常显低透明度替代 hover-only。

## v1.1.3 (2026-08-26)

### Fixed

- **codebuddy 适配器：`-` 开头的 prompt 不再被 CLI 拒绝（自定义命令修复）**：codebuddy CLI 的 `-p` 是布尔开关、prompt 为位置参数，此前 heron 把 prompt 直接拼在 `-p` 后，凡以 `-` 开头的内容（如执行 `/audit` 等自定义命令时整个 `.md` 文件的 YAML frontmatter 以 `---` 开头）都会被 CLI 解析成选项名，报 `error: unknown option '---\n...'` 后进程 exit 1，无任何 result。现在参数构造改为所有选项在前、`--`（end-of-options 分隔符）之后紧跟 prompt，任意内容（含 frontmatter、用户 `-` 开头消息）均可原样传递。附带 `launchArgs` 单元测试。

## v1.1.0 (2026-08-26)

### Added

- **会话自动标题（IM 会话在 Web 端可区分）**：企微等 IM 创建的会话此前在 Web 管理后台标题全部显示群名/用户名（chat 级共享的 UserMeta），同一群/用户的多个会话无法区分。现在首轮对话完成后自动生成会话级标题——**优先使用 agent 后端自带的会话摘要**（ACP session Title、claudecode/codex 的 summary，按 AgentSessionID 匹配），**兜底使用首条用户消息截断（30 字）**（codebuddy 等无 ListSessions 的 agent）。自动标题异步写入，不阻塞回复投递；写入前重查占位名，与用户此刻的手动命名不冲突。
- **配置项 `auto_session_title`**：按项目关闭自动标题，默认 true。用户手动命名（`/new <名称>`）永远不会被覆盖。
- **Web 会话来源标识**：会话卡片主标题显示会话名，次要 Badge 显示来源（user_name/chat_name）；标题 fallback 链统一为 name → user_name → chat_name → ID 截断（会话列表、抽屉、聊天页三处一致）。

### Fixed

- **Web 端不再显示占位会话名**：管理后台会话列表/详情接口把占位名（"default"/"session"）归一化为空，前端回退到有意义的标题；详情接口补发 `user_name`/`chat_name` 字段。
- **自动标题与自定义名索引隔离**：新增 `Session.NameAuto` 持久标记，防止 agent 切换（`InvalidateForAgent` 后 `CompareAndSet` 再次成功）时自动标题被误提升进 IM `/list` 的自定义名索引（sessionNames），该索引语义保持"仅用户手动命名"。

## v1.0.21 (2026-08-25)

### Fixed

- **`/new` 不再清空旧会话的历史与 agent_type（Web 会话列表可见性修复）**：`/new`（IM）与 Web 的强制新会话原来会同时清空旧活跃会话的 `history` 和 `agent_type`，导致旧会话在 Web 列表里变成空壳、不可见/不可追溯。改为只 `DetachAgentSession`（仅清掉可恢复的 `agent_session_id`，旧 id 记入 `past_agent_session_ids`），保留历史、类型、名称。旧会话作为"过往对话"继续可被浏览与检索。
- **Web 多浏览器/多机器同时登录会话串号修复**：多个 Web 客户端共用同一 `session_key=bridge:web-admin:<project>`，会在 `agent_session_id` 绑定上互相覆盖，触发 `interactive session mismatch → recycling → kill` 空响应。改为按连接隔离：`bridge:web-admin:<project>:<connID>`，每个浏览器/标签页/机器各自独立会话（与 IM 按用户隔离一致）。

## v1.0.20 (2026-08-25)

### Added

- **Web 聊天回复复制自动剥离运行时页脚**：最终回复气泡的复制按钮在复制时剥离末尾 `*model · usage · path*` 运行时页脚，只复制正文。

### Changed

- **Web 执行中显示红色暂停按钮**：Agent 处理中（typing 或消息 streaming）输入框保持可点击，发送按钮变为红色方块暂停按钮，点击自动发送 `/stop` 终止执行；执行中按回车不再误发新消息。
- **Web/bridge 的 tool/thinking 进度不再裁剪**：新增可选能力接口 `MessageSizeLimitProvider`。IM 平台（feishu/discord 等）声明自身单条消息长度上限（默认 4000）后保持原裁剪行为；Web（bridge，经 WebSocket 传输无单条长度限制）不实现该接口，tool/thinking 合并气泡保留全部内容，不再被 3800 字符截断。属平台能力对齐，无需为 Web 单独配置。

## v1.0.19 (2026-08-25)

### Fixed

- **Web 聊天输入法（IME）回车误发**：中文拼音等输入法组合期间按回车（选词/确认字母）会误触发发送半成品。发送分支增加 `e.nativeEvent.isComposing` 判断，组合未结束时忽略回车，组合结束后回车才真正发送；移动端/英文输入不受影响。

## v1.0.18 (2026-08-25)

### Changed

- **Web favicon 替换为 heron icon**：源 `icon.png`（1024×1024，340KB）缩放为 64×64 PNG（6.2KB），删除旧 `favicon.svg`，`index.html` 改为引用 `/favicon.png`。全仓无其他位置引用该图标（无 PWA manifest / Go embed 依赖）。

## v1.0.17 (2026-08-25)

### Fixed

- **Web 聊天窗口 PC/移动端高度分开 + 顶栏按路由自动收起**：
  - 消息历史区 `max-h` 改为响应式：移动端 `calc(100vh - 136px)`、PC 端（`md:` 及以上）`calc(100vh - 192px)`，修复 PC 端消息区被压窄的问题。
  - 顶部栏收起状态按路由默认：移动端进入 `/chat` 自动收起（聊天区占满高度），离开聊天回到其他页面自动展开，无需手动点收起/展开。桌面端收起 UI 本就 `md:hidden`，不受影响。

## v1.0.16 (2026-08-25)

### Fixed

- **Web 聊天窗口交互细节修正（用户实测反馈）**：
  - 聊天根容器去掉 `px-4 md:px-6` 水平内边距，聊天区铺满宽度不再显窄。
  - 消息历史区加 `px-2`（仅历史记录左右留白，顶部栏与输入框不加）及 `max-h-[calc(100vh-124px)]`，确保只在可视范围内滚动。
  - **横向溢出封死**：消息历史容器加 `overflow-x-hidden`，气泡加 `min-w-0 break-words`，长 URL/无空格长串自动换行；代码块与 Markdown 表格仍各自在气泡内 `overflow-x-auto` 单独横滚，不波及窗口。
  - **顶部栏收起浮层（两层）对齐**：收起后浮层簇由 `left-2` 改 `right-2`（右上角），其 `⋯` 下拉由 `left-0` 改 `right-0` 向右展开，文字不再被屏幕左缘裁掉。展开态移动端 `⋯` 下拉本就 `right-0`，保持一致。

## v1.0.15 (2026-08-25)

### Fixed

- **Web 聊天页滚动区域修正**：此前聊天页 `<main>` 自身带 `overflow-y-auto`，导致长会话时顶部栏与输入框随消息一起整体滚动，难以稳定停在顶部/底部。现在聊天路由下 `<main>` 改为 `overflow-hidden` 的固定高度列，消息列表成为**唯一滚动区**（顶部栏、输入框 `shrink-0` 固定）；ChatView 根容器补 `px-4 md:px-6` 保持原有左右边距。其他页面滚动行为不变。

## v1.0.14 (2026-08-25)

### Added

- **Web 移动端顶部栏整体收起（两层结构）**：针对移动端「顶部栏占太多垂直空间」的反馈，桌面端布局与交互完全不变。
  - 顶部栏在移动端（`<md`）新增「收起」按钮（chevron-up），点击后整栏从布局中移除，聊天窗口占据满高；聊天记录仍是唯一的滚动区域（头部 + 输入框 `shrink-0`），滚动行为符合预期。
  - 收起后在左上角保留一个**浮层按钮簇**（`fixed top-2 left-2 z-30`），保持两层入口都可达：汉堡（导航，第 1 层）+ ⋯ kebab（刷新/语言/主题/退出登录，第 2 层）；kebab 下拉底部含「展开」按钮可还原整栏。
  - 桌面端（`md:` 及以上）保持原有的内联 4 个工具按钮 + 无收起按钮，行为不变。
  - 5 个语言包补充 `common.more` / `common.theme` / `common.collapse` / `common.expand`。

## v1.0.13 (2026-08-25)

### Added

- **Web 移动端自适应（一套代码）**：针对移动端体验差的痛点做纯响应式优化，桌面端布局与折叠行为完全不变。
  - 侧边栏在桌面端（`md:` 及以上）保持原有固定栏 + 折叠逻辑；移动端改为**左滑 off-canvas 抽屉**，由 Header 汉堡按钮（`md:hidden`）打开、背景遮罩关闭，路由切换自动收起。
  - `Layout` 主区内边距 `p-6` → `p-4 md:p-6`（窄屏省空间）。
  - `ChatView` 根容器 `h-[calc(100vh-8rem)]` → `flex-1 min-h-0`（消除裁剪/双重滚动）；消息列表 `py-6` → `py-4 md:py-6`；气泡内边距与用户气泡宽度在移动端加宽（`max-w-[85%] sm:max-w-[70%]`）；头部标题 `min-w-0` + `truncate` 防止长项目名挤压状态徽标。
  - 命令面板 `w-80` 加 `max-w-[calc(100vw-1rem)]`、会话抽屉 `w-80` 加 `max-w-[90vw]`，与 `CommandResultPanel` 一致，避免 375px 屏溢出。
  - Markdown 表格加 `overflow-x-auto` 横向滚动包裹，避免窄气泡内表格溢出。
  - **对话界面（`/chat`）不再展示底部 copyright footer**，其他页面照常显示。
  - 5 个语言包补充 `common.menu`（汉堡按钮 aria-label）。

## v1.0.12 (2026-08-25)

### Fixed

- **Web 思考/工具过程执行后保留展示**：此前 Web 端在工具执行完毕（或整轮结束）后，思考过程与工具调用会「消失」。根因是后端对 `web` 平台（未声明 `delete_message` 能力）把思考/工具作为 `preview_start` + `update_message` 发送、且从不删除；而前端旧的 `reply`/`reply_stream`(done) 处理逻辑用 `findIndex(m => m.streaming && m.role === 'assistant')` 找到的第一个流式消息恰好是思考/工具进度预览，于是被最终答案「就地替换」掉。现在最终答案只会更新/追加**没有 `previewHandle` 的**答案占位消息，绝不覆盖思考/工具进度预览；并在答案到达时把仍在流式的进度预览置为完成态（停止脉冲），使其稳定保留在聊天历史中。
- **新增回归测试** `TestBridge_WebThinkingToolPersist`：用 Web 前端的真实能力集驱动整轮交互，断言思考/工具内容以 `preview_start`+`update_message` 下发、无 `delete_message`、最终答案以独立 `reply` 下发，从后端侧坐实上述修复。

## v1.0.7 (2026-08-24)

### Fixed

- **ACP agent 支持 Skills 目录识别**：`type = "acp"` 且 `command = "codebuddy"`（如 `codebuddy --acp`）的项目此前在 Web「技能」页面永远看不到任何 skill，因为通用 ACP adapter 没有实现 `SkillProvider` 接口。现在会根据 `command` 识别出底层 CLI 并扫描对应的 `.codebuddy/skills` 目录（项目级 + 用户级），与专用 `type = "codebuddy"` agent 行为一致。
- **CodeBuddy 模型列表反映 `.codebuddy/models.json`**：`type = "codebuddy"` 与 `type = "acp"`（`command = "codebuddy"`）两种接入方式的 `AvailableModels()` 此前是硬编码的静态模型列表，不反映用户在 `models.json` 里自定义的模型。现在会按 CodeBuddy Code 官方文档的优先级（项目级覆盖用户级，`availableModels` 白名单过滤）合并解析 `~/.codebuddy/models.json` 与 `<workDir>/.codebuddy/models.json`，读取失败时回退到内置列表。ACP 场景下仅当 ACP 协议握手未上报任何模型时才触发该回退，协议上报的模型列表始终优先。

## v1.0.6 (2026-08-24)

### Added

- **Web 会话列表新增搜索与创建**：`/sessions` 会话列表页新增按会话名称/用户/消息内容搜索,并支持直接创建新会话(选择项目 + 可选命名),创建后跳转到聊天窗口即可对话。
- **新增 `Engine.ForceNewSession`**：管理 API `POST /projects/{name}/sessions` 现在会真正创建一个全新会话(此前该接口只会返回已存在的活跃会话,不会新建)。

### Fixed

- **统一聊天窗口实现**：删除功能阉割的重复组件 `SessionChat.tsx`,`/chat` 与 `/sessions` 列表现在都指向同一个 `ChatView` 聊天窗口(支持斜杠命令面板、命令结果卡片),`ChatView` 新增可选会话 ID 路由参数(`/chat/:project/:id`)以支持直达指定会话。
- **品牌残留清理**：Codex RPC 的 `clientInfo.title` 与 Web 侧边栏品牌文字中残留的 "CC-Connect"/"CC" 改为 "Heron Connect"/"HC"。

### Changed

- **导航文案调整**：侧边栏"对话"改为"项目对话",以区分按项目分组的对话列表与跨项目的会话列表("会话")。

## v1.0.5 (2026-08-21)

### Added

- **恢复 Web 管理后台**：重新加入内嵌 React 前端（项目管理、会话监控、定时任务编辑、Provider 管理、聊天界面，支持中/英/繁中/日/西多语言），并恢复 `heron-connect web` 命令与聊天内 `/web setup`、`/web status` 命令。Web 后台在未使用 `no_web` 构建标签时随二进制编译（`make build` 默认包含，`make build-noweb` 不包含）。

### Fixed

- **补全命令描述**：为 `web`、`tts`、`whoami`、`workspace`、`heartbeat`、`cancel` 六个内置命令补齐 5 语言描述（此前在 bot 菜单/帮助卡片中会回退为原始命令名），并将 `/web` 加入帮助卡片的系统分组。

### Changed

- **文档补充**：在 README 与 `config.example.toml` 中补充 Web 管理后台的使用说明与 `[management]`/`[bridge]` 配置说明。

## v1.0.4 (2026-08-20)

### Changed

- **版本号占位**：v1.0.4 发布时源码未包含 Web 管理后台，相关能力顺延至 v1.0.5 一并发布。

## v1.0.3 (2026-08-17)

### Changed

- **Cron shell 任务全程静默**：shell 类型的 cron 任务不再发送「开始通知」`⏰ desc` 和「进行中」`⏰ ⏳` 进度消息，完全由脚本决定是否推送——仅在脚本有实际 stdout 输出或失败/超时时才发送结果消息。

## v1.0.2 (2026-08-17)

### Changed

- **Cron shell 任务空输出不再推送消息**：shell 类型的 cron 任务执行成功且 stdout 无输出时，不再发送 `✅ (no output)` 消息，改为静默完成；仅当脚本有实际输出或失败时才推送结果。由脚本决定是否发送消息。

## v1.0.1 (2026-08-13)

### Changed

- **品牌更名为 heron-connect**：项目从 cc-connect-qhn 更名为 heron-connect，npm 包改为 `@qinghuangniao/heron-connect`，二进制/命令、数据目录、GitHub 仓库与 Go module 路径全部同步更新。
- **移除 Web 前端**：删除内嵌 React 前端与 `heron-connect web` 命令，保留 HTTP Management API。
- **修正 CodeBuddy 安装包名**：从错误的 `@anthropic-ai/codebuddy-code` 更正为腾讯官方的 `@tencent-ai/codebuddy-code`。

## v2.0.6 (2026-08-12)

### Added

- **企微引用消息标注**：当用户引用（quote/reply）消息时，为引用原文添加 `[引用消息]` 前缀，使模型能够区分引用内容与用户当前输入；文本和语音引用均支持。

### Changed

- **ACP client 版本号**：ACP `initialize` 中的 `clientInfo.version` 不再硬编码，改为读取编译时注入的版本号，与主程序版本一致。

## v2.0.5 (2026-08-12)

### Fixed

- **ACP 子 Agent `_meta` 解析位置**：将 `parentToolCallId` / `parentToolUseId` 的解析优先级改为先读 `params.update._meta`（ACP 协议实际位置），再回退 `params._meta`，修复 `tool_messages = false` 仍展示子 Agent 内容的问题。
- **启动日志版本号**：`cc-connect-qhn is running` 日志增加 `version` 字段，便于从日志确认实际运行的版本。

## v2.0.4 (2026-08-11)

### Added

- **`cron_data_dir` 配置**：在 `[cron]` 段新增 `cron_data_dir` 字段，可显式指定 cron `jobs.json` 的存储目录；空值时回退到顶层 `data_dir`，保持向后兼容。支持相对路径（基于进程工作目录解析），方便多环境部署。

### Docs

- **IM 群聊会话隔离说明**：新增 `docs/session-isolation.zh-CN.md`，说明各平台群聊的会话隔离/共享规则、`share_session_in_channel`、`thread_isolation` 及选型建议。

## v2.0.3 (2026-08-11)

### Fixed

- **ACP 子 Agent 元数据兼容**：同时支持 CodeBuddy 实际使用的扁平字段 `_meta["codebuddy.ai/parentToolCallId"]` 与原有嵌套字段 `_meta["codebuddy.ai"]["parentToolUseId"]`；扁平字段存在时优先使用，确保实际 ACP 通知中的子 Agent 事件能被正确识别和按 `tool_messages` 控制显示。

## v2.0.2 (2026-08-10)

### Fixed

- **ACP 子 Agent 显示控制**：识别 CodeBuddy ACP `Agent` 工具调用及其关联的子 Agent 事件；`tool_messages = false` 时隐藏子 Agent 的文本、思考和工具细节，同时保留主 Agent 回复。
- **企微 WebSocket 长回复**：流式预览使用 20,480-byte 内容上限，并保留安全展示余量；超出预览预算后实时续发后续消息，不再插入“内容较长”提示。
- **企微流式格式与投递可靠性**：分段保持 UTF-8 边界和 Markdown 代码围栏完整；ACK 超时不再被视为已投递；终态帧提交后阻止旧的预览和工具进度更新覆盖最终回复。
- **企微工具进度**：工具开始或完成时可建立或复用流式预览，使工具进度与已有回复共同展示，并在最终回复中清除进度区域。

### Tests

- 增加 ACP 子 Agent 标记、嵌套关系和 `tool_messages` 显示控制的覆盖。
- 增加企微流式 UTF-8、字节上限、代码围栏续传、分段失败重试、终态屏障和工具进度的回归测试。

## v2.0.1 (2026-08-04)

**New agent + model refresh**: Adds `codebuddy` headless CLI driver, completes the `cmd/cc-connect` → `cmd/cc-connect-qhn` directory rename, refreshes stale model lists across all agents and provider presets.

### Added

- **`type = "codebuddy"` agent**: Headless stream-json CLI driver (`codebuddy -p <prompt> --output-format stream-json`), following the same per-turn spawn pattern as qoder/gemini/kimi. Supports `mode`, `model`, `SkillDirs`, `MemoryFileProvider`, and `ContextCompressor`. Includes example config (`examples/feishu-codebuddy.toml`).

### Changed

- **`cmd/cc-connect` → `cmd/cc-connect-qhn`**: Directory renamed to complete the v2.0.0 branding migration.
- **Model lists refreshed across all agents**: `gemini` adds `gemini-3.5-flash`; `kimi` adds `kimi-k3`; `opencode` updates `gpt-4o` → `gpt-5.4`; `codex` adds `gpt-5.4`/`gpt-5.4-mini`; `cursor` fallback models updated.
- **`provider-presets.json`**: All Claude presets now default to `claude-sonnet-5`; Gemini presets default to `gemini-3.1-pro-preview` with `gemini-3.5-flash`.
- **Codex docs**: Added third-party provider examples (DeepSeek, MiniMax) with `wire_api` guidance in `examples/feishu-codex.toml` and `config.example.toml`.

### Fixed

- **codebuddy model naming**: Fixed dotted (`claude-sonnet-4.6`) → dashed (`claude-sonnet-4-6`) to match rest of repo.
- **Stale model IDs in tests**: Updated `claude-opus-4-7` → `4-8`, `kimi-k2` → `k3`, `gemini-2.5-*` → `3.1-*` across test fixtures.

## v2.0.0 (2026-07-31)

**Breaking branding change**: All user-facing log/output strings renamed from `cc-connect` to `cc-connect-qhn`. Binary filename, GitHub release assets, updater APIs all switched to the fork's own identity.

### Breaking

- **Binary renamed**: Go binary output changed from `cc-connect` to `cc-connect-qhn`. Makefile `APP`, npm wrapper scripts (`run.js`, `install.js`, `release-assets.js`), and release asset naming all updated accordingly.
- **GitHub release assets renamed**: `cc-connect-v2.0.0-darwin-arm64.tar.gz` → `cc-connect-qhn-v2.0.0-darwin-arm64.tar.gz`.
- **All CLI help/output text renamed**: `Usage: cc-connect [flags]` → `Usage: cc-connect-qhn [flags]`, all examples, error messages, daemon status messages, platform setup prompts updated across ~30 Go files.
- **Log message renamed**: `"cc-connect is running"` → `"cc-connect-qhn is running"`.
- **Agent system prompt renamed**: Agent-visible context (system prompt, sender markers, instruction marker) uses `cc-connect-qhn` instead of `cc-connect`.
- **Daemon service renamed**: Systemd unit description `cc-connect` → `cc-connect-qhn`, launchd label `com.cc-connect.service` → `com.cc-connect-qhn.service`, service name const `cc-connect` → `cc-connect-qhn`.
- **ACP session names renamed**: ACP `clientInfo.name` and session titles use `cc-connect-qhn`.
- **i18n strings renamed**: All localized help/status strings across 5 languages updated.
- **Weixin channel version renamed**: `cc-connect-weixin/1.0` → `cc-connect-qhn-weixin/1.0`.
- **Windows daemon script renamed**: `cc-connect-daemon.ps1` → `cc-connect-qhn-daemon.ps1`.

### Changed

- **Updater repo URLs switched from upstream to fork**: `CheckForUpdate` and `SelfUpdate` in `core/updater.go`, plus CLI `update` command in `cmd/cc-connect-qhn/update.go`, now query `janostudio/cc-connect-qhn` instead of `chenhg5/cc-connect` / `cg33/cc-connect`.
- **npm package version sync fixed**: `syncNpmPackageVersion` now uses `strings.Contains` for scoped package `@qinghuangniao/cc-connect-qhn` name matching.

### NOT changed (preserved for compatibility)

- **Go import paths**: `github.com/chenhg5/cc-connect/...` unchanged.
- **`.cc-connect-qhn` data directory**: Already had `-qhn` suffix, unchanged.
- **npm global command**: `cc-connect-qhn` unchanged (already correct).

### Why v2.0.0

This is a semver-major bump because:
1. The `ServiceName` const and launchd label change means existing daemon installations need re-install.
2. The agent system prompt change means agents using memory files with old `cc-connect` instructions will get them refreshed on next run.
3. The updater URL change means the auto-update path switches from upstream to the fork — users on old versions won't see updates from the new repo.

### Files changed (35 files)

`agent/acp/`: list_sessions.go, session.go
`cmd/cc-connect-qhn/`: main.go, cron.go, send.go, daemon.go, provider.go, feishu.go, weixin.go, sessions.go, update.go, relay.go, session_id.go, config_cmd.go, doctor_runas.go, instance_lock.go, web.go
`core/`: engine.go, engine_bind_cmds.go, i18n.go, interfaces.go, runas_check.go, updater.go, web_manager.go
`daemon/`: manager.go, systemd.go, launchd.go, windows.go
`platform/weixin/`: client.go
`npm/`: run.js, install.js, release-assets.js
`Makefile`
Test files: update_test.go, engine_test.go

## v1.4.9 (2026-07-29)

Personal fork session idle reaper, WeCom rate-limit tracking, reassurance messages, token usage fix, and log diagnostics hardening for `@qinghuangniao/cc-connect-qhn`.

### Notes

- **Session idle reaper (360min)**: Added background goroutine `startSessionReaper` that periodically scans `interactiveStates` and kills agent processes that have received no events for longer than `resetOnIdle` (default 360 min, configurable via `reset_on_idle_mins`). This prevents zombie ACP subprocesses from accumulating. The reaper uses `lastEventTime` (updated on every agent event) with `turnStartTime` fallback. Disabled when `resetOnIdleMins = 0`.

- **WeCom per-chat rate-limit tracking**: Added `chatRateTracker` in `platform/wecom/rate_tracker.go` that counts sends per chatID with 1-min and 1-hour sliding windows. Before each `aibot_respond_msg` and `aibot_send_msg` call, the tracker checks against WeCom limits (30/min, 1000/hour per chat) with configurable buffer (5/min, 50/hour). If approaching limits, sends are throttled with a wait. After successful ack, the send is recorded.

- **WeCom 846607 retry with exponential backoff**: `writeAndWaitAck` now retries rate-limited sends (errcode=846607) up to 3 times with 3s/6s/12s backoff. Error 846608 (stream expired) is detected and not retried — returns immediately.

- **Reassurance messages during long waits**: Added a 1-minute timer in `processInteractiveEvents` that sends "⏳ 正在处理您的请求..." via stream preview when no agent output has been received. Uses WeCom's full-replacement stream semantics (`aibot_respond_msg`), so reassurance text is automatically replaced when real output arrives. No new messages are created — only existing stream content is updated.

- **Token usage extraction fix**: `maybeAbsorbUsageUpdate` now parses `_meta.usage.prompt_tokens` → `InputTokens` and `_meta.usage.completion_tokens` → `OutputTokens` from ACP `usage_update` notifications. `acpSession.Send()` reads the `usageSnapshot` and populates `EventResult.InputTokens`/`OutputTokens` before emitting, so `turn complete` logs now show actual token counts instead of always 0.

- **Graceful stop logging**: `acpSession.Close()` now logs per-phase elapsed time (stdin close, SIGTERM, SIGKILL) with `session_id`. Changed `process exited cleanly` from WARN to INFO (normal exit is not a warning).

- **Log diagnostics hardening**: Added `session_key`, `platform`, `msg_id` to 15+ error/warn logs in `engine_turn.go` (agent error, prompt send, rich card send, streaming card finalize, channelClosed). Added `session_key`, `platform`, `user`, `request_id`, `tool` to permission error logs in `engine.go`. Added `chat_type` to WeCom send errors. Standardized `"err"` → `"error"` key naming across codebase. Upgraded streaming preview degradation from Debug to Warn with platform name.

### Tests

- `agent/acp/session_test.go`: 4 new tests (`TestMaybeAbsorbUsageUpdate_*`) — parses meta usage, legacy format, non-usage update, zero total_tokens fallback.
- `core/engine_test.go`: 6 new tests (`TestReapIdleSessions_*`) — kills idle, skips active, skips dead, skips nil agent, disabled when zero, falls back to turnStartTime.
- `platform/wecom/websocket_test.go`: 10 new tests (`TestChatRateTracker_*`, `TestWriteAndWaitAck_StreamExpiredNoRetry`, `TestIsErrCode`) — rate tracking, cleanup, concurrent access, error code detection, stream expired no-retry.

### Docs

- `docs/wecom-optimization.md`: Full optimization plan with problem analysis, implementation details, test cases, and configuration.
- `.codebuddy/plans/stellar-thunder-newton-BxOjesy5.md`

## v1.4.8 (2026-07-23)

Personal fork session switch / list fix, error sanitization, and `/list` fallback hardening for `@qinghuangniao/cc-connect-qhn`.

### Notes
- **Fix `/list` and `/switch` user isolation**: `sessionsFromSessionManager` previously used `AllSessions()` (cross-user). Changed to `ListSessions(userKey)`, scoped to `msg.SessionKey`. Added the same fallback in `cmdSwitch` so `/switch` works after restart when the agent backend reports no sessions (ACP without `session/list`).
- **`/list` shows last user message as summary**: fallback path now fills `MessageCount=len(History)` and `Summary` from the last `role=="user"` entry (truncated to 30 runes), making sessions distinguishable after restart.
- **EventError desensitization**: all 4 code paths that relay agent errors to IM users (foreground EventError, unsolicited EventError, `Send` error, dropped-queue notifications) now route through `sanitizeAgentError` → `sanitizeAgentErrorMessage`. The function returns localized i18n messages for known patterns (`Session not found` → `MsgSessionNotFound`, quota/rate-limit → `MsgModelQuotaExceeded`, ACP internal errors → `MsgAgentInternalError`, process exits → `MsgAgentProcessExited`) and default-denies everything else to a generic `MsgAgentInternalError`. Raw errors remain in logs and hooks only.
- 3 new i18n keys: `MsgAgentInternalError`, `MsgAgentProcessExited`, `MsgAgentUnsupportedMethod` (5 languages each).
- **Fix pre-existing flaky `TestGetModelAndReasoningEffort_FromRuntimeConfigWhenUnset`**: shell mock pattern `*"method":"initialize"*` incorrectly matched `"initialized"` notification, spawning unnecessary `printf|sed` subprocesses under load. Changed patterns to `*"method":"initialize","*` / `*"method":"config/read","*` and moved `id` extraction inside case branches. Bumped test timeout to 5 s.

### Tests
- `TestSessionsFromSessionManager_*` updated for `userKey` parameter, plus 4 new tests: `_userIsolation`, `_fillsSummaryAndCount`, `_summaryTruncates`.
- `TestSanitizeAgentErrorMessage` (17 cases covering known-friendly, ACP-internal, stderr, stack traces, path-like, unknown).
- `tests/release_local/engine_matrix/restart_persistence_test.go` (5 restart-scenario tests with a fake ACP agent).

### Docs
- `.codebuddy/plans/toasty-vortex-einstein-liQoVUJT.md`

## v1.4.7 (2026-07-21)

Personal fork `/model` live switch, ACP refusal error surfacing, and orphaned subprocess cleanup for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix `/model` not applying to the current session. Previously `/model` always killed the running agent subprocess and deferred the model change to the next incoming message (which respawned with `--model <new> --resume <id>`). This worked but was not "immediate" — the `/model` reply itself was sent by cc-connect, not the new model, and on ACP backends the running subprocess was torn down even though ACP supports in-process model swaps. Introduced `core.LiveModelSwitcher` interface (mirroring the existing `LiveModeSwitcher` pattern used by `/mode`). `cmdModel` now first attempts `applyLiveModelChange` on the running `state.agentSession`; on success the subprocess is kept alive and the reply appends a localized "Current session updated immediately." notice. On failure (or when the agent doesn't implement the interface) it falls back to the legacy `cleanupInteractiveState` + `--resume` respawn path — non-ACP agents (opencode/codex/claudecode/qoder/pi/kimi/cursor/gemini/iflow) keep their existing behavior since their model is a `--model` CLI flag baked at spawn time.
- Implement `LiveModelSwitcher` on `*acpSession`. The new `SetLiveModel` method reuses the existing `SetModel` RPC path (`session/set_model` with `session/set_config_option` fallback), which was previously only invoked at session spawn. ACP backends now hot-swap models without restarting the subprocess.
- Mirror the live-switch branch in both card-action entry points (`handleModelCardAction`, `performModelSwitchAsync`) so `/model` invoked via Feishu/WeCom card buttons behaves identically to the slash command.
- Fix ACP `refusal` responses being silently sent to the user as "(空响应)". CodeBuddy wraps model-side failures (quota exceeded 429, 500 internal errors) in a JSON-RPC **success** envelope with `stopReason:"refusal"` and the real error in `_meta["codebuddy.ai/errorMessage"]`. `acpSession.Send()` previously discarded the entire `result` and unconditionally emitted `EventResult{Done:true}`, so the engine's empty-response fallback kicked in and the user got "(空响应)" while the real error was buried in logs. `Send()` now parses the `result` and, on refusal/error, emits `EventError` with the parsed `payload.Message` verbatim instead of `EventResult`. The engine's existing `EventError` handler finalizes the progress card as failed and sends the error to the user. Added `MsgModelQuotaExceeded` i18n key (5 languages) wired via the `agentErrorHandlers` substring table so quota errors get a friendly localized notice; any other refusal error passes through unchanged as `"❌ 错误: <原始 message>"` (never empty). Added `extractACPReturnError` helper that returns the full error message verbatim — never an empty string — so unmatched errors always surface to the user instead of looking like a cc-connect-qhn bug.
- New i18n keys: `MsgModelChangedLive` ("✅ Current session updated immediately." in 5 languages), `MsgModelQuotaExceeded` ("⚠️ AI 服务暂时不可用：使用额度已耗尽，请稍后再试。" in 5 languages).
- **Eliminate orphaned CLI subprocesses on session end / restart.** Every `codebuddy --acp` (and other CLI) subprocess that died or was replaced was leaked because `Close()` was never called on it — stdin stayed open, no SIGKILL was sent, and on cc-connect restart via `syscall.Exec` all children became orphans (PPID=1), accumulating over weeks (34 stale `--acp` processes, ~8.9 GB RSS observed). Five root causes fixed:
  - `acpSession.Close()` never closed the events channel — the engine's `channelClosed` cleanup path (which calls `cleanupInteractiveState`) never fired for ACP sessions, leaving dead `interactiveState` entries in the map. Now closes via `sync.Once` (`closeEvents` helper) so the engine observes termination correctly; idempotent and safe against concurrent/Double `Close()`.
  - `EventError` path in `processInteractiveEvents` returned without calling `cleanupInteractiveState` when the agent session was dead, leaking the subprocess (stdin never closed, no SIGKILL). Now calls `cleanupInteractiveState` when `!Alive()`, which closes the agent session and removes the state from the map. The previous `notifyDroppedQueuedMessages` call was removed to avoid duplication (`cleanupInteractiveState` already drains queued messages internally).
  - `Engine.Stop()` called `state.agentSession.Close()` serially with no timeout — a single stuck session blocked restart, and `syscall.Exec` then orphaned all still-running children (PPID=1). Now closes all sessions in parallel with a 180 s batch timeout; each session also stops its unsolicited reader and resolves pending permissions before `closeAgentSessionWithTimeout`.
  - `getOrCreateInteractiveStateWith` silently overwrote dead sessions without calling `Close()` when a new message arrived — added defensive cleanup (`stopUnsolicitedReader` + `markStopped` + `closeAgentSessionWithTimeout` + `delete`) so a dead state is always torn down before a fresh one is created.
  - No process group isolation (except Codex) — grandchildren (shell, npm, git, …) spawned by the CLI survived restart as orphans. Added shared `core.PrepareCmdForKill` / `core.ForceKillProcessGroup` / `core.SignalProcessGroup` helpers (Unix: `Setpgid` + negative-PID kill; Windows: `CREATE_NEW_PROCESS_GROUP` + `taskkill /T /F`) and wired them into all 9 agents (ACP, Claude Code, OpenCode, Gemini, Cursor, Pi, Kimi, Qoder, iFlow) plus the ACP `probeSpawn` one-shot. iFlow uses PTY (`pty.Start` calls `setsid` internally) so `Setpgid` is intentionally skipped there to avoid `operation not permitted`; the `osCmd` reference is still saved for `ForceKillProcessGroup` on timeout.
- **Fix pre-existing flaky `TestQueuedMessagePreservesFiles`.** `Engine.Stop()` did not wait for in-flight `processInteractiveMessageWith` goroutines before returning, so a late `session.Save()` could race with `t.TempDir()` cleanup and report `directory not empty`. Added `turnWg sync.WaitGroup` to `Engine` tracking all turn goroutines; `Engine.Stop()` now waits on it (10 s bounded) before returning.
- **Fix pre-existing `go vet` warning.** `permRecordingSession` value-embedded `controllableAgentSession`, copying its `sync.Mutex` (`literal copies lock value`). Changed to pointer embedding (`*controllableAgentSession`).

### Tests
- 3 new unit tests in `core/engine_test.go` for `/model` live switch: `TestCmdModel_AppliesLiveModelWithoutReset` (live switch succeeds, subprocess kept alive, session ID & history preserved, reply mentions immediate switch), `TestCmdModel_FallsBackToRespawnWhenLiveFails` (SetLiveModel errors → falls back to cleanupInteractiveState, session ID preserved for resume, reply omits live hint), `TestCmdModel_FallsBackToRespawnWhenNoLiveSession` (no running interactive state → legacy respawn path, non-ACP behavior). Existing `TestCmdModel_KeepHistoryPreservesSessionID` still passes (verifies no regression in the respawn path).
- 1 new unit test in `agent/acp/feature_test.go`: `TestACPSession_implementsLiveModelSwitcher` (interface compliance assertion for `*acpSession` → `core.LiveModelSwitcher`).
- 8 new unit tests in `core/engine_proc_cleanup_test.go` for subprocess cleanup: `TestGetOrCreateState_DeadSessionCleanedUp` (dead session defensively closed when a new message arrives), `TestGetOrCreateState_DeadSessionWithNilAgentSession` (nil agentSession handled without panic), `TestEventError_DeadSessionTriggersCleanup` (EventError + `!Alive()` calls `cleanupInteractiveState`), `TestEventError_AliveSessionDoesNotCleanup` (per-turn errors on live sessions preserve the session), `TestEngineStop_ParallelCloseWithTimeout` (parallel close is faster than serial), `TestEngineStop_StuckSessionDoesNotBlockForever` (stuck session doesn't block quick sessions), `TestACPStyle_CloseEventsChannel` (Close closes events channel), `TestCloseAgentSessionWithTimeout_AbandonsStuckSession` (timeout protection works).
- 6 new unit tests in `agent/acp/session_cleanup_test.go` for ACP Close: `TestClose_ClosesEventsChannel` (events channel closed after Close), `TestClose_Idempotent` (multiple Close calls don't panic via `sync.Once`), `TestCloseEvents_Once` (concurrent closeEvents is safe), `TestClose_DeadSessionClosesEvents` (already-dead session still closes channel), `TestClose_AliveStateAfterClose` (alive flag cleared), `TestPrepareCmdForKill_SetsPgid` (helper smoke test).
- Full suite: 36 packages pass, 3 consecutive runs, 0 failures. (`agent/codex` has one pre-existing flaky test `TestGetModelAndReasoningEffort_FromRuntimeConfigWhenUnset` that fails intermittently due to shell-script mock timing — passes on repeat runs, unrelated to these changes.)

## v1.4.6 (2026-07-07)

Personal fork `/cancel` fix for `@qinghuangniao/cc-connect-qhn`. Fixes the bug where output kept streaming to the user after `/cancel` was issued.

### Notes
- Fix `/cancel` continuing to relay output after cancellation. Previously `cmdCancel` only sent `agentSession.CancelTurn()` (a fire-and-forget `session/cancel` notification to the agent backend) without touching any engine-local state, so the foreground event loop in `processInteractiveEvents` kept reading and relaying already-buffered chunks from the events channel (cap 128), plus any chunks the ACP server emitted between receiving the cancel and actually stopping. The loop's `select` only recognized `stopCh` (`/stop`) and `e.ctx.Done()` — there was no cancel path. A second leak: even if the loop returned, without `eventsNeedResync=true` the unsolicited reader would pick up the leftover chunks and relay them as "background" events.
- Add a per-turn `cancelCh` to `interactiveState`, created fresh at the start of each `processInteractiveEvents` call and cleared on exit (scoped to the running turn, unlike `stopCh` which tears down the whole interactive state). `cmdCancel` now closes `cancelCh` (authoritative local stop) in addition to `CancelTurn()` (best-effort server-side stop), and the event loop checks `cancelCh` with priority so cancellation is deterministic rather than racing buffered chunks. `handleCancel` finalizes the progress card, discards the streaming preview, and sets `eventsNeedResync=true` so leftover events are drained before the next turn and are not picked up by the unsolicited reader.
- `cmdCancel` now uses `cancelCh` presence (not just `agentSession != nil`) to decide whether a turn is actually in progress, and claims the channel under `state.mu` (setting it to nil) so a racing second `/cancel` sees "no turn in progress" instead of double-closing. The session stays alive after `/cancel` — the next message reuses the same `agentSession`. Bump `clientInfo.version` to `1.4.6`.

### Tests
- 2 new unit tests in `core/engine_test.go`: `TestCmdCancel_StopsTurnAndKeepsSessionAlive` (loop exits promptly, `CancelTurn` called once, `MsgTurnCancelled` sent, post-cancel events are NOT relayed, session stays alive, `eventsNeedResync` set) and `TestCmdCancel_NoTurnInProgress` (replies `MsgNoTurnInProgress` and does not call `CancelTurn` when idle; no double-close panic).
- Full suite: `core` pass (short + `-race`), cancel tests pass under `-race -count=50`.

## v1.4.5 (2026-07-01)

Personal fork streaming dedup fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix `mergeStreamDisplayContent` duplicate output bug in `displayModeStream` mode. When `finalResponse` includes metadata (e.g. `*model · usage · path*`) appended after the answer text, the old comparison (`TrimSpace(streamContent) == finalResponse`) always failed, causing `streamContent + "\n\n" + finalResponse` which duplicated the answer. Added prefix-based dedup: if `finalResponse` starts with `streamContent` (the stream was just a prefix), return `finalResponse` as-is; if `streamContent` starts with `finalResponse`, return `streamContent` as-is. Bump `clientInfo.version` to `1.4.5`.

### Tests
- 11 new unit tests in `core/engine_test.go` covering: stream-is-prefix-of-final, final-is-prefix-of-stream, exact match, empty inputs, no-match concatenation, last-assistant dedup, trailing newlines/spaces, and the real-world bug scenario from production logs.

## v1.4.4 (2026-07-01)

Personal fork ACP protocol alignment for `@qinghuangniao/cc-connect-qhn`. Brings the generic ACP agent adapter in line with the latest [Agent Client Protocol](https://agentclientprotocol.com/) spec so commands like `/list` and `/model` work against ACP servers (e.g. CodeBuddy) that previously returned "未找到此项目的会话" / "当前 Agent 不支持模型切换".

### Notes
- Implement local session tracking as a fallback for `/list` when the ACP server does not advertise `sessionCapabilities.list`. Sessions started by cc-connect are recorded locally with their cwd and an auto-extracted title (first `agent_message_chunk`), and surfaced via `ListSessions` whenever the server-side probe is unsupported or fails. This makes `/list` and `/switch` usable against CodeBuddy without any server-side changes.
- Add engine-level fallback in `cmdList`: when `agent.ListSessions()` returns empty, build the list from cc-connect's own `SessionManager.AllSessions()` so previously started sessions remain visible even if the agent backend doesn't track them. This fixes the case where `/list` returns "未找到此项目的会话" on first run against an ACP server without `session/list` support.
- Implement `core.ModelSwitcher` on the ACP agent. Model lists are parsed from both the new `configOptions` (category `model`, the v2 ACP way) and the legacy `models` field returned by `session/new` / `session/load`. `SetModel` is applied on the next `StartSession` via `session/set_model`, with a fallback to `session/set_config_option` (configId `model`) when the server returns method-not-found. This unblocks `/model` and `/model switch <id>` on CodeBuddy.
- Fix legacy `models` field parsing: CodeBuddy returns `modelId` (not `id`) as the identifier in `availableModels` entries. Introduced dedicated `acpModelEntry` type with `json:"modelId"` tag so model IDs are correctly extracted.
- Add user-facing hint when `/model` is invoked before any session has started (model list is empty because it's only populated after the first `session/new` handshake). New i18n key `MsgModelListEmptyHint` in 5 languages.
- Handle `config_option_update` notifications so the cached model list stays in sync when the server changes the active model (e.g. after a rate-limit fallback).
- Handle `session_info_update` notifications so the locally tracked session title stays in sync with server-reported metadata.
- Handle `usage_update` notifications and implement `core.ContextUsageReporter` on `acpSession` so the `/usage` command can surface server-reported token usage for ACP sessions.
- Add `title` field to `clientInfo` in `initialize` requests (both handshake and probe paths) per the ACP spec recommendation, and bump the reported version to `1.4.4`.

### Tests
- 30 new unit tests in `agent/acp/feature_test.go` covering: local session tracking & ListSessions fallback, ModelSwitcher interface & GetModel precedence, parseModels from configOptions / legacy models / mixed inputs, session/set_model RPC success & fallback to session/set_config_option, config_option_update / session_info_update / usage_update notification handling, auto-extracted title truncation & no-overwrite guard, interface compliance assertions for `ModelSwitcher` / `ContextUsageReporter` / `sessionCallbacks`.
- 4 new unit tests in `core/session_test.go` covering `sessionsFromSessionManager` fallback (returns tracked sessions, filters by agent name, ignores empty/sentinel session IDs, empty manager).
- Full suite: `agent/acp` 71 pass, `core` 740 pass (short mode), 0 failures.

### Docs
- `.codebuddy/plans/acp-command-support-plan.md` — implementation plan with gap analysis against the latest ACP protocol spec, 4 phases, and file-level change matrix.

## v1.4.0 (2026-07-01)

Personal fork WeCom streaming display alignment with openclaw progress-draft design for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Introduce three-region `wecomStreamAssembler` (`visibleText` / `progressLines` / `heldTool`) as the single source of truth for WeCom WebSocket stream preview state, replacing the old two-field `wsStreamAssembler` that mixed tool blocks into visible text.
- Add `ProgressAssembler` optional platform interface (`core/interfaces.go`) so the engine routes `EventToolUse`/`EventToolResult` to a UI side-channel in `tool_hold` mode instead of appending tool messages to the `textParts` buffer. This physically separates tool progress from the model-produced answer text, aligning with openclaw's progress-draft philosophy.
- Implement `WSPlatform.OnToolStart`/`OnToolComplete` (`platform/wecom/websocket_progress_assembler.go`) to route tool progress into `progressLines`, then merge with `visibleText` via `UpdateMessage` so the preview shows both together instead of alternating flicker.
- Wire `WSPlatform.UpdateMessage` to track received text in `assembler.visibleText` and send the merged render (`progressLines + separator + visibleText`), so tool progress and answer text are always displayed together.
- Wire `WSPlatform.FinalizePreviewMessage` to call `assembler.finish(finalText)`, which clears `progressLines` so the final frame contains only the answer (no residual tool progress).
- Remove `streamToolHoldNeedsAnswerSeparator` hack from `core/engine_turn.go` (4 call sites) since the assembler now owns separation via `render()`.
- Remove `shouldHoldOnlyTool`/`holdTool` guessing logic from `platform/wecom/websocket_stream_queue.go` since tool holding is now driven explicitly by the engine via `ProgressAssembler` events.
- Add `SessionIDRotator` optional interface (`core/interfaces.go`) and implement it on `acpSession` so ACP backends that rotate session IDs on spawn refresh the persisted binding unconditionally, while non-ACP sessions keep `CompareAndSetAgentSessionID` semantics (only set when empty or sentinel). Fixes `TestSessionIDWriteback_DoesNotOverwriteExisting` regression introduced by commit `6e70f87`.

### Tests
- 16 new `wecomStreamAssembler` unit tests covering invariants I1-I6 (visibleText isolation, progressLines FIFO bounding, render idempotency, finish clears progress, heldTool overwrite) and paths G1-G6 (appendText/onToolStart/onToolComplete region isolation, snapshot read-only, formatToolLine explain/raw modes, truncateMiddle).
- 2 updated engine integration tests (`StreamModeToolHoldRoutesToolProgressToAssembler`, `StreamModeMultiToolRoutesAllToAssembler`) expressing the new contract: tool progress must NOT appear in any preview message, must be routed via `ProgressAssembler`.
- 5 new WeCom integration tests (`UpdateMessage_MergesProgressAndVisibleText`, `OnToolStart_DoesNotSendStandaloneFrameWhenNoVisibleText`, `FinalizePreviewMessage_ClearsProgressAndSendsOnlyAnswer`, `UpdateMessage_TracksVisibleTextInAssembler`, `FullFlow_TextToolTextFinalize`) verifying the end-to-end打通: visible text and tool progress are merged before sending, finalize clears progress.
- Full suite: `platform/wecom` 89 pass, `core` 736 pass, `agent/acp` 34 pass (short mode), 0 failures.

### Docs
- `docs/upgradefeature/wecom-stream-align-openclaw-design.md` — technical design with 18-module alignment matrix, 4-scenario before/after comparison, 5-phase rollout plan.
- `docs/upgradefeature/wecom-stream-tdd-gap-analysis.md` — TDD gap analysis with 5 reverse-failing tests, 12 uncovered paths, 22 recommended new tests.

## v1.3.12 (2026-06-05)

Personal fork codebase modularization for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Split the monolithic `core/engine.go` (~13,800 lines) into 14 focused files (`engine_admin_cmds.go`, `engine_alias_cmds.go`, `engine_bind_cmds.go`, `engine_card_actions.go`, `engine_cards.go`, `engine_cron.go`, `engine_info_cmds.go`, `engine_model_cmds.go`, `engine_provider_cmds.go`, `engine_relay.go`, `engine_reply.go`, `engine_session_cmds.go`, `engine_shell_cmds.go`, `engine_turn.go`) for improved maintainability.
- Extract six sub-systems out of `core/` into dedicated top-level packages: `relay`, `webhook`, `bridge`, `api`, `management`, `proxy`, reducing core coupling and enabling independent testing.
- Introduce `core.RelayManagerAPI` interface so `core/` references relay via interface rather than a concrete struct, breaking the circular import.
- Refactor WeCom WebSocket stream handling into three focused files: `websocket_stream_assembler.go`, `websocket_stream_queue.go`, `websocket_stream_reply.go`.
- Add public Engine accessor methods (`Commands()`, `HandleRelayRequest()`, `SendMessage()`, `ProcessInteractiveMessage()`) to expose internal functionality for the extracted packages without breaking encapsulation.

## v1.3.11 (2026-05-28)

Personal fork WeCom stream dedup regression fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom WebSocket stream aggregation so a finalized answer that extends an already streamed partial line no longer repeats the partial prefix in the closing frame.
- Cover the repeated root-directory markdown reply case from `auto-bugfix/latest/cc-connect.log` with a dedicated regression fixture.
- Keep the earlier long-finalize splitting protection and verify the full `platform/wecom` test suite still passes after the dedup adjustment.

## v1.3.10 (2026-05-28)

Personal fork WeCom long-finalize delivery fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom WebSocket preview finalize path so terminal content over 2048 bytes is split into one closing stream frame plus ordered follow-up markdown messages instead of being forced into a single oversized frame.
- Keep the existing preview `stream_id` when finalizing long replies so the in-place closing frame still lands on the original preview message.
- Add regression coverage for the online long-finalize case and for prefix/tool aggregation edge cases derived from the captured logs.

## v1.3.9 (2026-05-27)

Personal fork WeCom stream regression hardening for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom preview completion so the final answer closes the existing stream message in place instead of creating an additional near-duplicate message.
- Fix WeCom stream aggregation so partial text from the last acked frame is only reused when the next payload is truly incremental, avoiding repeated prefixes in long updates.
- Add log-derived regression fixtures under `platform/wecom/testdata/stream_regressions.json` so the exact online payload patterns from `a.log` remain covered by stable tests.

## v1.3.8 (2026-05-27)

Personal fork WeCom stream visibility and audit fixes for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix WeCom `stream` mode with `tool_messages = false` so hidden tool events no longer leak into the final visible stream payload.
- Keep WeCom long-message splitting on the final answer path when tool updates are hidden, avoiding the single-preview-message fallback.
- Continue recording inbound WeCom access attempts and unauthorized users for allow-list troubleshooting.

## v1.3.7 (2026-05-27)

Personal fork WeCom delivery and audit fixes for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Add WeCom access audit logging so inbound users and prompt send time are recorded to local JSONL files, including `allow_from` rejections.
- Fix WeCom WebSocket final reply delivery for long messages by splitting the terminal response into ordered follow-up messages instead of truncating the tail.
- Align WeCom message splitting with the documented 2048-byte limit and keep preview updates within that bound.

## v1.3.4 (2026-05-25)

Personal fork release for `@qinghuangniao/cc-connect-qhn`.

### Notes
- This is an unofficial personal fork intended for personal practice, experimentation, and self-use.
- Fork base: upstream `cc-connect` release `v1.3.3-beta.2`.
- Local fork import reference in this repository: commit `c099ce699e44d74a9f2018244375a4ff410cd7eb`.

## v1.3.6 (2026-05-27)

Personal fork WeCom stream/tool display fixes for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix ACP tool result mapping so completed tool updates prefer `rawOutput` over streamed assembly fragments.
- Fix WeCom tool-hold stream aggregation so stale partial text does not collapse the final payload into a lone backtick.
- Fix WeCom final rendering to insert a blank line between tool result blocks and the following natural-language answer, avoiding markdown/render breakage in WeCom.

## v1.3.5 (2026-05-26)

Personal fork packaging fix for `@qinghuangniao/cc-connect-qhn`.

### Notes
- Fix npm installer release asset lookup so global install downloads from the fork release repository.
- Intended to resolve `npm install -g @qinghuangniao/cc-connect-qhn` failing with GitHub release asset `404`.

## v1.3.3-beta.2 (2026-05-09)

Beta release with Slack Assistant API, DingTalk improvements, MAX platform webhook mode, and numerous platform fixes. No breaking changes.

### New Features
- **Slack Assistant API**: support Slack Assistant API (Agent toggle) with natural on/off switching (#844)
- **DingTalk richText**: support richText message type for DingTalk platform (#828)
- **DingTalk image handling**: add DingTalk image message support (#828)
- **MAX webhook delivery mode**: add webhook delivery mode for MAX messenger platform with deployment docs (#818)
- **Claude Code env vars**: support project-level environment variables via `env` config section (#812)
- **display_mode enum**: add `display_mode` enum to replace boolean `quiet` config, with quiet/compact/normal/full options (#655)
- **Core reset_on_idle_mins default**: default to 30 minutes to prevent context drift (#494)
- **Claude Code custom system prompt**: add support for custom system prompt configuration via `system_prompt` option (#534)

### Fixed
- **Bridge security**: require token when Bridge is enabled to prevent unauthorized access (#408)
- **Feishu recalled messages**: handle recalled messages gracefully (#841)
- **Feishu media download failure**: notify user when media download fails instead of silent drop (#815)
- **WeChat video messages**: send video files as proper video messages in WeChat (#813)
- **WeChat incomplete delivery**: notify user on incomplete message delivery and enhance retry logging (#771)
- **Telegram private topics**: preserve private topic session keys (#804)
- **Kimi session UUID**: capture session UUID from stderr instead of stdout (#766)
- **Codex app_server config**: app_server backend should honor model/effort/provider config + add stdio sentinel (#837)
- **Codex progress rendering**: render progress in rich Card 2.0 format (#838)
- **Core ellipsis events**: suppress ellipsis-only events and handle context indicator in footer
- **Core Markdown table**: render inline formatting inside GFM table cells (#675)
- **Feishu user id resolution**: guard user id resolution against edge cases
- **Feishu thread topics**: skip quote injection in thread-isolated topics (#767)
- **Config display mode**: honor project display mode setting
- **Daemon restart**: add --force flag to daemon restart command (#736)
- **AskUserQuestion**: use question text as answers key for proper answer routing (#822)

## v1.3.3-beta.1 (2026-04-25)

Beta release with new agents, new features, and broad platform fixes. No breaking changes.

### New Features
- **Devin agent**: add Devin CLI as a first-class agent with full `/list`, `/mode`, and session management (#672)
- **`/ps` command** (replaces `/btw`): send a message to a busy session mid-turn; `/btw` kept as alias for backward compatibility (#620)
- **`!` shell shortcut**: use `!ls -la` as shorthand for `/shell ls -la`, with optional `--timeout` parameter (#658)
- **NO_REPLY suppression**: agents can return `NO_REPLY` to silently skip platform delivery, useful for cron/analysis tasks (#682)
- **Feishu shared WebSocket**: multiple projects sharing the same `app_id` now share one WebSocket connection with per-project `allow_chat` / `group_only` filtering (#613)
- **Message queue depth configurable**: new `[queue] max_depth` config option (default 5) (#690)
- **Claude Code opus[1m]**: add 1M-context Opus model option with shorthand descriptions (#660)
- **QQ Bot file send/receive**: full file attachment support with robustness checks (#685)
- **Bridge ImageSender/FileSender**: `cc-connect send --image/--file` now works through bridge protocol (#712)
- **Provider presets**: add NekoCode, VisionCoder, and AIHubMix to provider presets; add Trae CLI ACP and COCO ACP config examples (#739)

### Fixed
- **OpenCode image handling**: inbound images from WeChat/WeCom are now correctly passed to OpenCode CLI via `--file` flags (#717)
- **Slack Markdown**: convert standard Markdown to Slack mrkdwn format (bold, italic, strike, links, headings) (#680)
- **QQ Bot reconnect**: cancel stale goroutines on WebSocket reconnect to prevent race conditions (#678)
- **Gemini multiline prompt**: pass prompt via stdin to preserve newlines (#695)
- **Telegram HTML fallback**: upgrade silent HTML parse failures to Warn-level logs (#674)
- **Telegram /skills**: show Telegram-safe skill command format (#571)
- **Feishu webhook mode**: skip bot open_id fetch in webhook mode for private deployments (#696)
- **Reply footer**: suppress footer when only workdir is known (#701)
- **Web UI add-platform**: fix "project not found" error when adding a new platform to an uncreated project

### Contributors
Thanks to all contributors who made this release possible:
- @YoungShook — Devin agent integration, Telegram HTML fallback
- @Cigarrr — /ps command, NO_REPLY feature
- @vinnyxiong — Feishu shared WebSocket and allow_chat
- @happyTonakai — Shell `!` prefix and `--timeout`
- @AaronZ345 — Claude Code opus[1m] model
- @ferocknew — QQ Bot file support
- @soaringk — OpenCode image fix
- @Zx55 — Telegram /skills fix
- @zhaomoran — Feishu webhook mode fix
- @LyInfi — Reply footer suppression
- @meloalright — Trae/COCO ACP config examples

## v1.3.2 (2026-04-21)

Hotfix release: session filtering is now configurable and defaults to showing all sessions.

### Fixed
- **`/list` shows all sessions by default**: the session filter introduced in v1.3.0 (which hid sessions not created by cc-connect) was accidentally merged and caused confusion. The filter is now **off by default** — `/list`, `/switch`, and `/delete` show all agent sessions regardless of origin.

### Added
- **`filter_external_sessions` config option**: users who *do* want to hide externally-created sessions can set `filter_external_sessions = true` in `[[projects]]` to restore the old filtering behavior.
- **Comprehensive integration tests**: real-agent E2E tests for both Codex and Claude Code covering the full `/list` → `/new` → conversation → `/list` lifecycle with provider-based authentication (no env-var API keys required). Plus 9 adapter-level filter tests using real Codex/Claude Code session file fixtures.

## v1.3.1 (2026-04-20)

Patch release with critical bug fixes for session management, config preservation, and Weibo media support.

### Fixed
- **Session visibility (`/list`)**: historical Codex sessions disappeared after upgrade due to `AgentSessionID` being cleared on `/new` or provider switch without preservation. Added `PastAgentSessionIDs` tracking with legacy data migration so existing sessions remain visible.
- **Session naming (`/new xxx`)**: custom session names from `/new` were not mapped to the agent session ID for agents where the ID is established asynchronously (Codex, Qoder, Kimi, etc.). Added name mapping to all `EventResult` and `EventText` handlers across interactive, relay, and drain paths.
- **Config comment preservation**: `/provider switch`, `/model`, `/lang`, display settings, and TTS changes now use surgical text-level editing instead of full TOML re-serialization, preserving all comments, unknown fields, and formatting.
- **Codex `codex_home` path**: session listing, history, and deletion now consistently use the configured `codex_home` instead of hardcoded `~/.codex`.
- **Feishu card callback hint**: log a reminder when interactive card mode is enabled but `card.action.trigger` may not be subscribed.

### Added
- **Weibo image & file support**: send and receive images and files in Weibo DMs via base64 encoding within the WebSocket `send_message` payload. Implements `ImageSender` and `FileSender` interfaces.
- **Comprehensive session tests**: 12 new `SessionManager` unit tests covering `PastAgentSessionIDs`, legacy data migration, and version-based schema detection. 9 new `Engine` integration tests covering `/list` visibility across `/new`, provider switch, and real-world legacy data scenarios, plus end-to-end session name mapping tests for all three agent ID patterns (immediate, EventText, EventResult).
- **Config preservation tests**: 8 new tests verifying comment and field preservation for `SaveActiveProvider`, `SaveAgentModel`, `SaveProviderModel`, `SaveLanguage`, `SaveDisplayConfig`, `SaveTTSMode`, multi-project config, and global provider refs.

## v1.3.0 (2026-04-19)

First stable release of the 1.3 series. 555 commits since v1.2.1 with major new features, platform improvements, and broad community contributions.

### Highlights

- **Web Admin UI** — Full management dashboard embedded in the binary via `go:embed`. Project CRUD, session monitoring, cron editor, provider management, chat interface, and i18n (en/zh/zh-TW/ja/es). Use `cc-connect web` to open directly in the browser with auto-login.
- **Lifecycle Event Hooks** — New `[[hooks]]` config to trigger shell commands or HTTP webhooks on 7 event types: `message.received`, `message.sent`, `session.started`, `session.ended`, `cron.triggered`, `permission.requested`, `error`. Async by default, fail-open, non-blocking.
- **Skill Management** — New `/skills` page in the web UI with local skill browser (per-project, per-agent) and recommended skill presets fetched from remote.
- **Global Provider Management** — Add, edit, delete providers in the web UI; import from cc-switch config; per-agent-type provider presets with featured/star badges.

### New Features
- `cc-connect web` CLI command: auto-configure web admin, open browser with token-based login
- Feishu: auto-resolve `@name` mentions to clickable at-tags (`resolve_mentions` config)
- Feishu: multi-level reply chain recognition; done-emoji reaction after streaming
- Feishu: configurable progress display styles (compact/card)
- Claude Code: support CLI wrappers via `cli_path`; `/effort` command for reasoning effort; `auto` permission mode; `disallowed_tools` config
- Codex: runtime reply footer; preserve workspace app-server options
- Kimi CLI: new agent support
- Pi: new agent support
- Discord: preserve table formatting; proxy support; `@everyone`/`@here` broadcast
- Telegram: forum topic support; markdown table monospace rendering; command menu adaptation
- WeCom: configurable `api_base_url` for private deployments; file receiving via HTTP callback
- Weixin (ilink): personal chat platform with CDN media, QR setup, image/file/audio send
- Config: support `${ENV_VAR}` placeholders in TOML values
- Core: `/workspace init` with local directory paths; `/dir` directory history; `agent-sid` command; auto-compress context on token threshold; outgoing rate limiting
- Daemon: preserve proxy env in systemd service

### Bug Fixes
- Fix Windows cross-compilation (duplicate runas stub file)
- Fix web footer double 'v' prefix in version display
- Fix web modal overlay not covering full viewport (portal rendering)
- Fix provider preset cards: action buttons pinned to card bottom
- Fix web page content overlapping footer (global layout restructure)
- Fix Gemini image handling: save to workspace, prompt-based file references
- Fix Claude Code: unblock readLoop when child subprocesses hold stdout pipe
- Fix Codex: multiline prompt on resume; force-kill process group on stop
- Fix core: race condition during session cleanup; follow symlinked skill directories; persist agent_session_id; filter `/list` to cc-connect owned sessions
- Fix Feishu: slash commands in thread/reply context; user/chat name resolution in async goroutine
- Fix Telegram: UTF-8-safe command menu descriptions
- Fix TTS: don't send empty language_type to Qwen TTS API
- Fix config: `formatTOML` no longer strips user-set zero values
- Security: mask bridge token in `/api/v1/status`; path traversal protection for static files

### Contributors

Thanks to all contributors who made this release possible:

- [@leoliang1997](https://github.com/leoliang1997) — Feishu card rendering, auto-resolve @mentions
- [@xukp20](https://github.com/xukp20) — Provider env handling, skill discovery, Codex options
- [@boyu-zhu](https://github.com/boyu-zhu) — Telegram markdown table rendering
- [@RukawaKaede](https://github.com/RukawaKaede) — Claude Code CLI wrapper support
- [@meishaoqing](https://github.com/meishaoqing) — Feishu multi-level reply chain
- [@Zx55](https://github.com/Zx55) — Telegram command menu, symlinked skill dirs
- [@leighstillard](https://github.com/leighstillard) — Claude Code `/effort` command
- [@ht290](https://github.com/ht290) — inject_sender display name
- [@Sentixxx](https://github.com/Sentixxx) — Claude Code readLoop subprocess fix
- [@bugwz](https://github.com/bugwz) — WeCom private deployment API base URL
- [@cold2600438-lgtm](https://github.com/cold2600438-lgtm) — Kimi CLI agent
- [@MeteorSkyOne](https://github.com/MeteorSkyOne) — Discord table formatting
- [@happyTonakai](https://github.com/happyTonakai) — Feishu done-emoji reaction
- [@xxb](https://github.com/xxb) — Codex reply footer, Discord session routing
- [@q107580018](https://github.com/q107580018) — Feishu delete/model card flows
- [@Cigarrr](https://github.com/Cigarrr) — Workspace binding parsing
- [@g1f9](https://github.com/g1f9) — Local directory workspace init
- [@0xsegfaulted](https://github.com/0xsegfaulted) — agent-sid command
- [@yzlu0917](https://github.com/yzlu0917) — Env var config placeholders
- [@sidney061212-ai](https://github.com/sidney061212-ai) — Agent session ID persistence
- [@zkunzhu](https://github.com/zkunzhu) — Daemon proxy env preservation
- [@Yuri0314](https://github.com/Yuri0314) — TTS language type fix

## v1.2.2-beta.5 (2026-03-31)

Beta release with embedded web admin, Discord proxy support, multimodal fixes, and major platform improvements.

### New Features
- **Embedded Web Admin**: Web frontend is now compiled into the binary via `go:embed` — no separate `npm install` needed. Use `/web setup` to configure, or build with `no_web` tag to exclude. Binary size increases ~1MB (#356)
- **Web Admin Dashboard**: Full-featured management UI with project CRUD, session management, cron job editor, global settings, chat interface with bridge WebSocket, slash commands, and i18n (en/zh/zh-TW/ja/es) (#316)
- **Discord Proxy Support**: Discord platform now supports `proxy`, `proxy_username`, `proxy_password` options for HTTP API and WebSocket Gateway connections
- **Feishu Progress Styles**: Configurable progress display styles (compact/card) to reduce message spam
- **Claude Code Auto-Permission Mode**: New `auto` permission mode for Claude Code agent (#329)
- **WeCom File Receiving**: WeCom HTTP callback now supports receiving files and forwarding them to the agent (#330)
- **Outgoing Rate Limiting**: Per-platform outgoing message rate limiting
- **Telegram Forum Topics**: Migrated to `go-telegram/bot` library with forum topic support (#321)
- **Global Settings UI**: Expose global configurations (language, quiet, display, stream preview, rate limit, log) in the web admin

### Bug Fixes
- **Gemini Image Handling**: Save attachments to workspace directory instead of `/tmp` so Gemini CLI tools can access them; use prompt-based file references instead of unsupported `--image` flag
- **Security**: Mask bridge token in `/api/v1/status` endpoint; add path traversal protection for static file serving
- **Codex**: Fix multiline prompt preservation on resume (#341); force kill session process group on stop (#340)
- **Session Recycling**: Wait for old session to close before creating new one (#352)
- **Discord**: Harden session routing and remove implicit continue bridge (#322); execute slash commands when defer fails (#300)
- **Slack**: Pass file uploads to agent (#296)
- **Telegram**: UTF-8-safe command menu descriptions (#301)
- **WeCom**: Strip @bot mentions from inbound text (#303)
- **Daemon**: macOS launchd do not respawn on clean exit (#304)
- **Core**: Route workspace model changes through session context (#339); outgoing rate limit refinements and i18n tightening
- **Config**: `formatTOML` no longer strips user-set zero values (e.g. `quiet = false`)

### Improvements
- **CI**: Add Node.js setup for web frontend build in CI pipeline; use `no_web` tag for e2e/smoke tests
- **Tests**: Expanded coverage across agents, config, and core packages
- **Selective Compilation**: Added `no_web` build tag to exclude web assets from binary

### Contributors

Special thanks to all contributors who made this release possible:

- **cg33** — Embedded web admin, Discord proxy, Gemini fix, security hardening
- **xxb** — Discord session routing fix, codex process kill, workspace reconnect (#322, #340, #315)
- **dev-null-sec** — Codex multiline prompt fix (#341)
- **xukp20** — Workspace model routing (#339)
- **zhengbuqian** — Telegram go-telegram/bot migration and forum topics (#321)
- **huangdijia** — Claude Code auto permission mode (#329)
- **buddhism5080** — Discord file sending (#307)

## v1.2.2-beta.4 (2026-03-22)

Beta release with Weixin (ilink) personal chat support, session/continue improvements, and platform fixes.

### New Features
- **Weixin Personal (ilink)**: New platform with long-poll `getUpdates` / `sendMessage`, QR `weixin setup`, CDN decrypt for inbound media and `ImageSender`/`FileSender` outbound (#257)
- **Telegram**: Voice/audio reply support (#225) and async startup recovery
- **Discord**: `@everyone` / `@here` broadcast support (#132)
- **Cron**: Optional new session per run and per-job timeout (#236)
- **Claude Code**: `disallowed_tools` configuration option (#232)
- **Auto-Compress**: Compress context when estimated tokens exceed threshold (#231)
- **Continue / Sessions**: Fork session on `--continue` to avoid context contamination (#244); replace persisted `ContinueSession` sentinel with real agent session id; reserve CLI `--continue` bridge for real user traffic
- **Core**: `/dir` directory history; `/model` switching aligned with provider flow (#246)
- **Providers**: MiniMax M2.7 high-speed model added to example configs (#217)

### Bug Fixes
- **Weixin**: Harden send path (empty body skip, response body cap, dedup keys, multi-voice segments); treat `sendMessage` JSON `ret != 0` as failure so quota/API errors surface correctly
- **Feishu**: Always reply to the original message; dispatch message handling asynchronously (#57)
- **Codex**: Mode switch and `--json` flag position fixes (#240, #239)
- **Multi-Workspace**: Workspace command prefix missing leading slash (#135)
- **Non-Claude Agents**: Ignore `ContinueSession` sentinel where inappropriate (#244 follow-up)
- **npm / Update**: Version sync after update; pre-release version comparison normalization

### Improvements
- **Tests**: Expanded coverage across `config`, `core`, agents, and platforms
- **Logging / Errors**: Additional error logging in several code paths

### Contributors

Special thanks to all contributors who made this release possible:

- **cg33** — Weixin ilink platform, setup CLI, and CDN media (#257)
- **Shawn** — Feishu async dispatch and reply-to-original fixes (#57)
- **quabug** — Discord broadcast and non-Claude ContinueSession handling (#132, #244)
- **huluma1314** — Auto-compress when token threshold exceeded (#231)
- **Leigh Stillard** — Fork session on `--continue` (#244)
- **Deeka Wong** — Telegram audio replies and core `/model` provider flow (#225, #246)
- **q107580018** — Telegram async startup recovery
- **just4zeroq** — Codex mode and JSON flag fixes (#240)
- **术士木星** — Cron session-per-run and job timeout (#236)
- **hushicai** — Claude `disallowed_tools` (#232)
- **Octopus** — MiniMax M2.7 high-speed in examples (#217)
- **alinnb** — `/dir` directory history
- **Claude** — Continue-session bridge fixes, auto-compress/cron edge cases, Weixin send hardening and API error handling, and broad test improvements

## v1.2.2-beta.3 (2026-03-19)

Beta release with major multi-user mode, improved workspace stability, and platform enhancements.

### New Features
- **Multi-User Mode**: Per-user rate limits, role-based ACL (allow_from/admin_from), and audit logging
- **ImageSender**: Unified image sending support for 6 platforms (Feishu, Telegram, Discord, Slack, DingTalk, QQ)
- **MiniMax M2.7**: Upgraded default model from M2.5 to M2.7 for improved reasoning
- **/whoami Command**: Display user ID for allow_from/admin_from configuration
- **/btw Command**: Inject messages into busy sessions without interrupting
- **/dir Command**: Dynamic runtime work directory switching
- **Cron Muting**: Mute/unmute cron jobs with platform wrapper and UI integration
- **Interrupt Support**: Send interrupt signal to agent sessions (Ctrl+C equivalent)
- **CORS Support**: Cross-origin requests enabled for Bridge API
- **Message Queuing**: Queue messages when agent is busy instead of discarding
- **QQ Bot Markdown**: Full Markdown message support for QQ Bot

### Bug Fixes
- **Workspace Session Persistence**: Sessions now persist to disk in multi-workspace mode
- **Race Conditions**: Multiple data race fixes (adminFrom, degraded field, userRolesMu)
- **Memory Leaks**: Fixed pendingAcks leak on WeCom WebSocket disconnect, goroutine leaks
- **i18n**: Complete translation coverage for error messages
- **Relay Timeout**: Return partial text after timeout instead of error
- **QQ Bot Reconnect**: Handle nil wsConn on failed reconnect

### Improvements
- **Message Queue**: Extracted message queue handling into dedicated method
- **Cron UX**: Improved human-readable cron expressions
- **Slack**: Typing indicator, file download error handling, auth diagnostics
- **Provider Config**: `models` list for per-provider model selection via alias
- **Build**: Test infrastructure with P0/P1分层测试targets

### Contributors

Special thanks to all contributors who made this release possible:

- **sean2077** - Multi-user mode, ACL, and audit logging
- **0xsegfaulted** - Multi-workspace fixes and interrupt support
- **octo-patch** - MiniMax M2.7 upgrade
- **windli2018** - Bridge CORS support
- **jenvan** - CORS fixes

## v1.2.2-beta.2 (2026-03-16)

Beta release with significant improvements to agent stability, platform onboarding, and user experience.

### New Features
- **Feishu/Lark CLI Onboarding**: New `cc-connect feishu setup` command with QR code terminal display for quick bot configuration, supporting both new bot creation and existing bot binding
- **Pi Agent**: Added support for Pi coding agent with full session management and tool handling
- **Session TUI Browser**: New `cc-connect sessions` subcommand with terminal UI for browsing session history
- **Multi-Workspace Mode**: Channel-based workspace resolution with auto-binding by convention and interactive init flow
- **Design Documentation**: Added comprehensive design plans for multi-workspace and session resilience features
- **Slack Enhancements**: Typing indicator via emoji reactions, mrkdwn formatting guidance in system prompt
- **Session Resilience**: Automatic `--continue` on first connection, resume-failure fallback, and context usage indicators
- **Management API**: HTTP REST API endpoints for external management tools with WebSocket bridge support
- **Cron Setup Command**: `/cron setup` for easy cron job configuration with memory file integration

### Bug Fixes
- **RateLimiter Goroutine Leak**: Fixed cleanup goroutine not stopped on replacement and engine shutdown
- **DrainEvents Infinite Loop**: Fixed infinite loop when channel is closed in `drainEvents`
- **InteractiveKey Consistency**: Fixed `executeCardAction` using wrong key for `interactiveStates` lookup in multi-workspace mode
- **Workspace Command Prefix**: Fixed missing leading slash in workspace command prefix check
- **Agent Session Close**: Always close events channel on session timeout to prevent goroutine leaks
- **Pi Agent Mutex**: Move thinking field read inside mutex in `StartSession` to prevent race condition
- **Session AgentID Protection**: Protect `Session.AgentSessionID` writes with mutex to prevent data races
- **Session Routing Race**: Prevent session routing race when `/new` runs during active turn
- **Discord Duplicate Messages**: Deduplicate gateway `MessageCreate` events causing duplicate responses
- **Codex JSON Lines**: Handle large stdout JSON lines without scanner buffer overflow
- **UTF-8 Safety**: Use rune-based splitting in `splitMessage` to prevent invalid UTF-8 sequences

### Improvements
- **Gemini Display**: Enhanced tool display with diff syntax highlighting and improved Telegram markdown rendering
- **Thread Safety**: Added comprehensive thread-safe accessors for Session fields
- **Test Engine**: Thread safety improvements to test engine and fixed test assertions
- **Input Validation**: Consolidated interactive state cleanup and added input validation
- **i18n**: Updated rate limit messages to mention `/btw` command for adding context during processing

### Contributors

Special thanks to all contributors who made this release possible:

- **kevinWangSheng** - Multiple critical bug fixes (RateLimiter, drainEvents, UTF-8 safety, session routing)
- **q107580018** - Feishu CLI onboarding with QR code integration
- **sean2077** - Session TUI browser and sessions management
- **quabug** - Pi agent implementation and Discord fixes
- **AtticusZeller** - Gemini tool display and Telegram markdown enhancements
- **leighstillard** - Multi-workspace design, session resilience, and Slack improvements
- **Shawn** - Thread safety fixes and test improvements
- **zhuguanqi** - Session management and data race fixes
- **Steve-Rye** - JSON lines handling improvements
- **Xihui He** - iFlow and agent enhancements
- **Mr.QiuW** - Various platform improvements

## v1.2.2-beta.1 (2026-03-12)

Beta release with major new features and security improvements.

### New Features
- **`/usage` Command**: Add a built-in quota usage command with a generic agent usage-reporting interface; Codex now supports ChatGPT OAuth usage lookup via `~/.codex/auth.json`
- **Feishu Interactive Cards**: Beautiful card-based UI for slash commands (/help, /list, /status, etc.) with tabbed navigation and in-place updates
- **Lark Platform Support**: Added support for Lark (飞书国际版) with proper domain handling
- **Codex Reasoning Effort**: New `/reasoning` command to switch reasoning effort levels (low/medium/high)
- **Codex Model Cache Fallback**: `/model` command now falls back to local `~/.codex/models_cache.json` when API is unavailable
- **Gemini Timeout Config**: New `timeout_mins` option to configure per-turn timeout for Gemini agent
- **Batch Session Deletion**: `/delete` now supports comma lists, ranges, and mixed forms for batch deletion
- **TTS Support**: Text-to-speech with Qwen and OpenAI providers
- **Admin Privilege System**: Admin-only commands for privileged operations
- **iFlow Tool Timeout**: Configurable tool timeout and reset timer on partial completion
- **Card-based Permission Prompts**: Permission requests now use interactive cards with callback support
- **Shared Session Support**: Share sessions across all platforms with `share_session_in_channel` option

### Bug Fixes
- **Security Hardening**: Socket permissions tightened (0600), token redaction in logs, warning for open `allow_from`
- **Slack @mention Support**: Fixed AppMentionEvent handling for channel @mentions
- **Update Fallback**: Self-update now falls back to .tar.gz/.zip archive when bare binary returns 404
- **Skill Symlink**: Fixed skill directory scanning to follow symbolic links
- **QQBot Error Handling**: Added error logging for json.Unmarshal and WriteJSON calls
- **Claude Code Path**: Fixed underscore handling in findProjectDir path matching

### Improvements
- **Daemon Config Flag**: Support daemon install with config file path
- **Message Tracing**: Added message tracing and threaded replies
- **Scanner Buffer**: Optimized scanner buffer sizes for large outputs

## v1.2.1 (2026-03-09)

Patch release with bug fixes and minor enhancements.

### Bug Fixes
- **Engine: Idle Timer During Permission Wait** - Stop idle timer while waiting for user permission response to prevent session termination
- **Feishu: Nil Pointer Checks** - Add nil checks for `SenderId.OpenId` and `msg.Content` to prevent panics
- **Feishu: URL Validation** - Validate URLs before creating hyperlinks to prevent rejection of non-HTTP(S) URLs
- **Cron: Error Logging** - Log `json.Unmarshal` errors instead of silently ignoring when cron file is corrupted
- **Engine: Stale Event Prevention** - Add `drainEvents` utility to clear buffered events between turns

### New Features
- **Bind Setup Command** - `/bind setup` writes relay instructions to memory file for better bot-to-bot relay configuration

## v1.2.0 (2026-03-08)

This is the first stable release of cc-connect 1.2.0, consolidating all beta changes and adding new features.

### New Features (since beta.7)
- **Official QQ Bot Platform**: Native integration with Tencent's official QQ Bot Platform via WebSocket, supporting text, image, and document messages
- **iFlow CLI Agent**: Full support for iFlow CLI agent with interactive tool-call handling and mode switching
- **Shell Command Execution**: Custom commands can execute shell commands directly with `exec` field in config
- **Telegram Bot Menu**: Auto-register bot command menu on startup for better discoverability
- **DingTalk Reply Preprocessing**: Improved markdown content preprocessing for reply messages
- **Multi-Bot Relay Persistence**: Relay bindings now persist across restarts with improved binding messages

### Improvements
- **Quiet Mode**: `/quiet` now supports both per-session and global scope modes
- **Compression Command**: Improved `/compress` command handling and code refactoring
- **i18n**: Added new message keys and improved command formatting

### All 1.2.0 Highlights (from beta releases)
- **Bot-to-Bot Relay**: Forward messages between different messaging platforms
- **Streaming Preview**: Real-time message preview on Telegram, Discord, and Feishu
- **Typing Indicators**: Visual processing feedback on supported platforms
- **Session Search**: Search sessions by name, ID prefix, or summary
- **Custom Slash Commands**: Define reusable prompt templates
- **Agent Skills Discovery**: Auto-discover and invoke user-defined skills
- **Daemon Mode**: Run as background service with systemd/launchd support
- **Rate Limiting**: Per-session sliding-window rate limiter
- **Command Aliases**: Define shortcut aliases for commands
- **Self-Update**: In-place binary updates with auto-restart
- And many more improvements and bug fixes...

## v1.2.0-beta.7 (2026-03-07)

### New Features
- **Multi-Bot Relay Binding**: `/bind` now supports binding multiple bots in a group chat; use `/bind <project>` to add, `/bind -<project>` to remove specific project
- **System-level Systemd**: Daemon mode now supports system-level systemd (`/etc/systemd/system/`) when running as root, useful for servers and containers
- **Config Example Command**: `cc-connect config-example` prints embedded config template for quick reference
- **Interactive Command Buttons**: `/lang`, `/model`, `/mode` commands now show interactive button menus for easy selection
- **Exec Commands**: Custom commands can execute shell commands directly with `exec` field in config
- **Configurable Idle Timeout**: Agent idle timeout can be configured via `idle_timeout_mins` in config

### Improvements
- **Daemon Error Messages**: Improved systemd detection and error messages for WSL2, containers, and SSH environments
- **Codex CLI Visibility**: Patched codex session source to make CLI output visible

### Bug Fixes
- **Streaming Preview**: Fixed stale preview messages when streaming degrades

## v1.2.0-beta.6 (2026-03-06)

### New Features
- **Bot-to-Bot Relay**: Forward messages between different messaging platforms via CLI (`cc-connect relay`) and internal API; enables cross-platform bot communication
- **Session Search**: Search sessions by name, ID prefix, or summary with `/search <keyword>` command
- **List Pagination**: `/list` now supports pagination with `--page` and `--page-size` flags for large session counts
- **Per-Platform Streaming Preview Control**: Configure streaming preview per platform via `streaming_preview` setting (Telegram, Discord, Feishu)
- **Silent Cron Mode**: Suppress cron job notification messages with `silent = true` in cron job config
- **Voice Qwen Mode**: Voice function now supports Qwen audio model for speech-to-text
- **Feishu Three-Tier Rendering**: Intelligent markdown rendering strategy — simple text uses plain messages, rich markdown uses Post, code blocks/tables use Card

### Improvements
- **Status Display**: Improved `/status` command output with better formatting and Feishu message rendering fixes
- **Self-Update**: Auto-restart after update; added Gitee mirror support for Chinese users
- **Windows Self-Update**: Full Windows support for in-place binary updates
- **Message Splitting**: Improved boundary checks for cleaner message chunking
- **Platform Startup**: Better error handling and logging during platform initialization
- **Session Switch i18n**: Added translation for session switch success message

### Bug Fixes
- **Idle Session Timeout**: Added timeout for unresponsive agent sessions to prevent hangs
- **Streaming Preview**: Removed `maxChars` check that caused premature preview termination
- **Message Deduplication**: Deduplicate messages by process start time to prevent duplicate processing

## v1.2.0-beta.5 (2026-03-06)

### New Features
- **Streaming Preview**: Real-time message preview that updates in-place as the agent streams output; supported on Telegram, Discord, and Feishu with configurable interval, min delta, and max length
- **Rate Limiting**: Per-session sliding-window rate limiter to prevent message flooding; configurable `max_messages` and `window_secs`
- **Typing Indicators**: Visual processing feedback — Telegram/Discord show native typing action, Feishu adds emoji reaction (auto-removed on completion)
- **Command Aliases**: Define shortcut aliases for commands (`[[aliases]]` in config.toml or `/alias add`); e.g. map "帮助" → "/help"
- **Banned Words Filter**: Block messages containing configured sensitive words (`banned_words` in config.toml)
- **Project-level Command Disabling**: Disable specific commands per project via `disabled_commands` config
- **Session Deletion**: Delete sessions with `/del` command
- **`/switch` Fuzzy Matching**: Switch sessions by name, ID prefix, or summary substring in addition to numeric index

### Improvements
- **Streaming Preview + Tool Messages UX**: In non-quiet mode, when thinking/tool messages are sent, the streaming preview freezes and the final response is delivered as a new message at the bottom of the chat (instead of silently updating an older message above the tool messages)
- **Telegram Markdown→HTML**: Full Markdown-to-HTML conversion with proper escaping, placeholder-based tag nesting, and automatic fallback to plain text on parse errors
- **Discord Code-Fence-Aware Splitting**: Message chunking now respects code block boundaries, closing and re-opening fences across splits
- **Feishu Dual Rendering**: Simple markdown uses Post messages (normal font), code blocks/tables use Card messages (native rendering); matches Claude-to-IM's approach
- **Feishu Permission Interaction**: Confirmed WebSocket mode incompatibility with card button callbacks; uses text-based `/perm` commands (consistent with Claude-to-IM)
- **Session Creation & Naming**: Improved session naming with last user message as summary
- **Graceful Shutdown**: Improved context handling and lock release during shutdown
- **Unit Tests**: Added ~50 new test cases covering markdown conversion, message splitting, session management, and engine logic

### Bug Fixes
- **Telegram HTML Crossed Tags**: Fixed `<b><i>...</b></i>` nesting issues by using placeholder-based formatting pipeline
- **Telegram HTML Attribute Escaping**: Fixed `"` in URLs breaking `<a href>` attributes (escape to `&quot;`)
- **Telegram Duplicate Messages**: Fixed duplicate sends caused by streaming preview optimization skipping final HTML update
- **Streaming Preview Cursor**: Removed trailing `▍` cursor from final messages
- **Feishu Message Recall**: Unified preview and final message types to Card, eliminating unnecessary delete-and-resend
- **Feishu Reaction Cleanup**: Register empty handler for `im.message.reaction.deleted_v1` to suppress error logs
- **`fmt.Sprintf` Warnings**: Remove non-constant format strings flagged by `go vet`

## v1.2.0-beta.2 (2026-03-01)

### New Features
- **`/upgrade` Command**: Check for available updates (including beta) and self-update the binary in-place; queries both GitHub and Gitee releases
- **`/restart` Command**: Restart cc-connect service from chat with post-restart success notification
- **`/config reload` Command**: Hot-reload configuration (display, providers, commands) without restarting
- **`/name` Command**: Set custom display names for sessions (e.g. `/name my-feature`, `/name 3 bugfix`); names persist across restarts and show in `/list`, `/switch`, `/status`
- **Default Quiet Mode**: Configure `quiet = true` globally or per-project in config.toml to suppress thinking/tool progress by default; users can still toggle with `/quiet`
- **Command Prefix Matching**: Type shortened commands like `/pro l` for `/provider list`, `/sw 2` for `/switch 2`; works for all commands and subcommands
- **Numeric Session Switching**: `/list` shows numbered sessions; `/switch 3` switches by number instead of copying long IDs
- **Group Chat Mention Filtering**: Feishu, Discord, and Telegram bots now only respond to @mentions in group chats instead of all messages
- **Claude Code Router Support**: Integration with Claude Code Router for enhanced routing capabilities
- **Third-party Provider Proxy**: Local reverse proxy rewrites incompatible `thinking` parameters for third-party LLM providers (e.g. SiliconFlow)

### Improvements
- **Session History for Claude Code**: `/history` now works after `/switch` by reading from agent JSONL files
- **List Summary**: `/list` now shows the most recent user message as summary instead of the first
- **Session Names in UI**: Custom session names display with 📌 prefix in `/list`, `/switch`, `/status`
- **API Server Shutdown**: Clean shutdown without "use of closed network connection" error
- **Agent Session Timeouts**: 8-second graceful shutdown timeout for all agent sessions with kill fallback
- **Feishu Rich Text**: Use Post (rich text) messages instead of Interactive Cards for normal font size

### Bug Fixes
- **DingTalk Startup**: Fix false startup failure when stream client returns nil error
- **Deadlock on /new and /switch**: Release lock before async agent session close to prevent hangs
- **Provider Command**: Correctly list providers when no active provider is set
- **Unknown Command Handling**: Show i18n-friendly warning and fall through to agent for native commands

### Security & Reliability
- **Race Condition Fixes**: `sync.Once` for channel close, mutex protection for concurrent fields, non-blocking event sends
- **Atomic File Writes**: Config, session, and cron files use temp+rename pattern
- **Message Deduplication**: Platform-level dedup for Feishu and DingTalk webhooks
- **HTTP Client Timeouts**: Shared 30s-timeout HTTP client for all outbound requests
- **Path Traversal Protection**: Validate command file paths
- **Sensitive Data Redaction**: Redact API keys and tokens in logs

## v1.2.0-beta.1 (2026-03-01)

### New Features
- **Custom Slash Commands**: Define reusable prompt templates as global slash commands (`[[commands]]` in config.toml or `/commands add`); supports positional parameters (`{{1}}`), rest parameters (`{{2*}}`), default values (`{{1:default}}`), and runtime add/del/list
- **Agent Skills Discovery**: Auto-discover and invoke user-defined skills from agent directories (e.g. `.claude/skills/<name>/SKILL.md`); list with `/skills`, invoke with `/<skill-name> [args]`; supports all agents (Claude Code, Cursor, Gemini, Codex, Qoder)
- **`/config` Command**: View and modify runtime configuration (e.g. `thinking_max_len`, `tool_max_len`) from chat, with persistent save to `config.toml`
- **`/doctor` Command**: Run system diagnostics covering agent authentication, platform connectivity, system resources, dependencies, and network latency; fully i18n-supported
- **Discord Slash Commands**: Register native Discord Application Commands so typing `/` shows an autocomplete menu; supports per-guild instant registration via `guild_id` config
- **Daemon Mode**: Run cc-connect as a background service (`cc-connect daemon install/start/stop/status/logs`); supports systemd (Linux) and launchd (macOS)
- **Qoder CLI Agent**: Full support for the Qoder coding agent with streaming JSON, mode switching, and model selection
- **Telegram Proxy**: Support HTTP/SOCKS5 proxy for Telegram bot API connections
- **WeChat Work Proxy Auth**: Add `proxy_username` / `proxy_password` for authenticated forward proxies
- **i18n Expansion**: Add Traditional Chinese (zh-TW), Japanese (ja), and Spanish (es) language support
- **`--stdin` Support**: Read prompt from stdin for CLI usage (`echo "hello" | cc-connect send --stdin`)

### Improvements
- **Slow Operation Monitoring**: Warn-level logs for slow platform send (>2s), agent start (>5s), agent close (>3s), agent send (>2s), and agent first event (>15s); turn completion logs now include `turn_duration`
- **`tool_max_len=0` Fix**: Remove hardcoded 200-char truncation in all agent sessions (Claude Code, Cursor, Codex, Gemini, Qoder), making the user-configurable `tool_max_len` setting authoritative
- **Cursor `/list` Improvements**: Parse binary blob structure to show accurate message counts and first user message summary

### Bug Fixes
- **Telegram proxy**: Only override `http.Transport` when proxy is actually configured
- **Discord interaction fallback**: Gracefully fallback to channel messages when interaction token expires

## v1.1.0 (2026-03-02)

### New Features
- **`/compress` Command**: Compress/compact conversation context by forwarding native commands to agents (Claude Code `/compact`, Codex `/compact`, Gemini `/compress`); keeps long sessions manageable
- **Auto-Compress**: Added optional automatic context compression when estimated token usage exceeds a configurable threshold (`[projects.auto_compress]`).
- **Telegram Inline Buttons**: Permission prompts on Telegram now use clickable inline keyboard buttons (Allow / Deny / Allow All) instead of requiring text replies
- **`/model` Command**: View and switch AI models at runtime; supports numbered quick-select and custom model names. Fetches available models from provider API in real-time (Anthropic, OpenAI, Google), with built-in fallback list
- **`/memory` Command**: View and edit agent memory files (CLAUDE.md, AGENTS.md, GEMINI.md) directly from chat; supports both project-level and global-level (`/memory global`)
- **`/status` Command**: Display system status including project, agent, platforms, uptime, language, permission mode, session info, and cron job count

### Improvements
- **Cron list display**: Multi-line card-style formatting with human-readable schedule translations and next execution time
- **Model switch resets session**: Switching model via `/model` now starts a fresh agent session instead of resuming the old one, preventing stale context from affecting the new model
- **Permission modes docs**: README now documents permission modes for all four agents (Claude Code, Codex, Cursor Agent, Gemini CLI)
- **Natural language scheduling docs**: INSTALL.md now explains how to enable cron job creation via natural language for non-Claude agents
- **README revamp**: Redesigned project header with architecture diagram, feature highlights, and multi-agent positioning

### Bug Fixes
- **Gemini `/list` summary**: Fixed session list showing raw JSON (`{"dummy": true}`) instead of actual user message summary
- **GitHub Issue Templates**: Added structured templates for bug reports, feature requests, and platform/agent support requests

## v1.1.0-beta.7 (2026-03-02)

(see v1.1.0 above — beta.7 changes are included in the stable release)

## v1.1.0-beta.6 (2026-02-28)

### New Features
- **QQ Platform** (Beta): Support QQ messaging via OneBot v11 / NapCat WebSocket
- **Cron Scheduling**: Schedule recurring tasks via `/cron` command or CLI (`cc-connect cron add`), with JSON persistence and agent-aware session injection
- **Feishu Emoji Reaction**: Auto-add emoji reaction (default: "OnIt") on incoming messages to confirm receipt; configurable via `reaction_emoji`
- **Display Truncation Config**: New `[display]` config section to control thinking/tool message truncation (`thinking_max_len`, `tool_max_len`); set to 0 to disable truncation
- **`/version` Command**: Check current cc-connect version from within chat

### Bug Fixes
- **Windows `/list` fix**: Claude Code sessions now discoverable on Windows despite drive letter colon in project key paths
- **CLAUDECODE env filter**: Prevent nested Claude Code session crash by filtering CLAUDECODE env var from subprocesses

### Docs
- Clarified global config path `~/.cc-connect-qhn/config.toml` in INSTALL.md
- Fixed markdown image syntax in Chinese README

## v1.1.0-beta.5 (2026-03-01)

### New Features
- **Gemini CLI Agent**: Full support for `gemini` CLI with streaming JSON, mode switching, and provider management
- **Cursor Agent**: Integration with Cursor Agent CLI (`agent`) with mode and provider support

## v1.1.0-beta.4 (2026-03-01)

### Bug Fixes
- Fixed npm install: check binary version on install, replace outdated binary instead of skipping
- Added auto-reinstall logic for outdated binaries in `run.js`

## v1.1.0-beta.3 (2026-03-01)

### New Features
- **Voice Messages (STT)**: Transcribe voice messages to text via OpenAI Whisper, Groq Whisper, or SiliconFlow SenseVoice; requires `ffmpeg`
- **Image Support**: Handle image messages across platforms with multimodal content forwarding to agents
- **CLI Send**: `cc-connect send` command and internal Unix socket API for programmatic message sending
- **Message Dedup**: Prevent duplicate processing of WeChat Work messages

## v1.1.0-beta.2 (2026-03-01)

### New Features
- **Provider Management**: `/provider` command for runtime API provider switching; CLI `cc-connect provider add/list`
- **Configurable Data Dir**: Session data stored in `~/.cc-connect-qhn/` by default (configurable via `data_dir`)
- **Markdown Stripping**: Plain text fallback for platforms that don't support markdown (e.g. WeChat)

## v1.1.0-beta.1 (2026-03-01)

### New Features
- **Codex Agent**: OpenAI Codex CLI integration
- **Self-Update**: `cc-connect update` and `cc-connect check-update` commands
- **I18n**: Auto-detect language, `/lang` command to switch between English and Chinese
- **Session Persistence**: Sessions saved to disk as JSON, restored on restart

## v1.0.1 (2026-02-28)

- Bug fixes and stability improvements

## v1.0.0 (2026-02-28)

- Initial release
- Claude Code agent support
- Platforms: Feishu, DingTalk, Telegram, Slack, Discord, LINE, WeChat Work
- Commands: `/new`, `/list`, `/switch`, `/history`, `/quiet`, `/mode`, `/allow`, `/stop`, `/help`
