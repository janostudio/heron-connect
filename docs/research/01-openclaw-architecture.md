# OpenClaw 架构深度分析

> 基于对 `projects/openclaw` 源码的完整调研

## 一、项目概述

OpenClaw 是一个 TypeScript/Node.js 实现的 AI 编码 Agent ↔ IM 平台桥接工具，与 Heron Connect-QHN 目标相同但架构风格迥异。

**技术栈：**
- TypeScript (ESM modules, strict 模式)
- Bun 运行时
- 插件化的 `ChannelPlugin` 架构
- 支持 23+ IM 平台

## 二、目录结构

```
openclaw/
├── src/
│   ├── core/
│   │   ├── agent/           ← Agent 适配器
│   │   ├── channel/         ← IM 平台插件（23+个）
│   │   ├── draft/           ← 流式传输核心
│   │   │   ├── draft-stream-loop.ts     ← 流式主循环
│   │   │   ├── draft-chunker.ts         ← Markdown 分块
│   │   │   └── draft-*.ts
│   │   ├── context/
│   │   │   └── inbound-event-context.ts ← 入站上下文构建
│   │   └── engine/          ← 核心调度引擎
│   └── types/
│       └── channel-plugin.ts            ← 插件契约定义
```

## 三、ChannelPlugin 契约（核心设计）

OpenClaw 的核心设计是 `ChannelPlugin` 接口，每个 IM 平台通过**声明式契约**暴露其全部能力：

```typescript
// src/types/channel-plugin.ts (关键摘录)
export interface ChannelPlugin {
  // ── 基础标识 ──
  readonly id: string           // 平台唯一ID，如 "feishu"
  readonly name: string         // 显示名称
  
  // ── 生命周期 ──
  initialize(config: ChannelConfig): Promise<void>
  start(): Promise<void>
  stop(): Promise<void>
  
  // ── 消息接收 ──
  onMessage(handler: InboundMessageHandler): void
  
  // ── 消息发送（必须实现）──
  sendMessage(ctx: MessageContext, content: string): Promise<void>
  
  // ── 可选富格式能力 ──
  readonly capabilities: ChannelCapabilities
  
  // ── 流式支持 ──
  readonly streamingMode: StreamingMode    // 'off' | 'partial' | 'block' | 'progress'
  startStreaming?(ctx: MessageContext): Promise<StreamHandle>
  updateStreaming?(handle: StreamHandle, content: string): Promise<void>
  finalizeStreaming?(handle: StreamHandle, content: string): Promise<void>
  
  // ── 原生卡片 ──
  sendCard?(ctx: MessageContext, card: NativeCard): Promise<void>
  updateCard?(handle: CardHandle, card: NativeCard): Promise<void>
  
  // ── 文件/图片 ──
  sendFile?(ctx: MessageContext, file: FileAttachment): Promise<void>
  sendImage?(ctx: MessageContext, image: ImageAttachment): Promise<void>
  
  // ── 线程/回复 ──
  createThread?(ctx: MessageContext): Promise<ThreadHandle>
  replyInThread?(handle: ThreadHandle, content: string): Promise<void>
  
  // ── 用户信息 ──
  resolveUser?(userId: string): Promise<UserInfo>
  
  // ── 消息历史 ──
  fetchHistory?(ctx: MessageContext, limit: number): Promise<HistoryEntry[]>
  
  // ── 原生渲染 ──
  renderMessage(content: string, format: MessageFormat): string
}
```

### 关键设计思想：契约优先（Contract-First）

与 Heron Connect-QHN 的运行时接口探测（`if updater, ok := p.(MessageUpdater); ok`）不同，OpenClaw 要求每个插件在初始化时**声明自己的能力集**：

```typescript
// 能力声明示例（飞书插件）
const capabilities: ChannelCapabilities = {
  supportsStreaming: true,
  streamingMode: 'block',       // 支持分块流式
  supportsNativeCards: true,    // 支持原生卡片
  supportsThreads: true,        // 支持线程
  supportsFileAttachment: true, // 支持文件附件
  supportsImageAttachment: true,
  supportsReactions: true,      // 支持表情反应
  supportsMessageEdit: true,    // 支持消息编辑
  maxMessageLength: 4000,
  markdownDialect: 'lark',      // 平台特定 Markdown 方言
}
```

引擎在消息路由时查询 `capabilities`，而不是在运行时尝试类型断言。

## 四、流式传输架构（最重要的差距）

### 4.1 四种流式模式

OpenClaw 定义了严格的 `StreamingMode` 枚举：

```typescript
type StreamingMode = 
  | 'off'       // 不支持流式，等全量完成后发送
  | 'partial'   // 支持实时更新（消息内容原地替换）
  | 'block'     // 分块发送（不能编辑，发多条）
  | 'progress'  // 进度卡片（AI Card 类型，如 DingTalk）
```

| 模式 | 行为 | 典型平台 |
|------|------|----------|
| `off` | 等 Agent 全部完成后一次性发送 | WeChat Work、LINE |
| `partial` | 实时 UpdateMessage，用户看到内容逐渐增加 | Telegram、Discord |
| `block` | 每积累一定内容就发一条新消息 | Slack（编辑有频率限制时） |
| `progress` | 专用进度卡片，展示 thinking/tool/text 层次 | DingTalk AI Card、飞书 |

### 4.2 `draft-stream-loop.ts`（核心流式循环）

```typescript
// src/core/draft/draft-stream-loop.ts（简化逻辑）
export async function runDraftStreamLoop(
  plugin: ChannelPlugin,
  ctx: MessageContext,
  agentEventStream: AsyncIterableIterator<AgentEvent>
): Promise<void> {
  
  const mode = plugin.streamingMode
  
  // 根据平台模式选择策略
  const strategy = selectStreamingStrategy(mode, plugin)
  
  for await (const event of agentEventStream) {
    switch (event.type) {
      case 'thinking':
        await strategy.onThinking(event.content)
        break
      case 'tool_use':
        await strategy.onToolUse(event.tool, event.input)
        break
      case 'tool_result':
        await strategy.onToolResult(event.tool, event.output)
        break
      case 'text':
        await strategy.onText(event.content)
        break
      case 'done':
        await strategy.onDone()
        break
      case 'error':
        await strategy.onError(event.error)
        break
    }
  }
  
  await strategy.finalize()
}
```

### 4.3 `draft-chunker.ts`（Markdown 感知分块）

这是 OpenClaw 体验明显优于 Heron Connect-QHN 的核心功能之一：

```typescript
// src/core/draft/draft-chunker.ts（关键逻辑）
export function chunkMarkdownText(
  text: string,
  options: ChunkOptions
): string[] {
  const { maxLength, respectCodeBlocks, respectTables } = options
  
  if (text.length <= maxLength) {
    return [text]
  }
  
  const chunks: string[] = []
  
  // 策略1：在段落边界分块（\n\n）
  // 策略2：如果段落太长，在列表项边界分块
  // 策略3：如果仍然太长，在句子边界分块
  // 策略4：最后才按字符数硬截断（尽量避免）
  
  // 关键：绝不在代码块中间截断
  if (respectCodeBlocks) {
    const codeBlockRanges = findCodeBlockRanges(text)
    // 确保分割点不在代码块内部
    splitPoints = splitPoints.filter(p => !isInsideCodeBlock(p, codeBlockRanges))
  }
  
  // 关键：绝不在表格中间截断
  if (respectTables) {
    const tableRanges = findTableRanges(text)
    splitPoints = splitPoints.filter(p => !isInsideTable(p, tableRanges))
  }
  
  return chunks
}
```

**对比 Heron Connect-QHN：**
- Heron Connect-QHN 的 `maxPlatformMessageLen = 4000` 直接按字符数截断
- 没有代码块感知，会把 `\`\`\`python\n...` 截在中间
- 会破坏 Markdown 表格结构

## 五、入站消息上下文管道

### 5.1 `buildChannelInboundEventContext()`

OpenClaw 为每条入站消息构建极丰富的 AI 上下文：

```typescript
// src/core/context/inbound-event-context.ts
export async function buildChannelInboundEventContext(
  plugin: ChannelPlugin,
  rawMessage: RawMessage,
  config: AgentConfig
): Promise<InboundEventContext> {
  
  // 1. 用户信息解析
  const user = await plugin.resolveUser?.(rawMessage.userId) ?? {
    id: rawMessage.userId,
    name: rawMessage.userName,
  }
  
  // 2. 历史消息（如果平台支持）
  const history = plugin.capabilities.supportsHistory
    ? await plugin.fetchHistory?.(ctx, config.historyLimit ?? 10) ?? []
    : []
  
  // 3. 平台能力描述（注入系统提示）
  const platformCapabilities = describePlatformCapabilities(plugin)
  
  // 4. 频道/会话信息
  const channelInfo = {
    id: rawMessage.channelId,
    name: await plugin.resolveChannelName?.(rawMessage.channelId),
    type: rawMessage.channelType,  // 'direct' | 'group' | 'thread'
  }
  
  // 5. 当前时间戳（防止 AI 时间感知问题）
  const timestamp = new Date().toISOString()
  
  // 6. 消息引用（如果是回复某条消息）
  const quotedMessage = rawMessage.quotedMessageId
    ? await plugin.fetchMessage?.(rawMessage.quotedMessageId)
    : null
  
  return {
    user,
    history,
    platformCapabilities,
    channelInfo,
    timestamp,
    quotedMessage,
    rawMessage,
  }
}
```

这些上下文会被注入到 AI 的系统提示中，让 AI 知道：
- 当前平台是什么（飞书/Telegram/Discord）
- 平台支持什么格式（Markdown 方言、是否支持卡片）
- 对话历史
- 用户身份信息
- 是否在线程/群组中

### 5.2 格式化指令注入

```typescript
// 根据平台能力生成格式化指令
function describePlatformCapabilities(plugin: ChannelPlugin): string {
  const caps = plugin.capabilities
  
  const lines = [
    `Platform: ${plugin.name}`,
    `Markdown dialect: ${caps.markdownDialect ?? 'standard'}`,
  ]
  
  if (caps.supportsNativeCards) {
    lines.push('Rich cards: supported — you can use structured responses')
  }
  
  if (caps.maxMessageLength) {
    lines.push(`Max message length: ${caps.maxMessageLength} characters`)
  }
  
  if (!caps.supportsMarkdown) {
    lines.push('IMPORTANT: This platform does NOT support Markdown formatting. Use plain text only.')
  }
  
  return lines.join('\n')
}
```

## 六、多账号支持

OpenClaw 支持同一平台注册**多个账号**，每个账号独立运行：

```typescript
// 配置示例
{
  "channels": [
    {
      "plugin": "telegram",
      "id": "telegram-work",      // 工作账号
      "token": "BOT_TOKEN_1",
      "allowFrom": ["@alice", "@bob"]
    },
    {
      "plugin": "telegram", 
      "id": "telegram-personal",  // 个人账号
      "token": "BOT_TOKEN_2",
      "allowFrom": ["@charlie"]
    }
  ]
}
```

内部实现通过 `channelId`（不只是 `pluginId`）区分不同账号，每个账号维护独立的会话上下文。

## 七、平台原生渲染

每个 ChannelPlugin 实现 `renderMessage()` 方法，可以将 Markdown 转换为平台原生格式：

```typescript
// 飞书插件的 renderMessage
renderMessage(content: string, format: MessageFormat): string {
  if (format === 'card') {
    // 转换为飞书 Interactive Card JSON
    return buildFeishuCardJSON(content)
  }
  if (format === 'rich_text') {
    // 转换为飞书富文本格式
    return markdownToLarkText(content)
  }
  // 默认：飞书 Lark Markdown
  return convertToLarkMarkdown(content)
}

// Telegram 插件的 renderMessage
renderMessage(content: string, format: MessageFormat): string {
  // 转换为 Telegram HTML 格式（Telegram 不支持标准 Markdown）
  return markdownToTelegramHTML(content)
}

// Discord 插件的 renderMessage
renderMessage(content: string, format: MessageFormat): string {
  // Discord 支持标准 Markdown，但有特殊语法
  return adaptMarkdownForDiscord(content)
}
```

## 八、总结：OpenClaw 的核心设计优势

| 维度 | OpenClaw 方案 | 体验影响 |
|------|---------------|----------|
| 流式模式 | 4种明确模式，统一 draft-stream-loop | 各平台流式体验一致 |
| 分块策略 | Markdown 感知，代码块/表格不截断 | 代码不被破坏 |
| 平台渲染 | 每个插件有 renderMessage，平台原生格式 | 充分利用平台能力 |
| 入站上下文 | buildChannelInboundEventContext 丰富注入 | AI 回复质量更高 |
| 多账号 | channelId 维度，同平台多账号 | 更灵活的部署 |
| 能力声明 | capabilities 对象，契约优先 | 引擎逻辑更简洁 |
