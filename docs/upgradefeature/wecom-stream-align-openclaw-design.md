# 企微流式展示效果对齐 openclaw — 技术方案与改造效果

## 0. 背景与目标

### 0.1 现状感知

用户主观感受:**openclaw 的流式展示效果明显优于 qhn**。这不是"图标好不好看"的表面问题,而是 openclaw 把"流式展示"做成了一个**只读投影**,qhn 把它做成了一个**可变状态**,每层都在改,导致越改越乱。

### 0.2 改造目标

把 qhn 企微流式展示从"多层可变状态"改造成"单状态机的只读投影",对齐 openclaw 的 `progress drafts` 设计哲学:

1. **单一事实源**:一个 assembler 对象独占展示状态,engine 和发送层都不再各自拼装
2. **三区分离**:`visibleText`(正文)/ `progressLines`(工具进度)/ `heldTool`(待并入工具块)独立存储,互不污染
3. **工具信息是 UI 侧信道**:工具进度行只渲染给用户看,绝不进入 model context,finalize 时直接清空
4. **发送层只串行去重**:不再从 `lastAcked` 反推前缀,不做带猜测性质的二次补全

### 0.3 改造范围

- 平台:`platform/wecom/`(WebSocket 长连接模式)
- 引擎:`core/engine_turn.go`、`core/engine_session_cmds.go`、`core/streaming.go`、`core/progress_compact.go`
- 事件源:`agent/acp/mapping.go`、`agent/claudecode/session.go`、`agent/codex/session.go` 等
- 不影响:其他平台、`full`/`compact`/`quiet` 等 display mode、Webhook 模式

### 0.4 全局审查:文档完整性自检

本文档经全局代码审查后,已覆盖以下易遗漏路径:

| 路径 | 是否已处理 | 说明 |
|------|----------|------|
| `EventToolResult` 进 `textParts` | ✅ 第 4.6 节 | 工具结果也走 `tool_hold`,不能只改 `EventToolUse` |
| `EventThinking`/`mapPlan` 走 stream preview | ✅ 第 4.7 节 | thinking 和 plan 会和正文混合 |
| `streamPreview.transform`(`renderOutgoingContentForWorkspace`) | ✅ 第 4.8 节 | 正文 transform 链必须保留 |
| `streamPreview.freeze()`/`detachPreview()` 路径 | ✅ 第 4.9 节 | 权限提示/工具中断时冻结 |
| `silentHold`(NO_REPLY 前缀 hold) | ✅ 第 4.10 节 | 静默回复检测 |
| `segmentStart` 分段机制 | ✅ 第 4.11 节 | `textParts` 分段发送 |
| 三套正文缓冲(`textParts`/`partialText`/`sp.fullText`) | ✅ 第 4.12 节 | 不能只改一个 |
| `compactProgressWriter` 并行路径 | ✅ 第 4.13 节 | wecom 不走此路径,但要确认 |
| 多 turn/queued 消息重建 `sp` | ✅ 第 4.14 节 | steer/followup 模式 |
| 不同 agent 的事件粒度差异 | ✅ 第 4.15 节 | ACP vs claudecode vs codex |
| replyFooter 注入 `fullResponse` | ✅ 第 4.16 节 | footer 在 finalize 前追加 |
| `waitOutgoing` 限流器 | ✅ 第 4.17 节 | 发送层限流 |
| Webhook 模式 finalize 路径 | ✅ 第 4.18 节 | 不受影响但要说明 |

---

## 1. 问题诊断:为什么 qhn 效果不如 openclaw

### 1.1 根因:展示文本是"可变状态"而非"只读投影"

**openclaw 的做法**(`docs/concepts/progress-drafts.md:198-200`):
> OpenClaw renders only the `progress.text` in the channel progress UI. The normal tool result still arrives later as `content` and `details`, and is **the only part returned to the model**.

工具进度行是 UI 侧信道,从单一状态机 `render()` 出来,用完即弃。最终答案和进度行**物理隔离**在不同字段。

**qhn 的做法**(`core/engine_turn.go:672-694`、`core/i18n.go:594-596`):
```go
// 工具消息被拼进了 textParts(正文缓冲)
toolMsg := fmt.Sprintf("🔧 **工具 #%d: %s**\n---\n%s", toolCount, name, input)
textParts = append(textParts, "\n\n")
textParts = append(textParts, toolMsg)  // 工具块进了正文!
```

工具块用 `append` 拼进了 `textParts`,和正文混在一个缓冲里。最终展示文本是"正文 + 工具块"的混合体,**无法干净地分离**。

### 1.2 症状清单(用户感知到的现象 → 根因映射)

| 用户感知 | 根因 | 涉及代码 |
|---------|------|---------|
| 同一条消息前缀重复两次 | `lastAcked` 补前缀策略误判 + engine 重复拼装 | `websocket_stream_queue.go:99-128`、`engine_turn.go:1088` |
| 工具块和正文交错 | 工具块进了 `textParts`,和正文同缓冲 | `engine_turn.go:678`、`engine_turn.go:690` |
| 工具中间态(半成品命令)刷屏 | `tool_call_update.in_progress` 已抑制,但 `tool_hold` 判断只看前缀,语义脆弱 | `websocket_stream_assembler.go:64-75` |
| finalize 后工具块残留 | `ingest()` 把工具块当正文 segment 拼进 `visibleText`,finalize 没清 | `websocket_stream_assembler.go:35-44` |
| 长消息分片后内容混乱 | 分片发生在 aggregator 之前,第一片又被拿去和 `lastAcked` 混合 | `websocket_stream_queue.go:117`、`websocket_stream_reply.go:75-96` |
| preview 截断提示和最终内容边界不清 | `wecomPreviewPayload` 截断后,finalize 内容不再以 preview 为前缀,触发补前缀 | `websocket_stream_reply.go:98-116` |

### 1.3 三层都在拼内容(架构性缺陷)

当前一次 turn 的内容拼接链路:

```
Layer 1: engine/core
  - textParts += 文本
  - textParts += 工具消息      ← 第 1 次拼装
  - mergeStreamDisplayContent() ← 第 2 次拼装(streamContent + finalResponse)

Layer 2: platform/wecom/wsStreamAssembler
  - ingest(content) 拼工具块和正文  ← 第 3 次拼装

Layer 3: platform/wecom/runStreamQueue
  - lastAcked 补前缀            ← 第 4 次拼装(带猜测)
```

**openclaw 只有 Layer 1 的"产事件"和 Layer 2 的"render",没有 Layer 3 的猜测补全。**

---

## 2. 对齐模块清单:哪些模块需要对齐,因为什么

### 2.1 总览

| # | 模块 | 当前问题 | 对齐动作 | 对齐 openclaw 的什么 |
|---|------|---------|---------|---------------------|
| 1 | `wsStreamAssembler` 状态机 | 字段只有 `visibleText`+`heldTool`,工具块进正文 | 拆三区:`visibleText`/`progressLines`/`heldTool` | `progress drafts` 的 label + progressLines + 最终答案三部分独立(`progress-drafts.md:52-57`) |
| 2 | `engine_turn.go` `EventToolUse` 处理 | 工具块 `append` 进 `textParts` | engine 只产标准化事件,不为 wecom 拼展示文本 | openclaw 的 engine 只产 `append_text`/`hold_tool`/`finish` 事件 |
| 3 | `engine_turn.go` `EventToolResult` 处理 | 工具结果也 `append` 进 `textParts`(L789-798) | 同 #2,`onToolComplete` 转给 progressLines | 同上 |
| 4 | `mergeStreamDisplayContent()` | stream 内容和 finalResponse 二次合并 | 删除此函数,finalize 直接用 `finalResponse` | openclaw 没有"二次合并"层 |
| 5 | `runStreamQueue` 的 `lastAcked` 补前缀 | 猜测性补全导致前缀重复 | 删掉补前缀逻辑,只保留串行+去重 | openclaw 发送层职责单一(`streaming.md`) |
| 6 | `ingest()` 把工具块当正文 | `heldTool` 并入 `visibleText` | `heldTool` 转成 `progressLines` 行,不进 `visibleText` | openclaw 工具进度是 UI 侧信道,不进最终答案 |
| 7 | 工具消息格式 | `🔧 **工具 #1: Bash**\n---\n%s`(markdown 加粗 + 多行) | 紧凑单行 `🛠️ Bash: <explain>`,不进正文 | openclaw 的 `🛠️ Bash: run tests` 单行格式 |
| 8 | `streamToolHoldNeedsAnswerSeparator` | engine 层的 `\n\n` 分隔符 hack | 删除,由 assembler 的 render 统一分隔 | openclaw 没有"分隔符 hack" |
| 9 | 长消息分片时机 | 分片前还在 aggregator 内,分片后又二次聚合 | 分片发生在 assembler 产出最终文本**之后**,不再进 aggregator | openclaw 的 `textChunkLimit` 钳制 chunker,不二次聚合 |
| 10 | `EventThinking`/`mapPlan` | thinking/plan 进 `sp.fullText`,和正文混合 | stream 模式下 thinking 默认不进 preview | openclaw 的 reasoning 默认隐藏 |
| 11 | `streamPreview.transform` | `renderOutgoingContentForWorkspace` 作用于 preview 文本 | 保留 transform,在发送出口应用,assembler 存 raw | openclaw 也有 transform 链 |
| 12 | `sp.freeze()`/`detachPreview()` | 权限/工具中断时冻结 preview | assembler 新增 `snapshot()`,freeze 时用快照 | openclaw 的安全回退 |
| 13 | `silentHold`(NO_REPLY 前缀 hold) | 流式时 hold 可能是 NO_REPLY 的文本 | silentHold 逻辑保留 engine 层,不进 assembler | openclaw 的 silent reply 处理 |
| 14 | `segmentStart` 分段机制 | `textParts` 分段发送 | stream 模式下废弃分段,用 freeze+detach 替代 | openclaw 的 progress draft 不分段 |
| 15 | 三套正文缓冲 | `textParts`/`partialText`/`sp.fullText` 并存 | assembler 只取代 `sp.fullText` 的发送源角色 | openclaw 状态单一 |
| 16 | `replyFooter` 注入 | footer 在 `fullResponse` 里,被带进 stream | 符合预期,footer 是 finalize 时才进入 | openclaw 的 footer 也在最终消息 |
| 17 | 多 turn / queued 重建 `sp` | steer/followup 模式重建 sp 和 assembler | 每 turn 独立实例,前 turn finalize 清空 | openclaw 每 turn 独立 draft |
| 18 | 不同 agent 事件粒度 | ACP/claudecode/codex 事件粒度不同 | assembler 对所有 agent 一视同仁,差异在 mapping 层 | openclaw 的统一事件合约 |

### 2.2 为什么这些模块需要改造(因果链)

```
根因:展示文本是可变状态,每层都在改
  ├─ engine 把工具块 append 进 textParts
  │    → 工具信息和正文物理混合(模块 #2)
  │    → 工具结果也进 textParts(模块 #3)
  │    → 需要 mergeStreamDisplayContent 二次分离(模块 #4)
  │    → 需要 streamToolHoldNeedsAnswerSeparator hack 分隔(模块 #8)
  │    → thinking/plan 也进 sp.fullText(模块 #10)
  │
  ├─ wsStreamAssembler 把工具块当正文 segment
  │    → heldTool 并入 visibleText(模块 #6)
  │    → finalize 后工具块残留(模块 #1)
  │
  ├─ runStreamQueue 用 lastAcked 反推前缀
  │    → 截断/分片时误判,前缀重复(模块 #5)
  │    → 长消息分片后又二次聚合(模块 #9)
  │
  ├─ 工具消息用 markdown 多行格式
  │    → 个人微信不渲染(模块 #7)
  │    → 工具块和正文边界不清(模块 #7)
  │
  ├─ sp.transform 作用于 preview 文本
  │    → assembler 不能绕过 transform(模块 #11)
  │
  ├─ freeze/detach 时需要快照
  │    → assembler 需 snapshot() 支持(模块 #12)
  │
  ├─ silentHold 判断 NO_REPLY 前缀
  │    → appendText 需延迟调用(模块 #13)
  │
  ├─ segmentStart 分段发送
  │    → stream 模式下不适用(模块 #14)
  │
  ├─ 三套正文缓冲并存
  │    → 状态不同步风险(模块 #15)
  │
  └─ 多 turn / 不同 agent
       → 每 turn 独立实例(模块 #17, #18)
```

**改造核心原则:让展示文本成为状态的投影,而不是状态本身。**

---

## 3. 技术方案:三区分离状态机

### 3.1 新状态机定义

替换当前 `platform/wecom/websocket_stream_assembler.go` 的 `wsStreamAssembler`。

```go
// wecomStreamAssembler 是企微流式展示的单一事实源。
// 三区物理隔离:visibleText(正文) / progressLines(工具进度) / heldTool(待并入工具块)。
// 工具进度行绝不进入 visibleText,finalize 时直接清空。
type wecomStreamAssembler struct {
    mu sync.Mutex

    // 三区:独立存储,互不污染
    visibleText   string   // 只有 model 产出的正文
    progressLines []string // 工具进度行(最近 N 行)
    heldTool      string   // 待显示的最新工具块(只保留最后一个)

    // 生命周期
    finished bool

    // 渲染配置
    maxProgressLines int    // progressLines 最多保留几行,默认 4
    maxLineChars      int    // 单行超此字符数则中间省略,默认 120
    detailMode        string // "explain" | "raw",默认 "explain"
    separator         string // progress 区和正文区的分隔符,默认 "\n\n---\n\n"
}

// 标准化事件输入(engine 只产这四种事件)

// appendText:正文到来。只追加 visibleText,不动 progressLines。
func (a *wecomStreamAssembler) appendText(text string) string {
    a.mu.Lock(); defer a.mu.Unlock()
    a.visibleText += text
    return a.render()
}

// onToolStart:工具启动/更新。只更新 progressLines,不动 visibleText。
// 同一工具多次更新只保留最新一行(覆盖 heldTool)。
func (a *wecomStreamAssembler) onToolStart(toolName, explainArg, rawArg string) string {
    a.mu.Lock(); defer a.mu.Unlock()
    line := a.formatToolLine(toolName, explainArg, rawArg)
    a.heldTool = line
    a.progressLines = appendBounded(a.progressLines, line, a.maxProgressLines)
    return a.render()
}

// onToolComplete:工具完成。可选:把工具结果摘要作为独立 progressLine 追加。
// 不进入 visibleText。
func (a *wecomStreamAssembler) onToolComplete(toolName, resultSummary string) string {
    a.mu.Lock(); defer a.mu.Unlock()
    if resultSummary == "" {
        return a.render()
    }
    line := "✅ " + toolName + ": " + truncateMiddle(resultSummary, a.maxLineChars)
    a.progressLines = appendBounded(a.progressLines, line, a.maxProgressLines)
    a.heldTool = "" // 已完成的工具清出 held
    return a.render()
}

// finish:turn 结束。finalText 非空则作为最终正文,清空 progressLines。
func (a *wecomStreamAssembler) finish(finalText string) string {
    a.mu.Lock(); defer a.mu.Unlock()
    if finalText != "" {
        a.visibleText = finalText
    }
    a.progressLines = nil  // 关键:收尾时清掉工具进度
    a.heldTool = ""
    a.finished = true
    return a.render()
}

// discard:turn 取消。清空所有状态。
func (a *wecomStreamAssembler) discard() {
    a.mu.Lock(); defer a.mu.Unlock()
    a.visibleText = ""
    a.progressLines = nil
    a.heldTool = ""
    a.finished = false
}

// reset:turn 间清理。等价于 discard + 配置保留。
func (a *wecomStreamAssembler) reset() {
    a.discard()
}

// render:只读投影。拼 progressLines + visibleText,绝不修改状态。
func (a *wecomStreamAssembler) render() string {
    var parts []string
    // 1. 进度区(finished 时已清空,不会出现)
    if len(a.progressLines) > 0 {
        parts = append(parts, strings.Join(a.progressLines, "\n"))
    }
    // 2. 正文区
    if a.visibleText != "" {
        parts = append(parts, a.visibleText)
    }
    if len(parts) == 0 {
        return ""
    }
    if len(parts) == 1 {
        return parts[0]
    }
    return strings.Join(parts, a.separator)
}

// formatToolLine:格式化工具进度行。
// explain: 🛠️ Bash: 检查 JS 语法
// raw:     🛠️ Bash: 检查 JS 语法, node --check /tmp/app.js
func (a *wecomStreamAssembler) formatToolLine(toolName, explainArg, rawArg string) string {
    arg := explainArg
    if a.detailMode == "raw" && rawArg != "" {
        arg = explainArg + ", " + rawArg
    }
    if arg == "" {
        arg = toolName
    }
    line := "🛠️ " + toolName + ": " + arg
    return truncateMiddle(line, a.maxLineChars)
}

// appendBounded:追加到末尾,超限时丢弃最旧行(FIFO)。
func appendBounded(lines []string, newLine string, max int) []string {
    lines = append(lines, newLine)
    if max > 0 && len(lines) > max {
        lines = lines[len(lines)-max:]
    }
    return lines
}

// truncateMiddle:超长时中间省略,保留前后缀。
// 例:truncateMiddle("abcdefghij", 8) == "abcd…ij"
func truncateMiddle(s string, maxChars int) string {
    if maxChars <= 0 || len([]rune(s)) <= maxChars {
        return s
    }
    runes := []rune(s)
    if maxChars < 5 {
        return string(runes[:maxChars])
    }
    headLen := (maxChars - 1) / 2
    tailLen := maxChars - 1 - headLen
    return string(runes[:headLen]) + "…" + string(runes[len(runes)-tailLen:])
}
```

### 3.2 关键不变量(必须由测试覆盖)

| 不变量 | 含义 |
|-------|------|
| **I1** | `visibleText` 只包含 `appendText` 和 `finish(finalText)` 写入的内容,绝不包含工具消息文本 |
| **I2** | `progressLines` 只由 `onToolStart`/`onToolComplete` 写入,`finish` 后立即清空 |
| **I3** | `heldTool` 只保留最后一个工具块,新工具覆盖旧工具 |
| **I4** | `render()` 是只读操作,不修改任何状态字段 |
| **I5** | `finish()` 后 `progressLines == nil`,`heldTool == ""` |
| **I6** | 同一 `stream_id` 下,assembler 实例唯一(由 `streamStateFor` 保证) |

### 3.3 删除的旧逻辑

`websocket_stream_assembler.go` 旧 `ingest()` 方法**整体替换**。以下逻辑全部��除:

| 删除的代码 | 删除原因 |
|-----------|---------|
| `shouldHoldOnlyTool(content)` | 改用 `onToolStart` 显式事件,不需要从前缀猜 |
| `ingest()` 里把工具块当正文 segment 拼进 `visibleText` | 工具块不进 `visibleText` |
| `appendWecomStreamSegment(a.visibleText, a.heldTool)` | `heldTool` 转成 `progressLines` 行,不进 `visibleText` |
| `wecomToolBlockPrefix = "🔧 **"` 前缀判断 | 工具消息不再走 `ingest` 路径 |

---

## 4. 技术方案:engine 层改造

### 4.1 engine 只产标准化事件,不为 wecom 拼展示文本

**当前问题**(`core/engine_turn.go:672-694`):

```go
if streamPreviewToolHold {
    if e.display.ToolMessages {
        toolMsg := fmt.Sprintf(e.i18n.T(MsgTool), toolCount, event.ToolName, formattedInput)
        if len(textParts) > 0 {
            textParts = append(textParts, "\n\n")  // ← 分隔符 hack
        }
        textParts = append(textParts, toolMsg)     // ← 工具块进了正文缓冲
        streamToolHoldNeedsAnswerSeparator = true  // ← 状态分散
    }
    continue
}
```

**改造后**:engine 在 `EventToolUse` 时调用 `sp.onToolStart()`(新接口),不再把 `toolMsg` 拼进 `textParts`。

```go
case EventToolUse:
    // ... 现有 toolSteps.append 等逻辑保留(rich card 路径)...

    if streamPreviewToolHold {
        if e.display.ToolMessages {
            // 不再拼进 textParts,直接走 assembler 的 progressLines
            if sp.canPreview() {
                sp.onToolStart(event.ToolName, explainArg(event), rawArg(event))
            }
            // textParts 不再追加工具消息
            // streamToolHoldNeedsAnswerSeparator 不再设置
        }
        continue
    }
    // ... 非 tool_hold 路径保留 ...
```

### 4.2 删除 `mergeStreamDisplayContent`

**当前问题**(`core/engine_turn.go:1088`):

```go
if e.display.Mode == displayModeStream && !isSilent {
    deliverResponse = mergeStreamDisplayContent(strings.Join(textParts, ""), event.Content, fullResponse)
}
```

这个函数把 stream 期间累积的 `textParts` 和最终 `fullResponse` 二次合并,是"前缀重复"的直接来源之一。

**改造后**:`deliverResponse = fullResponse`,assembler 的 `finish(fullResponse)` 直接用最终答案。

```go
deliverResponse := fullResponse
if e.display.Mode == displayModeStream && !isSilent {
    // 不再二次合并,assembler.finish 会用 fullResponse 覆盖 visibleText
    sp.finish(fullResponse)
}
```

### 4.3 删除 `streamToolHoldNeedsAnswerSeparator`

**当前问题**(`core/engine_turn.go:853-856`):

```go
if streamPreviewToolHold && streamToolHoldNeedsAnswerSeparator && len(textParts) > 0 {
    textParts = append(textParts, "\n\n")  // ← 在正文里插分隔符
    streamToolHoldNeedsAnswerSeparator = false
}
```

这是为了"工具块后面接正文时加分隔符"的 hack。改造后由 assembler 的 `render()` 统一用 `separator` 字段分隔,engine 不再插手。

### 4.4 streamPreview 新增 `onToolStart` 方法

`core/streaming.go` 的 `streamPreview` 新增方法,转发给 platform 的 assembler:

```go
// onToolStart 把工具启动事件转给平台 assembler 的 progressLines。
// 只在 tool_hold 模式下生效。
func (sp *streamPreview) onToolStart(toolName, explainArg, rawArg string) {
    sp.mu.Lock()
    defer sp.mu.Unlock()
    if sp.degraded || !sp.cfg.Enabled {
        return
    }
    if sp.previewMode() != "tool_hold" {
        return
    }
    // 通过新增的 ProgressAssembler 接口转发
    if assembler, ok := sp.platform.(ProgressAssembler); ok {
        // assembler 实现侧更新 progressLines 并触发 throttled flush
        assembler.OnToolStart(sp.previewMsgID, toolName, explainArg, rawArg)
    }
}
```

### 4.5 新增 `ProgressAssembler` 平台接口

`core/interfaces.go` 新增:

```go
// ProgressAssembler 是平台可选接口,用于把工具进度作为 UI 侧信道
// 渲染到流式预览的独立区,不进入正文。
// 平台实现这个接口后,engine 在 tool_hold 模式下会调用 OnToolStart
// 而不是把工具消息拼进正文缓冲。
type ProgressAssembler interface {
    OnToolStart(previewHandle any, toolName, explainArg, rawArg string) error
    OnToolComplete(previewHandle any, toolName, resultSummary string) error
}
```

wecom 的 `WSPlatform` 实现这个接口,内部调 `wsStreamAssembler.onToolStart()`。

### 4.6 EventToolResult 也必须改(关键遗漏点)

**当前问题**(`core/engine_turn.go:789-798`):

```go
if streamPreviewToolHold {
    if e.display.ToolMessages {
        if len(textParts) > 0 {
            textParts = append(textParts, "\n\n")
        }
        textParts = append(textParts, resultMsg)     // ← 工具结果也进了 textParts!
        streamToolHoldNeedsAnswerSeparator = true
    }
    continue
}
```

不仅 `EventToolUse` 会把工具块拼进 `textParts`,`EventToolResult`(工具返回结果)在 `tool_hold` 模式下也走同样的 `append` 路径。如果只改 `EventToolUse` 不改 `EventToolResult`,工具结果还是会污染 `visibleText`。

**改造后**:`EventToolResult` 在 `tool_hold` 模式下也调 `sp.onToolComplete()`,不进 `textParts`:

```go
case EventToolResult:
    // ... 现有 resultMsg 构造保留 ...
    if streamPreviewToolHold {
        if e.display.ToolMessages {
            if sp.canPreview() {
                sp.onToolComplete(event.ToolName, result)  // 转给 assembler progressLines
            }
            // textParts 不再追加 resultMsg
            // streamToolHoldNeedsAnswerSeparator 不再设置
        }
        continue
    }
    // ... 非 tool_hold 路径保留 ...
```

### 4.7 EventThinking / mapPlan 的处理

**当前问题**(`core/engine_turn.go:533-590`、`agent/acp/mapping.go:223-250`):

ACP 的 `mapPlan` 把 plan 条目转成 `EventThinking`(`mapping.go:245-249`)。`EventThinking` 在 stream 模式下会:
- `e.display.ThinkingMessages` 为 true 时:走 rich card 路径(wecom 不支持)或 `sp.appendTextNow()`
- 为 false 时:走 `sp.appendSeparator()` 分隔

thinking/plan 文本会进 `sp.fullText`,和正文混合。

**改造决策**:thinking/plan 是否应该进 `visibleText`?

- **选项 A**(推荐):thinking 进独立 `thinkingLines` 区(第四区),和 `progressLines` 类似,finalize 时清空
- **选项 B**:thinking 不展示在企微(对齐 openclaw 的 `streaming.preview.toolProgress` 思路,thinking 默认不进预览)
- **选项 C**:thinking 继续进 `visibleText`(现状,但容易和正文混合)

**建议**:wecom 默认 `ThinkingMessages = false`(stream 模式约定),thinking 不进 preview。如需展示思考,用独立 `thinkingLines` 区。本方案先实现选项 B(thinking 不展示),后续如需再扩展第四区。

### 4.8 streamPreview.transform 链必须保留

**当前实现**(`core/streaming.go:41`、`core/engine_turn.go:390-392`):

```go
workspaceRenderer := func(content string) string {
    return e.renderOutgoingContentForWorkspace(state.platform, content, workspaceDir)
}
sp := newStreamPreview(e.streamPreview, state.platform, state.replyCtx, e.ctx, workspaceRenderer)
```

`sp.transform` 是 `TransformLocalReferences`(`engine_reply.go:340-345`),会把正文里的本地文件引用转成可点击链接。**这个 transform 作用于所有发往 preview 的文本**。

**改造影响**:assembler 的 `appendText` 收到的是 raw 文本,如果绕过 `sp.transform`,文件引用就不会被渲染。

**改造方案**:assembler 不接管 transform 职责,transform 仍在 `sp.flushLocked`/`sp.finish` 出口处应用:

```go
// assembler 只存 raw text
func (a *wecomStreamAssembler) appendText(text string) string {
    a.visibleText += text
    return a.render()  // render 产出 raw 文本
}

// sp 在调用 platform 发送前应用 transform(现状保留)
func (sp *streamPreview) flushLocked(text string) {
    if sp.transform != nil {
        text = sp.transform(text)  // ← 保留
    }
    // ... 发送 text ...
}
```

**关键**:assembler 的 `visibleText` 存 raw text,transform 在发送出口应用。这样 assembler 是纯状态机,transform 是渲染管线的一环。

### 4.9 freeze()/detachPreview() 路径

**当前实现**(`core/streaming.go:305-354`、`core/engine_turn.go:746-748,931-933`):

`sp.freeze()`:取消定时器,把当前 `fullText` 最后更新一次到 preview,然后标记 `degraded = true` 不再更新。
`sp.detachPreview()`:`previewMsgID = nil`,让冻结的 preview 成为永久消息(不再被后续 discard 删除)。

调用场景:
- `EventPermissionRequest`(L931):权限提示前冻结
- `EventToolUse` 非 tool_hold 路径(L746):工具调用前冻结已累积文本
- `EventThinking` compact 路径(L614):思考前冻结

**改造影响**:freeze 会把 assembler 当前状态 render 出来作为"最终态"留在企微。新 assembler 的 `render()` 此时产出 `progressLines + visibleText`,作为冻结快照。

**改造方案**:assembler 新增 `snapshot()` 方法,freeze 时调用:

```go
// snapshot:产出当前状态的只读快照,用于 freeze。
// 不修改状态,不设置 finished 标志。
func (a *wecomStreamAssembler) snapshot() string {
    a.mu.Lock(); defer a.mu.Unlock()
    return a.render()
}
```

`sp.freeze()` 改造:用 `assembler.snapshot()` 替代 `sp.fullText`。

**注意**:freeze 后 assembler **不 reset**,因为可能后续 turn 继续(权限批准后)。真正 reset 在 turn 结束或 `sp.discard()` 时。

### 4.10 silentHold(NO_REPLY 前缀 hold)

**当前实现**(`core/engine_turn.go:357,873-884`):

```go
silentHold := false  // true while accumulated segment text could still resolve to a bare NO_REPLY marker
// ...
segmentText := strings.Join(textParts[segmentStart:], "")
if silentHold {
    if !couldBeSilentPrefix(segmentText) {
        silentHold = false
        if sp.canPreview() {
            sp.appendText(segmentText) // flush all held chunks at once
        }
    }
} else if couldBeSilentPrefix(segmentText) {
    silentHold = true  // hold streaming until we know
} else if sp.canPreview() {
    sp.appendText(event.Content)
}
```

流式时如果文本可能是 `NO_REPLY` 的前缀(如收到 "NO_"、"NO_R"),先 hold 不发,等确认不是再 flush。这是为了避免发出去又删掉的尴尬。

**改造影响**:silentHold 机制和 assembler 的 `appendText` 直接交互。如果 assembler 每次 `appendText` 都立即 render 并发送,silentHold 就失效了。

**改造方案**:**silentHold 逻辑保留在 engine 层**,不进 assembler。engine 在确认非 NO_REPLY 前缀后才调 `sp.appendText()`:

```go
// engine 层(保留 silentHold 逻辑)
if silentHold {
    if !couldBeSilentPrefix(segmentText) {
        silentHold = false
        sp.appendText(segmentText)  // 一次性 flush held chunks
    }
    // silentHold 期间不调 sp.appendText,assembler 不更新
} else if couldBeSilentPrefix(segmentText) {
    silentHold = true
} else {
    sp.appendText(event.Content)
}
```

assembler 不知道 silentHold 的存在,它只看到"有时候 appendText 被延迟调用了"。

### 4.11 segmentStart 分段机制

**当前实现**(`core/engine_turn.go:356,571-744,872-928,1174-1189`):

`textParts` 是 `[]string`,`segmentStart` 是 int 索引,标记"已发送到第几段"。工具调用/思考/权限提示会推进 `segmentStart`,把之前的段通过 `sp.freeze()`+`detachPreview()` 或 `sendWorkspace()` 发出去。

**改造影响**:如果 assembler 接管 visibleText,`segmentStart` 的"分段发送"语义需要重新定义。assembler 的 `visibleText` 是完整累积,不是分段。

**改造决策**:**`segmentStart` 机制在 stream 模式下废弃**。理由:
- stream 模式本来就是"单条消息持续更新",不需要分段发送
- 工具/思考中断时,用 `freeze()`+`detachPreview()` 冻结当前 preview,新 preview 重新开始(见 4.9)
- 非 stream 模式(quiet/compact)保留 `segmentStart` 机制不变

**改造后** stream 模式下:
- `textParts` 仍累积(用于 history 上下文)
- `segmentStart` 在 stream 模式下始终为 0(不分段)
- 工具/思考中断时:`sp.freeze()`+`sp.detachPreview()`,assembler reset,下一段文本进新 preview

### 4.12 三套正文缓冲的统一

**当前实现**:
1. `textParts []string`(`engine_turn.go:355`)— 分段累积,用于 history 和分段发送
2. `partialText string`(`engine_turn.go:365`)— rich card 用的累积文本
3. `sp.fullText string`(`streaming.go:43`)— stream preview 用的累积文本

**问题**:三套缓冲各自维护,容易不同步。`partialText += event.Content` 和 `sp.appendText(event.Content)` 在 `EventText` 里被同时调用(L857-886)。

**改造方案**:
- **`textParts` 保留**:用于 history 上下文,model 回放需要,assembler 不接管
- **`partialText` 保留**:wecom 不走 rich card 路径,但其他平台用,wecom 场景下它是 dead code,不影响
- **`sp.fullText` 的角色变化**:assembler 接管后,`sp.fullText` 不再是"发往 preview 的文本",而是退化为"raw 累积器"。实际发往 preview 的是 `assembler.render()` 的产物

**关键**:assembler 不取代 `textParts` 和 `partialText`,只取代 `sp.fullText` 的"preview 发送源"角色。`sp.fullText` 可以保留作为 raw 累积(供 silentHold 判断等),但 `sp.flushLocked()` 改为发送 `assembler.render()` 而非 `sp.fullText`。

### 4.13 compactProgressWriter 并行路径(确认不受影响)

**当前实现**(`core/progress_compact.go:304-340`、`core/engine_turn.go:414`):

`cp := newCompactProgressWriter(...)` 和 `sp` 同时创建。`cp.enabled` 只在 `ProgressStyleCompact` 或 `ProgressStyleCard` 时为 true。

**wecom 现状**:wecom 没有实现 `ProgressStyleProvider` 接口,`progressStyleForPlatform(p)` 返回 `ProgressStyleLegacy`,`cp.enabled = false`。

**结论**:**wecom 不走 compactProgressWriter 路径**,本改造不影响 `cp`。但要注意:wecom 的工具进度展示完全依赖 `sp` + assembler,没有 `cp` 作为备选。

### 4.14 多 turn / queued 消息重建 sp

**当前实现**(`core/engine_turn.go:1257-1318,1558-1592`):

`messages.queue = "steer"`(默认)时,新消息进入当前 run;`"followup"`/`"collect"` 时排队。排队消息处理时(`L1317`)会重建 `sp`:

```go
sp = newStreamPreview(e.streamPreview, queued.platform, queued.replyCtx, e.ctx, queuedRenderer)
```

**问题**:新 `sp` 意味着新 assembler 实例。如果前一个 turn 的 assembler 还有未发送内容,会丢失。

**改造影响**:每个 turn 本来就应该有独立的 preview 消息。前一个 turn 的 finalize(`sp.finish()`)会清空 assembler,所以新 turn 重建 `sp` 是合理的。

**改造方案**:无需特殊处理。但要确保:
- 前 turn finalize 时,assembler `finish()` 被调用,progressLines 清空
- 新 turn 开始时,`sp` 和 assembler 都是全新实例
- 如果前 turn 异常中断(未 finalize),`sp.discard()` 要触发 `assembler.discard()`

### 4.15 不同 agent 的事件粒度差异

**当前实现**:
- **ACP**(`agent/acp/mapping.go`):`tool_call_update` 的 `in_progress`/`pending` 状态被抑制(L150-156),只返回 `completed`/`failed` 的 `EventToolResult`。`tool_call` 事件产生 `EventToolUse`。
- **claudecode**(`agent/claudecode/session.go:341,372`):直接解析 stream-json,`EventToolUse` 在工具开始时发,无中间态。
- **codex**(`agent/codex/session.go`):类似 ACP,但有 preamble/commentary 消息。

**改造影响**:tool_hold 机制对不同 agent 效果不同:
- ACP:已抑制中间态,tool_hold 主要防止 `EventToolUse` 和 `EventToolResult` 污染正文
- claudecode:无中间态,tool_hold 防止 `EventToolUse` 污染正文
- codex:preamble 可能走 `EventThinking`,需确认是否进 preview

**改造方案**:assembler 的 `onToolStart`/`onToolComplete` 是通用接口,对所有 agent 一视同仁。不同 agent 的事件粒度差异由各自的 mapping 层处理,assembler 不感知。

### 4.16 replyFooter 注入 fullResponse

**当前实现**(`core/engine_turn.go:1074-1085`):

```go
if footer := e.buildReplyFooter(replyAgent, state.agentSession, workspaceDir, footerContext); footer != "" {
    cleanResponse = appendReplyFooter(cleanResponse, footer)
} else if contextText != "" {
    cleanResponse += "\n" + contextText
}
fullResponse = cleanResponse  // ← footer 已经在 fullResponse 里
```

footer(如 `[ctx: ~30%]`、agent 签名等)在 `fullResponse` 里。`mergeStreamDisplayContent` 用 `fullResponse` 做最终合并,footer 会被带进 stream。

**改造影响**:删掉 `mergeStreamDisplayContent` 后,`sp.finish(fullResponse)` 会把 footer 带进 assembler 的 `visibleText`。这是合理的 — footer 是最终答案的一部分。

**但要确认**:footer 在 preview 阶段不应该出现(它是 finalize 时才计算的)。assembler 的 `appendText` 期间不会有 footer,只有 `finish(fullResponse)` 时 footer 才进入。符合预期。

### 4.17 waitOutgoing 限流器

**当前实现**(`core/engine_reply.go:332-334`):

```go
func (e *Engine) waitOutgoing(p Platform) error {
    // 阻塞在 per-platform 限流器上
}
```

`waitOutgoing` 在所有 `sendWorkspace`/`sendWithErrorForWorkspace` 调用前执行,防止发太快。

**改造影响**:wecom 的 stream 发送走 `sendStreamFrameAndWaitAck`,不经过 `waitOutgoing`。但 `Send()`(aibot_send_msg,长消息分片的后续片)会经过。

**改造方案**:无需改动。stream 队列自有串行+ack 机制,`waitOutgoing` 只管非 stream 的 proactive send。

### 4.18 Webhook 模式 finalize 路径(确认不受影响)

**当前实现**(`platform/wecom/wecom.go:484-527`):

Webhook 模式的 `Platform` 只实现 `Reply`/`Send`/`SendImage`,没有 `SendPreviewStart`/`UpdateMessage`/`FinalizePreviewMessage`。

**结论**:Webhook 模式不走 stream preview,`sp.canPreview()` 返回 false,所有流式逻辑被跳过。本改造完全不影响 Webhook 模式。

---

## 5. 技术方案:发送层改造

### 5.1 删除 `lastAcked` 补前缀逻辑

**当前问题**(`websocket_stream_queue.go:99-128`):

```go
rendered, shouldSend := state.assembler.ingest(req.content, req.finish)
lastAckedMatches := !req.finish && state.lastAcked == rendered
// ...
if err == nil && !req.finish {
    state.lastAcked = rendered  // ← 用于下次补前缀
}
```

`lastAcked` 的存在本身就会诱导补前缀逻辑,即使现在没有显式补前缀代码,未来也会长出来。

**改造后**:`runStreamQueue` 只保留两个职责:

```go
func (p *WSPlatform) runStreamQueue(key string, state *wsStreamState, rc wsReplyContext) {
    for {
        req := state.dequeuePending()  // 串行取出
        if req == nil {
            state.markIdle()
            return
        }

        // 职责 1:去重(相同内容跳过)
        if !req.finish && state.lastRendered == req.content {
            req.done <- nil
            close(req.done)
            continue
        }

        // 职责 2:发送
        reqID, frame, err := p.buildStreamFrame(rc, req.content, req.finish)
        if err == nil {
            err = p.writeAndWaitAck(context.Background(), frame, reqID)
        }
        if err == nil {
            state.lastRendered = req.content  // 只用于去重,不用于补前缀
        }
        req.done <- err
        close(req.done)
    }
}
```

**关键变化**:
- `lastAcked` 重命名为 `lastRendered`,语义从"上次 ack 的内容(可反推前缀)"变成"上次渲染的内容(只用于去重)"
- 删除 `assembler.ingest()` 调用 — assembler 的状态由 engine 通过 `appendText`/`onToolStart`/`finish` 显式更新,`runStreamQueue` 只消费 `req.content`(已经是 `render()` 的产物)

### 5.2 长消息分片时机后移

**当前问题**(`websocket_stream_reply.go:75-96`):

```go
func (p *WSPlatform) sendFinalReplyChunks(ctx context.Context, rc wsReplyContext, content string) error {
    chunks := splitByBytes(content, wecomStreamMaxBytes)  // ← 先分片
    // ... 第一片走 finish=true
    // ... 后续走 Send (aibot_send_msg)
}
```

分片发生在 assembler 之外,但 `wecomPreviewPayload` 在 preview 阶段又做了截断,两套截断逻辑不一致。

**改造后**:分片只在 finalize 时发生,且分片产物**不再进 assembler**:

```go
func (p *WSPlatform) sendFinalReplyChunks(ctx context.Context, rc wsReplyContext, finalText string) error {
    // assembler 已经产出 finalText(finish 调用),这里只做分片和发送
    chunks := splitSmart(finalText, wecomStreamMaxBytes)  // 智能切分
    if len(chunks) == 1 {
        return p.sendStreamFrameAndWaitAck(ctx, rc, chunks[0], true)
    }
    // 多片:第一片 finish=true 替换当前 stream 消息
    if err := p.sendStreamFrameAndWaitAck(ctx, rc, chunks[0], true); err != nil {
        return err
    }
    // 后续走 aibot_send_msg(markdown) 新发消息
    for i := 1; i < len(chunks); i++ {
        if err := p.Send(ctx, rc, chunks[i]); err != nil {
            return err
        }
    }
    return nil
}
```

### 5.3 智能切分算法(替换纯字节切)

新增 `splitSmart`,对齐 openclaw 的 `breakPreference`(`docs/concepts/streaming.md:74`):

```go
// splitSmart 按优先级切分长文本:
// 1. 段落边界(空行)
// 2. 单行换行
// 3. 句号/问号/感叹号
// 4. 空格
// 5. 硬切字节(UTF-8 安全)
// 代码块:强制切时 ``` 闭合再重开,保证每片都是合法 markdown。
func splitSmart(content string, maxBytes int) []string {
    // 1. 按段落边界预切
    paragraphs := strings.Split(content, "\n\n")
    // 2. 贪心合并到 maxBytes
    // 3. 单段超 maxBytes 时按 newline/sentence/whitespace 切
    // 4. 代码块检测:切点在 ``` 之间时,先闭合再重开
    // 5. UTF-8 安全:字节切点不在 rune 中间
}
```

替换当前 `splitByBytes`(`wecom.go:915`)的纯字节切,避免切坏 UTF-8 和代码块。

---

## 6. 改造前后效果对比

### 6.1 场景一:工具调用 + 正文回答

**改造前**(当前行为):

```
[流式中]
🔧 **工具 #1: Bash**
---
`wc -m CHANGELOG.md`

项目根目录没有 `CHANGELOG.md`。

🔧 **工具 #1: Bash**
---
`wc -m CHANGELOG.md`           ← 工具块重复(前缀补错)

根据你的问题...                    ← 正文
```

**问题**:工具块进了 `visibleText`,和正文交错;`lastAcked` 误判导致工具块前缀重复。

**改造后**:

```
[流式中]
🛠️ Bash: 统计 CHANGELOG.md 行数

---

项目根目录没有 `CHANGELOG.md`。

根据你的问题...                    ← 正文持续追加
```

**[finalize 后]**:
```
根据你的问题,CHANGELOG.md 不存在,建议...   ← 工具进度行已清空
```

**改善点**:
- 工具进度行单行紧凑(`🛠️ Bash: ...`),不是多行 markdown 块
- 工具进度在 `progressLines` 区,和正文用 `---` 分隔,边界清晰
- finalize 时 progressLines 清空,只留正文
- 个人微信也能显示 emoji(不依赖 markdown 加粗)

### 6.2 场景二:长消息(超过 2048 字节)

**改造前**:

```
[preview 阶段]
[内容较长，正在整理后续片段...]    ← 截断提示

[finalize 阶段]
[内容较长，正在整理后续片段...]    ← lastAcked 误判,把截断提示当前缀补回
[完整内容第一片]
[完整内容第二片]                    ← 用户看到:截断提示 + 第一片 + 第二片
```

**改造后**:

```
[preview 阶段]
[内容开头...]
[内容较长，正在整理后续片段...]    ← 截断提示

[finalize 阶段]
[完整内容第一片](finish=true)     ← 直接替换,不补前缀
[完整内容第二片](aibot_send_msg)
[完整内容第三片](aibot_send_msg)
```

**改善点**:
- preview 截断提示只出现在 preview 阶段,finalize 时被完整内容直接替换
- 不再有 `lastAcked` 补前缀,截断提示不会残留
- 分片发生在 assembler 产出最终文本之后,不再二次聚合

### 6.3 场景三:多个工具连续调用

**改造前**:

```
🔧 **工具 #1: Web Search**
---
`query: "discord edit message"`

🔧 **工具 #2: Read**
---
`path: /tmp/file`

🔧 **工具 #1: Web Search**      ← 工具块累积,旧工具块没清
---
`query: "discord edit message"`

正文...
```

**改造后**:

```
[流式中]
🛠️ Web Search: discord edit message
🛠️ Read: /tmp/file

---

(正在生成回答...)
```

**[finalize 后]**:
```
根据搜索结果...                   ← progressLines 清空,只留正文
```

**改善点**:
- `progressLines` 有 `maxProgressLines` 上限(默认 4),超过时丢最旧行(FIFO),不会无限累积
- 工具进度行只显示当前活跃的工具,完成的工具可选追加 `✅` 行
- finalize 时全部清空

### 6.4 场景四:工具中间态(`tool_call_update` 逐步拼接命令)

**改造前**:

```
🔧 **工具 #1: Bash**
---
`wc`

🔧 **工具 #1: Bash**
---
`wc -m`                     ← 中间态刷屏

🔧 **工具 #1: Bash**
---
`wc -m CHANGELOG.md`        ← 最终态
```

**改造后**:

```
🛠️ Bash: 统计 CHANGELOG.md 行数    ← 只显示最终态(hold 期间不刷)
```

**改善点**:
- `onToolStart` 同一工具多次调用只覆盖 `heldTool`,`progressLines` 里只保留最新一行
- 中间态不触发 `render()`,不发 frame
- 文本到来时才触发 `render()`,把最新工具进度行和正文一起发

### 6.5 改造效果汇总

| 维度 | 改造前 | 改造后 |
|------|-------|-------|
| 内容真源数量 | 4 处(textParts / heldTool / pendingTool / lastAcked) | 1 处(assembler) |
| 工具信息位置 | 进 visibleText,和正文混合 | 进 progressLines,物理隔离 |
| finalize 后工具残留 | 有,需手动清 | 无,finish() 自动清 |
| 前缀重复 | 有(lastAcked 误判) | 无(发送层不补前缀) |
| 长消息分片混乱 | 有(分片前后二次聚合) | 无(分片在 assembler 之后) |
| 工具中间态刷屏 | 部分(hold 判断脆弱) | 无(显式事件驱动) |
| 单条消息上限保护 | 有(splitByBytes) | 有(splitSmart,且不切坏 UTF-8/代码块) |
| 个人微信兼容性 | 差(markdown 加粗不显示) | 好(emoji + 纯文本) |

---

## 7. 落地步骤

### 阶段一:状态机替换(基础,其他阶段的前置)

1. 新增 `wecomStreamAssembler` 三区版本(替换 `wsStreamAssembler`)
2. 新增 `ProgressAssembler` 接口(`core/interfaces.go`)
3. `WSPlatform` 实现 `OnToolStart`/`OnToolComplete`
4. 单元测试覆盖第 3.2 节的全部不变量

**验收**:assembler 单测全绿,旧 `ingest()` 删除。

### 阶段二:engine 解耦

1. `engine_turn.go` 的 `EventToolUse` 在 `tool_hold` 模式下调 `sp.onToolStart()`,不再 `append` 工具消息到 `textParts`(模块 #2)
2. `engine_turn.go` 的 `EventToolResult` 在 `tool_hold` 模式下调 `sp.onToolComplete()`,不进 `textParts`(模块 #3)
3. 删除 `streamToolHoldNeedsAnswerSeparator` 变量及其所有引用(模块 #8)
4. 删除 `mergeStreamDisplayContent()` 函数及其调用,`deliverResponse = fullResponse`(模块 #4)
5. `sp.finish(fullResponse)` 时 assembler 清空 progressLines
6. `EventThinking`/`mapPlan` 在 stream 模式下默认不进 preview(模块 #10)
7. 保留 `sp.transform` 在发送出口应用(模块 #11)
8. 保留 `silentHold` 逻辑在 engine 层(模块 #13)
9. stream 模式下 `segmentStart` 不分段,用 freeze+detach 替代(模块 #14)

**验收**:`core` 测试中 tool_hold 相关用例通过;`a.log` 回放场景下 visibleText 不再包含 `🔧` 前缀文本;thinking 默认不进 preview。

### 阶段二.5:freeze/detach 路径适配(新增)

1. assembler 新增 `snapshot()` 方法(模块 #12)
2. `sp.freeze()` 改用 `assembler.snapshot()` 替代 `sp.fullText`
3. `sp.discard()` 触发 `assembler.discard()`
4. 验证权限提示/工具中断/错误路径的 freeze 行为

**验收**:权限提示时 preview 冻结成当前状态快照;中断后新 preview 正确重建。

### 阶段三:发送层瘦身

1. `runStreamQueue` 删除 `lastAcked`,改为 `lastRendered`(只去重)(模块 #5)
2. `enqueueLatestStreamSend` 删除 `shouldHoldOnlyTool` 判断(改由 engine 显式事件驱动)(模块 #6)
3. `wecomPreviewPayload` 保留(preview 截断提示仍需要)

**验收**:`a.log` 里"前缀重复"场景不再出现;长消息 finalize 不再补截断提示。

### 阶段四:切分算法升级

1. `splitByBytes` 替换为 `splitSmart`(段落/换行/句子/空格/字节优先级)
2. 代码块切分时闭合再重开 fence
3. UTF-8 安全切分

**验收**:含代码块的长消息分片后,每片都是合法 markdown;中文不在 rune 中间被切。

### 阶段五:观测性

新增结构化日志字段:

| 字段 | 含义 |
|------|------|
| `phase` | `preview` / `update` / `finalize` / `followup` |
| `render_source` | `append_text` / `on_tool_start` / `on_tool_complete` / `finish` |
| `visible_len` | visibleText 字符数 |
| `progress_lines` | progressLines 当前行数 |
| `held_tool_len` | heldTool 字符数 |
| `dedup_skipped` | 本次是否被去重跳过 |

**验收**:看到混乱时能直接判断是 assembler 状态错、engine 事件错、还是发送层重发。

---

## 8. 测试要点

### 8.1 状态机不变量测试(阶段一)

| 测试名 | 验证不变量 |
|--------|----------|
| `TestAppendText_DoesNotTouchProgressLines` | I1 |
| `TestOnToolStart_DoesNotTouchVisibleText` | I1 |
| `TestOnToolStart_OverwritesHeldTool` | I3 |
| `TestFinish_ClearsProgressLines` | I5 |
| `TestRender_IsReadOnly` | I4(连续 render 结果一致,状态不变) |
| `TestProgressLines_BoundedByMax` | 超限时 FIFO 丢弃 |

### 8.2 集成测试(阶段二、三)

| 测试名 | 验证场景 |
|--------|---------|
| `TestToolHold_ToolOnlyUpdatesDoNotSendFrame` | 工具中间态��发 frame |
| `TestToolHold_TextArrives_AggregatesToolAndText` | 正文到来时合并发送 |
| `TestFinalize_NoToolPrefixInVisibleText` | finalize 后 visibleText 不含工具前缀 |
| `TestLongMessage_FinalizeNoPrefixDuplication` | 长消息分片不补前缀 |
| `TestPreviewTruncation_FinalizeReplacesCleanly` | 截断提示不残留 |
| `TestMultiTool_OnlyLatestProgressVisible` | 多工具时只显示最新进度行 |

### 8.3 回归测试

用 `a.log`(参考 `wecom-stream-current-flow-design.md` 第 1 节)里的真实场景做回放:

- `2026-05-28 20:44:15` 段:验证流式发送正常
- `2026-05-28 20:37:47` 段:验证前缀不重复

### 8.4 验证命令

```bash
GOCACHE=$(pwd)/.gocache go test ./platform/wecom ./core -run 'TestWecomStreamAssembler|TestToolHold|TestFinalize'
```

---

## 9. 风险与回退

### 9.1 风险

| 风险 | 缓解 |
|------|------|
| engine 改动影响其他平台 | `ProgressAssembler` 是可选接口,未实现的平台走旧路径;改造只在 `tool_hold` 模式生效 |
| `mergeStreamDisplayContent` 删除后非 stream 模式受影响 | 该函数只在 `displayModeStream && !isSilent` 下调用,其他模式不受影响 |
| `splitSmart` 切分边界不准 | 先实现 + 单测覆盖代码块/中文/混合场景;灰度阶段保留 `splitByBytes` 作为 fallback |
| assembler 并发访问 | `sync.Mutex` 保护,所有公开方法加锁 |

### 9.2 回退方案

每个阶段独立可回退:

- 阶段一回退:保留旧 `wsStreamAssembler` 文件,通过 build tag 切换
- 阶段二回退:engine 改动用 feature flag(`wecom.useNewEngineEvents`),默认 false
- 阶段三回退:`lastAcked` 字段保留但不读取,出问题时一行注释恢复

---

## 10. 配置项(参考 openclaw)

```toml
[[projects.platforms]]
type = "wecom"

[projects.platforms.options]
mode = "websocket"
bot_id = "xxx"
bot_secret = "xxx"

# 流式配置(新增,可选,有默认值)
[projects.platforms.options.streaming]
mode = "progress"               # off | partial | progress,默认 progress
preview_threshold_ms = 5000     # 多久没出文本才显示进度草稿,默认 5000
min_emit_interval_ms = 300      # 流式更新最小间隔,默认 300
min_emit_chars = 20             # 内容变化多少字符才发,默认 20
tool_hold = true                # 工具中间态暂存,默认 true

[projects.platforms.options.streaming.progress]
max_lines = 4                   # 最多显示几行进度,默认 4
max_line_chars = 120            # 单行超此字符数中间省略,默认 120
detail = "explain"              # explain | raw,默认 explain
show_tool_complete = false      # 是否显示 ✅ 完成行,默认 false
separator = "\n\n---\n\n"       # 进度区和正文区分隔符
```

---

## 11. 对齐 openclaw 的设计哲学(总结)

| openclaw 原则 | qhn 当前 | qhn 改造后 |
|--------------|---------|-----------|
| 展示文本是状态的投影,不是状态本身 | 展示文本是可变状态,每层都改 | assembler 独占,`render()` 只读投影 |
| 工具进度是 UI 侧信道,不进 model context | 工具块进 `textParts` | 工具块进 `progressLines`,物理隔离 |
| 发送层只串行去重,不做猜测补全 | `lastAcked` 补前缀 | `lastRendered` 只去重 |
| 单一事实源 | 4 处状态并存 | 1 处 assembler |
| finalize 安全回退,不强行覆盖 | 分片后又二次聚合 | 分片在 assembler 之后,不二次聚合 |

**一句话**:对齐的不是图标或 markdown,而是**让展示文本成为状态的只读投影**。

---

## 12. 相关文件索引

| 文件 | 角色 |
|------|------|
| `platform/wecom/websocket_stream_assembler.go` | 状态机(替换:三区 + snapshot) |
| `platform/wecom/websocket_stream_queue.go` | 发送队列(瘦身:删 lastAcked) |
| `platform/wecom/websocket_stream_reply.go` | finalize 分片(后移分片时机) |
| `platform/wecom/websocket.go` | 平台主体(实现 ProgressAssembler) |
| `core/engine_turn.go` | 事件处理(解耦 EventToolUse/Result/Thinking,silentHold 保留,segmentStart 废弃) |
| `core/engine_session_cmds.go` | `mergeStreamDisplayContent`(删除) |
| `core/engine_reply.go` | `renderOutgoingContentForWorkspace`(transform 保留,不改动) |
| `core/streaming.go` | `streamPreview`(新增 onToolStart,freeze 改用 snapshot) |
| `core/interfaces.go` | 新增 `ProgressAssembler` 接口 |
| `core/progress_compact.go` | 确认 wecom 不走此路径(不改动) |
| `core/i18n.go` | 工具消息模板(可选调整) |
| `agent/acp/mapping.go` | 事件源(确认 tool_call_update 抑制不变) |
| `agent/claudecode/session.go` | 事件源(确认 EventToolUse 粒度不变) |

参考文档:
- `docs/upgradefeature/wecom-stream-current-flow-design.md`(现状诊断)
- `docs/upgradefeature/wecom-stream-tool-hold.md`(tool_hold 现状)
- openclaw: `docs/concepts/streaming.md`、`docs/concepts/progress-drafts.md`
