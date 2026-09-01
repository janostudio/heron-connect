# 空响应（模型接口异常）根因分析与同类 Bug 排查

> 2026-09-01，由用户报告"看到空响应，不知道什么出错，后来发现是模型接口返回不对"触发。

## 1. 你遇到的 bug：空 result 被当正常空响应

### 完整链路（为什么错误信息丢了）

```
模型接口返回不对（空 content / 异常被 CLI 吞成空 result）
  → CodeBuddy CLI 正常退出，输出 {"type":"result","result":""}
  → codebuddy 适配器 handleResult（agent/codebuddy/session.go:419）
      finalText = ""（空 result，pendingText 也空）
      → 发出空 content 的 EventResult
  → gotResult = true（session.go:209）
      → 直接 return，不走 exitFallbackEvent 兜底
  → engine 收到空 EventResult
  → engine_turn.go:1193 输出 "(空响应)"
```

**错误信息在三个环节被吞掉：**

1. **`handleResult` 无条件发 EventResult**（codebuddy/claudecode/codex 等所有适配器都一样）——即使 content 空，也当成正常结束，不检查"这轮是否真的产出了内容"。
2. **`gotResult` 短路了兜底逻辑**（codebuddy session.go:209）——只要有 result 事件就 return，`exitFallbackEvent`（已能正确把"静默退出"转成 EventError）根本没机会跑。
3. **stderr 被忽略**（session.go:210-215）——空 result 时 `exitErr == nil`，stderr 里的真实原因（"auth 过期"、"接口返回错误"等）只在前两种情况才被消费，这里直接跳过。

### 根因本质

**"空 result" 被当成"正常的空回复"，而不是"异常"。** 现有的 `exitFallbackEvent` 只兜底了"进程退出但没有 result 事件"的场景，漏掉了"进程正常退出 + 有 result 事件但 result 内容为空"的场景。

## 2. 同类问题（排查中发现）

### 2.1 所有适配器共享同一缺陷

`handleResult` 在 codebuddy（session.go:419）、claudecode（session.go:429）等处都是无条件 `EventResult{Content: content}`，空 content 不设防。这是**系统性**的，不是单个适配器。

### 2.2 engine 层的 `(空响应)` 无诊断信息

`engine_turn.go:1193` 输出 `(空响应)` 时，没有任何"为什么空"的上下文（是模型真没内容，还是接口错误，还是适配器没解析到）。用户只能看到两个字的占位符。

## 3. 修复建议（按优先级）

### 修复 A（核心，治本）：适配器层检测空 result

在 `handleResult` 里，当 `content == ""` 且无 pendingText 时，不直接发 EventResult，而是发 **EventError**（携带 stderr 或明确的"模型返回空结果"诊断）。这样：

- 用户看到的是明确的错误，而非 `(空响应)`；
- 复用现有的 error 处理路径（`sanitizeAgentError` 等）。

需要动的文件：
- `agent/codebuddy/session.go` handleResult + readLoop（把 stderr 传给 handleResult）
- `agent/claudecode/session.go` handleResult
- 其他有 handleResult 的适配器（codex/heron 等，逐一确认）

### 修复 B（兜底）：engine 层空响应加诊断

`engine_turn.go:1193` 处，当输出 `(空响应)` 时，同时 `slog.Warn` 一条带 session/agent/耗时 的诊断日志，至少让日志可查。**低成本、立即缓解排查难**。

### 修复 C（可选，增强）：result 事件加"是否有实质输出"标记

在 Event 里加一个 `HadOutput` 之类的字段，或让适配器在空 result 时附带 stderr 到 Event.Metadata，engine 据此给出更精准的提示。

## 4. 建议修复范围

- **最小修复**：修复 B（engine 层加诊断日志）——半天内可完成，立即改善排查体验。
- **治本修复**：修复 A（适配器空 result → EventError）——覆盖所有适配器，彻底消除"空响应"假象。

两者建议一起做，A 为主、B 为辅。

## 5. 验证方式

- 单测：构造"空 result 事件"，断言产出的是 EventError 而非空 EventResult；
- 端到端：mock 一个返回空 result 的 CLI 输出，确认用户收到的是明确错误而非 `(空响应)`。
