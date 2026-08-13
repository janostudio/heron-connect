# Heron Connect-QHN 架构调研总览

> 创建时间：2026-05-28  
> 背景：与 OpenClaw 的横向对比，为大重构提供依据

## 一、调研目的

Heron Connect-QHN 是一个将 AI 编码 Agent（Claude Code、Codex、Gemini CLI 等）与 IM 平台（飞书、Telegram、Discord、Slack、DingTalk、微信等）连接的桥接工具。

经过与 OpenClaw（同类 TypeScript 实现）的对比，发现 Heron Connect-QHN **在用户体验上明显落后**，需要做大规模重构。本调研文档记录了现状分析、差距根因和重构方案。

## 二、调研范围

| 文档 | 内容 |
|------|------|
| [01-openclaw-architecture.md](./01-openclaw-architecture.md) | OpenClaw 架构深度分析 |
| [02-heron-connect-current-state.md](./02-heron-connect-current-state.md) | Heron Connect-QHN 现状分析 |
| [03-comparison.md](./03-comparison.md) | 架构与实现横向对比 |
| [04-ux-gap-analysis.md](./04-ux-gap-analysis.md) | UX 差距根因分析（按优先级排序） |
| [05-refactoring-plan.md](./05-refactoring-plan.md) | 大重构路线图 |

## 三、关键发现（Executive Summary）

### 3.1 Heron Connect-QHN 的核心问题

**问题 1：Engine 大单体（God Object）**
- `core/engine.go` 共 **13,820 行**，包含消息路由、命令处理、卡片渲染、工作区管理、cron 调度等所有逻辑
- 函数超过 200 个（`func (e *Engine)` 搜索结果）
- 严重违反单一职责原则，测试和迭代极其困难

**问题 2：流式体验碎片化**
- `DisplayCfg.Mode` 有四种模式（full/compact/quiet/stream），但 stream 模式与 `streamPreview` 机制不统一
- `streamPreview` 实现在 `core/streaming.go`，逻辑复杂但与平台集成不一致
- 各平台对流式消息的支持能力差异很大，降级逻辑散落在 engine.go 各处

**问题 3：Markdown 处理粗放**
- `core/markdown.go` 只有 54 行，仅做简单的正则替换（`StripMarkdown`）
- 缺乏 Markdown 感知的文本分块（OpenClaw 有 `chunkMarkdownText`）
- 长消息按字符数截断，会破坏代码块、表格等结构化内容

**问题 4：平台原生富格式利用不足**
- 各平台（飞书、DingTalk 等）支持丰富的原生卡片格式
- 当前通过可选接口（`CardSender`, `RichCardSupporter`）暴露，但集成不完整
- OpenClaw 在飞书/DingTalk 等平台上原生利用卡片 API，体验更好

**问题 5：入站消息上下文单薄**
- `Message` 结构体传递的上下文有限
- OpenClaw 的 `buildChannelInboundEventContext()` 构建了极丰富的 AI 上下文（频道历史、用户角色、平台能力等）
- AI 获得的上下文质量直接影响回复质量

### 3.2 优先级排序

| 优先级 | 问题 | 影响范围 | 技术难度 |
|--------|------|----------|----------|
| P0 | engine.go 拆分 | 可维护性、所有新功能 | 高 |
| P1 | 流式体验统一 | 所有平台用户 | 中 |
| P1 | Markdown 感知分块 | 所有文本输出 | 低 |
| P2 | 平台原生富格式 | 飞书/DingTalk 用户 | 中 |
| P2 | 入站上下文增强 | AI 回复质量 | 中 |
| P3 | 多账号支持 | 高级用户 | 高 |

### 3.3 重构策略

不是推倒重建，而是**渐进式拆分**：

1. **Phase 1**：将 engine.go 的命令处理部分拆分到独立模块
2. **Phase 2**：统一流式传输层（Draft Stream 抽象）
3. **Phase 3**：引入 Markdown 感知分块器
4. **Phase 4**：增强平台原生能力集成
5. **Phase 5**：改进入站上下文管道

## 四、代码规模参考

```
heron-connect/
├── core/                    59,847 行
│   ├── engine.go           13,820 行  ← 最大问题
│   ├── engine_test.go      12,688 行
│   ├── i18n.go              3,948 行
│   ├── management.go        1,950 行
│   ├── streaming.go           526 行  ← 已有基础
│   └── interfaces.go          571 行  ← 接口设计良好
├── platform/                31,069 行
│   ├── feishu/feishu.go     4,234 行
│   ├── telegram/            1,568 行
│   ├── qqbot/               1,564 行
│   └── ...
└── agent/                   26,006 行
    ├── claudecode/          ~2,700 行
    └── ...
```

总计约 **116,000+ 行** Go 代码（不含测试）。

## 五、对比结论

OpenClaw 的设计在以下方面明显优于 Heron Connect-QHN：

1. **流式分层清晰**：4 种明确的 streaming mode（off/partial/block/progress），统一的 `draft-stream-loop.ts` 驱动
2. **Markdown 感知**：`chunkMarkdownText` 在代码块/表格边界分块，不破坏结构
3. **平台原生格式**：各 ChannelPlugin 独立实现 `renderMessage()`，可充分利用平台特性
4. **丰富上下文**：`buildChannelInboundEventContext()` 为每条消息注入完整的频道/用户/历史上下文
5. **插件完全解耦**：每个 ChannelPlugin 自声明所有能力，引擎无需 hardcode 平台知识

Heron Connect-QHN 的优势在于：
- Go 语言性能更好，资源占用更低
- 接口隔离设计（Interface Segregation）更灵活
- 已有成熟的 cron、relay、workspace 等高级功能
- 更完整的命令系统（/memory、/model、/provider 等）

重构目标是在保持 Go 语言优势和现有功能的前提下，学习 OpenClaw 的 UX 设计模式。
