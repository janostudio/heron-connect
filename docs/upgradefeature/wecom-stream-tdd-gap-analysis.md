# 企微流式改造 TDD 测试缺口分析

## 0. 背景与目标

本文档从 TDD(测试驱动开发)角度,审查 `wecom-stream-align-openclaw-design.md` 改造方案的测试充分性。

**核心问题**:按照该方案改造后,现有测试用例是否充分保障功能不回归?新增的不变量(I1-I6)是否有测试覆盖?哪些路径完全没有测试?

**结论**:**不充分**。现有测试有 5 个会反向失败的用例、12 个完全未覆盖的路径、3 个不变量零覆盖。

---

## 1. 现有测试盘点

### 1.1 测试文件与规模

| 文件 | 行数 | 测试函数数 | 覆盖范围 |
|------|------|----------|---------|
| `platform/wecom/websocket_test.go` | 1746 | 49 | wecom 协议、assembler、stream queue、ack |
| `platform/wecom/wecom_test.go` | 238 | ~15 | Webhook 模式 |
| `platform/wecom/websocket_media_test.go` | 196 | ~8 | 媒体消息 |
| `core/streaming_test.go` | 441 | 14 | streamPreview 基础流程 |
| `core/engine_test.go` | 12695 | ~300 | engine 整体,其中流式相关 ~10 |
| `platform/wecom/testdata/stream_regressions.json` | - | 3 | a.log 回归 |

### 1.2 流式相关测试清单(现有 27 个)

#### A. assembler 单元测试(6 个,改造后全部失效)

| 测试名 | 验证什么 | 改造后状态 |
|--------|---------|-----------|
| `TestWSStreamAssembler_KeepsOnlyLatestPendingTool` | `ingest()` hold 最新工具块 | ❌ 失效:`ingest()` 被删除 |
| `TestWSStreamAssembler_FinalizeFlushesPendingTool` | `ingest()` finalize 冲刷 heldTool | ❌ 失效:`ingest()` 被删除 |
| `TestWSStreamAssembler_IngestDoesNotTreatToolPlusAnswerAsHeld` | `ingest()` 不把工具+正文当纯工具块 | ❌ 失效:`ingest()` 被删除 |
| `TestWSStreamAssembler_ShouldHoldOnlyPureToolBlock` | `shouldHoldOnlyTool()` 前缀判断 | ❌ 失效:函数被删除 |
| `TestSendStreamFrameAndWaitAck_AggregatesPendingToolPreview` | 工具 hold + 正文聚合 | ⚠️ 需重写:改为 `onToolStart`+`appendText` |
| `TestSendStreamFrameAndWaitAck_TextStillStreamsWhileToolIsHeld` | 工具 hold 期间正文仍流式 | ⚠️ 需重写:同上 |

#### B. stream queue 行为测试(8 个,部分需调整)

| 测试名 | 验证什么 | 改造后状态 |
|--------|---------|-----------|
| `TestSendStreamFrameAndWaitAck_SerializesConcurrentUpdates` | 并发串行发送 | ✅ 保留:串行语义不变 |
| `TestSendStreamFrameAndWaitAck_LatestWinsPendingPreview` | 最新覆盖 | ✅ 保留 |
| `TestSendStreamFrameAndWaitAck_DoesNotDuplicateLastAckedDuringAggregation` | 去重 | ⚠️ 需调整:`lastAcked`→`lastRendered` |
| `TestSendStreamFrameAndWaitAck_FinalizeSkipsDuplicatePartialPrefix` | finalize 去重 | ⚠️ 需调整 |
| `TestSendStreamFrameAndWaitAck_ToolPlusAnswerDoesNotCollapseToPreviousText` | 工具+正文不塌缩 | ⚠️ 需重写 |
| `TestSendStreamFrameAndWaitAck_FinishFlushesHeldTool` | finalize 冲刷 | ⚠️ 需重写 |
| `TestSendStreamFrameAndWaitAck_FinalizeDoesNotReplayLastAckedPrefix` | 不补前缀 | ✅ 保留:语义不变(只是变量改名) |
| `TestStreamRegressionsFromLogFixtures` | a.log 回归(3 个 case) | ⚠️ 需更新 fixture |

#### C. finalize/分片测试(4 个)

| 测试名 | 验证什么 | 改造后状态 |
|--------|---------|-----------|
| `TestReply_SendsFinalStreamFrame` | Reply 发 finish=true | ✅ 保留 |
| `TestFinalizePreviewMessage_UsesSameStreamIDAndFinishTrue` | finalize 同 streamID | ✅ 保留 |
| `TestFinalizePreviewMessage_LongContentSplitsIntoFollowUpMessages` | 长消息分片 | ⚠️ 需更新:改用 `splitSmart` |
| `TestReply_LongContentSplitsIntoFollowUpMessages` | Reply 长消息分片 | ⚠️ 需更新 |
| `TestUpdateMessage_LongContentUsesPreviewNotice` | preview 截断提示 | ✅ 保留 |

#### D. engine stream 模式集成测试(4 个,改造后部分反向失败)

| 测试名 | 验证什么 | 改造后状态 |
|--------|---------|-----------|
| `TestProcessInteractiveEvents_StreamModeMergesToolProgressIntoPreview` | 工具进度进 preview | ❌ **反向失败**:改造后工具进度不进 visibleText |
| `TestProcessInteractiveEvents_StreamModeToolHoldKeepsToolProgressInFinalReply` | 工具块保留在最终消息 | ❌ **反向失败**:改造后工具块从最终消息移除 |
| `TestProcessInteractiveEvents_StreamModeToolHoldSkipsToolProgressWhenToolMessagesDisabled` | ToolMessages=false 时无工具 | ✅ 保留:行为不变 |
| `TestProcessInteractiveEvents_ToolSegmentsKeepFinalFooter` | 工具段保留 footer | ⚠️ 需调整:stream 模式下 segmentStart 废弃 |

#### E. streamPreview 基础测试(14 个)

| 测试名 | 验证什么 | 改造后状态 |
|--------|---------|-----------|
| `TestStreamPreview_BasicFlow` | 基础 appendText+finish | ✅ 保留 |
| `TestStreamPreview_ThrottlesUpdates` | 节流 | ✅ 保留 |
| `TestStreamPreview_MaxChars` | 最大字符截断 | ✅ 保留 |
| `TestStreamPreview_Disabled` | 禁用 | ✅ 保留 |
| `TestStreamPreview_FinishInPlace` | in-place finalize | ✅ 保留 |
| `TestStreamPreview_FreezeDeletesOnFinish` | freeze 后 finalize | ⚠️ 需补充:改用 `snapshot()` |
| `TestStreamPreview_NonUpdaterPlatform` | 无 UpdateMessage 降级 | ✅ 保留 |
| `TestStreamPreview_DiscardDeletesPreview` | discard 删除 preview | ✅ 保留 |
| `TestStreamPreview_FinishKeepsPreviewWhenPlatformPrefersInPlaceFinalize` | KeepPreviewOnFinish | ✅ 保留 |
| `TestStreamPreview_NeedsDoneReaction_*`(3 个) | done emoji 反应 | ✅ 保留 |
| `TestStreamPreview_FinishPrefersPreviewFinalizer` | PreviewFinalizer 优先 | ✅ 保留 |
| `TestStreamPreview_AppliesTransform` | transform 应用 | ✅ 保留:transform 在出口 |

---

## 2. 测试缺口分析(按改造方案模块对照)

### 2.1 不变量覆盖矩阵(方案 3.2 节的 I1-I6)

| 不变量 | 含义 | 现有测试 | 缺口 |
|-------|------|---------|------|
| I1 | visibleText 只含 model 文本,无工具消息 | ❌ 无 | 需新增:`TestAppendText_DoesNotTouchProgressLines`、`TestOnToolStart_DoesNotTouchVisibleText` |
| I2 | progressLines 只由 onToolStart/Complete 写入,finish 后清空 | ❌ 无 | 需新增:`TestFinish_ClearsProgressLines`、`TestOnToolStart_AddsToProgressLines` |
| I3 | heldTool 只保留最后一个工具块 | ⚠️ `KeepsOnlyLatestPendingTool` 旧版有 | 需重写:`TestOnToolStart_OverwritesHeldTool` |
| I4 | render() 是只读操作 | ❌ 无 | 需新增:`TestRender_IsReadOnly` |
| I5 | finish() 后 progressLines=nil, heldTool="" | ❌ 无 | 需新增:`TestFinish_ClearsProgressLinesAndHeldTool` |
| I6 | 同一 stream_id 下 assembler 实例唯一 | ❌ 无 | 需新增:`TestStreamStateFor_ReturnsSameAssembler` |

**结论**:6 个不变量中,5 个零覆盖,1 个有旧版需重写。

### 2.2 改造模块覆盖矩阵(方案第 2.1 节的 18 个模块)

| # | 模块 | 现有测试 | 缺口严重度 |
|---|------|---------|-----------|
| 1 | 三区状态机 | ❌ 旧 `ingest` 测试全失效 | 🔴 高 |
| 2 | EventToolUse 不进 textParts | ❌ 旧测试断言工具进 preview | 🔴 高 |
| 3 | EventToolResult 不进 textParts | ❌ 旧测试断言工具结果进 preview | 🔴 高 |
| 4 | 删 mergeStreamDisplayContent | ❌ 无直接测试 | 🟡 中 |
| 5 | 删 lastAcked 补前缀 | ⚠️ 有 `FinalizeDoesNotReplayLastAckedPrefix` | 🟢 低 |
| 6 | heldTool 转 progressLines | ❌ 无 | 🔴 高 |
| 7 | 工具消息格式改单行 | ❌ 无 | 🟡 中 |
| 8 | 删 streamToolHoldNeedsAnswerSeparator | ❌ 无 | 🟡 中 |
| 9 | 长消息分片时机后移 | ⚠️ 有分片测试但未验证时机 | 🟡 中 |
| 10 | EventThinking 不进 preview | ⚠️ 有断言"thinking 不出现"但未测 stream 模式 | 🟡 中 |
| 11 | transform 保留在出口 | ✅ `AppliesTransform` 已覆盖 | 🟢 低 |
| 12 | freeze 用 snapshot | ⚠️ `FreezeDeletesOnFinish` 但未测 snapshot | 🟡 中 |
| 13 | silentHold 保留 engine | ⚠️ 只测纯函数,未测流式集成 | 🟡 中 |
| 14 | segmentStart stream 废弃 | ❌ 无 | 🟡 中 |
| 15 | 三套缓冲统一 | ❌ 无 | 🟡 中 |
| 16 | replyFooter 注入 | ✅ `ToolSegmentsKeepFinalFooter` | 🟢 低 |
| 17 | 多 turn 重建 sp | ❌ 无 | 🟡 中 |
| 18 | 不同 agent 事件粒度 | ❌ 无 | 🟡 中 |

### 2.3 效果场景覆盖矩阵(方案第 6 节的 4 个场景)

| 场景 | 现有测试 | 缺口 |
|------|---------|------|
| 场景一:工具+正文 | ⚠️ `StreamModeToolHoldKeepsToolProgressInFinalReply` 但断言相反 | ❌ 需重写为"工具不在最终消息" |
| 场景二:长消息 | ⚠️ 有分片测试但未测"截断提示不残留" | ❌ 需新增 |
| 场景三:多工具 | ❌ 无 | ❌ 需新增 |
| 场景四:工具中间态 | ⚠️ `AggregatesPendingToolPreview` 测了 hold,但未测"只显最终态" | ⚠️ 需补充 |

---

## 3. 完全未覆盖的路径(12 个)

以下是改造涉及但**现有测试完全没触及**的路径:

### 3.1 状态机层(6 个)

| # | 未覆盖路径 | 风险 | 建议测试 |
|---|-----------|------|---------|
| G1 | `appendText` 不触发 progressLines 变化 | 工具信息混入正文 | `TestAppendText_DoesNotTouchProgressLines` |
| G2 | `onToolStart` 不触发 visibleText 变化 | 正文丢失 | `TestOnToolStart_DoesNotTouchVisibleText` |
| G3 | `render()` 连续调用结果一致,不修改状态 | 幂等性破坏 | `TestRender_IsReadOnly_Idempotent` |
| G4 | `finish()` 清空 progressLines 和 heldTool | 工具残留 | `TestFinish_ClearsAllProgressState` |
| G5 | `onToolComplete` 追加 ✅ 行不进 visibleText | 工具结果污染正文 | `TestOnToolComplete_AddsCheckmarkNotVisibleText` |
| G6 | `maxProgressLines` 超限时 FIFO 丢弃 | 进度行无限增长 | `TestProgressLines_FIFO_BoundedByMax` |

### 3.2 engine 层(4 个)

| # | 未覆盖路径 | 风险 | 建议测试 |
|---|-----------|------|---------|
| G7 | `EventThinking`/`mapPlan` 在 stream 模式下不进 preview | thinking 污染正文 | `TestStreamMode_ThinkingDoesNotEnterPreview` |
| G8 | `silentHold` 在 stream 模式下延迟 appendText | NO_REPLY 前缀泄漏 | `TestStreamMode_SilentHoldDelaysAppendUntilNoReplyResolved` |
| G9 | `segmentStart` 在 stream 模式下不分段 | 分段发送破坏单消息语义 | `TestStreamMode_NoSegmentation` |
| G10 | 多工具时 `progressLines` 只保留最新 N 行 | 工具刷屏 | `TestStreamMode_MultiTool_OnlyLatestProgressVisible` |

### 3.3 集成/边界层(2 个)

| # | 未覆盖路径 | 风险 | 建议测试 |
|---|-----------|------|---------|
| G11 | `freeze()`+`detachPreview()` 后新 preview 正确重建 | 权限提示后 preview 断裂 | `TestFreeze_ThenDetach_NewPreviewRebuilds` |
| G12 | 长消息 finalize 后截断提示不残留 | 截断提示污染最终消息 | `TestFinalize_LongMessage_TruncationNoticeNotResidual` |

---

## 4. 会反向失败的现有测试(5 个)

这些测试断言的是**改造前的行为**,改造后行为变了,必须更新:

| 测试名 | 旧断言 | 新断言 | 改造影响 |
|--------|-------|-------|---------|
| `TestProcessInteractiveEvents_StreamModeMergesToolProgressIntoPreview` | preview 含 `Tool #1` | preview 不含 `Tool #1`(在 progressLines) | 🔴 核心行为变更 |
| `TestProcessInteractiveEvents_StreamModeToolHoldKeepsToolProgressInFinalReply` | 最终消息含 `Tool #1`+`42 /tmp/agent.json` | 最终消息不含工具块 | 🔴 核心行为变更 |
| `TestWSStreamAssembler_KeepsOnlyLatestPendingTool` | `ingest()` hold 行为 | `onToolStart()` hold 行为 | 🔴 接口变更 |
| `TestWSStreamAssembler_FinalizeFlushesPendingTool` | `ingest()` finalize | `finish()` | 🔴 接口变更 |
| `TestWSStreamAssembler_ShouldHoldOnlyPureToolBlock` | `shouldHoldOnlyTool()` 前缀判断 | 函数删除 | 🔴 接口变更 |

**TDD 视角**:这 5 个测试是"行为契约"。改造前应先**更新这些测试**表达新契约,再改实现让它通过。这就是 TDD 的 Red-Green 循环。

---

## 5. TDD 落地顺序

### 5.1 Red-Green-Refactor 循环

TDD 的核心是:先写失败测试(Red),再实现使其通过(Green),再重构(Refactor)。

```
阶段一:状态机(纯单元测试,最快见效)
  Red:  写 I1-I6 不变量测试 + G1-G6 路径测试 → 全部失败(函数不存在)
  Green:实现 wecomStreamAssembler 三区 + snapshot → 测试通过
  Refactor:删除旧 ingest/shouldHoldOnlyTool → 旧测试清理

阶段一.5:旧测试更新
  Red:  更新 5 个反向失败测试的断言 → 失败(实现还没改)
  (此阶段不 Green,等阶段二实现后才通过)

阶段二:engine 解耦
  Red:  写 G7-G10 路径测试 → 失败
  Green:改 engine_turn.go → 测试通过
  Refactor:删 mergeStreamDisplayContent、streamToolHoldNeedsAnswerSeparator

阶段二.5:freeze/detach 适配
  Red:  写 G11 测试 → 失败
  Green:实现 snapshot() + 改 freeze() → 通过

阶段三:发送层瘦身
  Red:  更新 lastAcked 相关测试的变量名 → 编译失败
  Green:改 lastAcked→lastRendered → 通过
  Refactor:删补前缀逻辑

阶段四:切分算法
  Red:  写 splitSmart 测试(代码块/中文/混合) → 失败
  Green:实现 splitSmart(或复用 SplitMessageCodeFenceAware) → 通过

阶段五:集成回归
  Red:  写 G12 + 场景一/二/三测试 → 失败
  Green:整体调通 → 通过
  回归:a.log fixture 更新
```

### 5.2 测试优先级(按风险排序)

| 优先级 | 测试 | 理由 |
|-------|------|------|
| P0 | I1-I6 不变量(6 个) | 状态机正确性的基线,其他都依赖它 |
| P0 | 5 个反向失败测试更新 | 不更新就无法验证改造生效 |
| P1 | G1-G6 状态机路径(6 个) | 三区分离的核心验证 |
| P1 | G10 多工具、G12 截断残留 | 用户最常感知的混乱场景 |
| P2 | G7-G9 engine 路径(3 个) | thinking/silent/segment 边界 |
| P2 | G11 freeze 重建 | 权限提示路径 |
| P3 | 场景一/二/三/四集成(4 个) | 端到端效果验证 |
| P3 | a.log fixture 更新 | 回归保护 |

---

## 6. 测试基础设施缺口

### 6.1 mock 平台不足

现有 mock:
- `mockUpdaterPlatform`:实现 UpdateMessage + PreviewStarter + FinalizePreviewMessage
- `mockKeepPreviewPlatform`:上面 + KeepPreviewOnFinish + StreamPreviewMode
- `mockCleanerPlatform`:上面 + DeletePreviewMessage

**缺口**:没有 mock 实现 `ProgressAssembler` 接口(OnToolStart/OnToolComplete)。engine 集成测试无法验证 `sp.onToolStart()` 是否被正确调用。

**建议**:扩展 `mockKeepPreviewPlatform` 实现 `ProgressAssembler`,记录 `onToolStart`/`onToolComplete` 调用。

### 6.2 assembler 独立测试缺失

现有 assembler 测试都耦合在 `websocket_test.go` 里,通过 `sendStreamFrameAndWaitAck` 间接测试。

**缺口**:没有直接测 `wecomStreamAssembler` 的纯单元测试。

**建议**:新增 `websocket_stream_assembler_test.go`,纯函数测试,不依赖 WebSocket mock。

### 6.3 fixture 覆盖不足

`stream_regressions.json` 只有 3 个 case,都是 `a.log` 的纯文本流式场景。

**缺口**:没有工具 hold + 正文、多工具、截断提示+finalize 的 fixture。

**建议**:补充 4 个 fixture:
1. `tool_hold_then_text_aggregates`
2. `multi_tool_only_latest_progress`
3. `long_message_finalize_no_truncation_residue`
4. `tool_intermediate_state_not_shown`

---

## 7. 现有可复用的测试资产

### 7.1 `SplitMessageCodeFenceAware` 已有测试

`core/markdown_html_test.go` 已测试代码块感知切分(`TestSplitMessageCodeFenceAware_PreservesCodeBlock` 等)。

**建议**:阶段四的 `splitSmart` 可以直接复用 `SplitMessageCodeFenceAware`,不需要新写。只需在 wecom 侧把 `splitByBytes` 调用替换为 `SplitMessageCodeFenceAware`,补一个集成测试。

### 7.2 `couldBeSilentPrefix` 纯函数测试完备

`core/engine_test.go:11804` 的 `TestCouldBeSilentPrefix` 覆盖了 12 种边界。

**缺口**:只测了纯函数,没测"流式时 silentHold 是否正确延迟 appendText"。

### 7.3 `splitByBytes` UTF-8 安全测试

`platform/wecom/websocket_test.go:71` 的 `TestSplitByBytes_UTF8NeverSplitsMidRune` 已验证 UTF-8 安全。

**建议**:`splitSmart` 替换后保留这个测试,补充代码块边界用例。

---

## 8. 测试充分性评估

### 8.1 当前状态

| 维度 | 覆盖率 | 说明 |
|------|-------|------|
| 旧 assembler 行为 | ~70% | `ingest`/`shouldHoldOnlyTool` 有测试 |
| 新 assembler 不变量 | 0% | I1-I6 全无 |
| 新 assembler 路径 | 0% | G1-G6 全无 |
| engine stream 集成 | ~30% | 4 个测试,2 个反向失败 |
| freeze/detach 路径 | ~10% | 只有 1 个 freeze 测试 |
| silentHold 流式集成 | 0% | 只有纯函数测试 |
| 多 turn 场景 | 0% | 无 |
| 切分算法(代码块) | 0% | `SplitMessageCodeFenceAware` 有测试但 wecom 没用 |

### 8.2 改造后目标状态

| 维度 | 目标覆盖率 | 关键测试 |
|------|----------|---------|
| 新 assembler 不变量 | 100% | I1-I6(6 个) |
| 新 assembler 路径 | 100% | G1-G6(6 个) |
| engine stream 集成 | 90% | 5 个反向更新 + G7-G10(4 个) |
| freeze/detach | 80% | G11 + 旧 freeze 更新 |
| silentHold 流式 | 50% | G8(1 个,边界场景难全覆盖) |
| 多 turn | 50% | 1 个基础测试 |
| 切分算法 | 80% | 复用 CodeFenceAware + 2 个 wecom 集成 |
| 回归 fixture | 100% | 3 旧 + 4 新 = 7 个 |

### 8.3 风险评估

| 风险 | 严重度 | 缓解 |
|------|-------|------|
| 改了实现但测试没更新,误以为通过 | 🔴 高 | 先更新 5 个反向测试(Red),再改实现(Green) |
| 不变量 I1-I6 未测,三区分离静默失效 | 🔴 高 | 阶段一优先写 I1-I6 |
| freeze 路径回归(权限提示后 preview 断裂) | 🟡 中 | G11 集成测试 |
| 多 agent 事件粒度差异未测 | 🟡 中 | 至少覆盖 ACP + claudecode 两种 |
| 切分代码块时 fence 闭合错误 | 🟡 中 | 复用 `SplitMessageCodeFenceAware` 已有测试 |

---

## 9. 建议的新增测试清单(完整)

### 9.1 状态机不变量(6 个,P0)

```
TestWecomStreamAssembler_AppendText_DoesNotTouchProgressLines    (I1)
TestWecomStreamAssembler_OnToolStart_DoesNotTouchVisibleText     (I1)
TestWecomStreamAssembler_OnToolStart_OverwritesHeldTool          (I3)
TestWecomStreamAssembler_Finish_ClearsProgressLinesAndHeldTool   (I5)
TestWecomStreamAssembler_Render_IsReadOnly_Idempotent            (I4)
TestWecomStreamAssembler_StreamStateFor_ReturnsSameInstance      (I6)
```

### 9.2 状态机路径(6 个,P1)

```
TestWecomStreamAssembler_OnToolStart_AddsToProgressLines         (G1, G2)
TestWecomStreamAssembler_OnToolComplete_AddsCheckmarkLine         (G5)
TestWecomStreamAssembler_ProgressLines_FIFO_BoundedByMax          (G6)
TestWecomStreamAssembler_Render_ProgressAndTextSeparated           (G3)
TestWecomStreamAssembler_Snapshot_DoesNotMutateState               (G3)
TestWecomStreamAssembler_Discard_ClearsAll                          (I5 补充)
```

### 9.3 engine 集成(4 个,P2)

```
TestProcessInteractiveEvents_StreamMode_ThinkingDoesNotEnterPreview        (G7)
TestProcessInteractiveEvents_StreamMode_SilentHoldDelaysAppend             (G8)
TestProcessInteractiveEvents_StreamMode_NoSegmentation                     (G9)
TestProcessInteractiveEvents_StreamMode_MultiTool_OnlyLatestProgress       (G10)
```

### 9.4 freeze/边界(2 个,P2)

```
TestStreamPreview_FreezeUsesSnapshot_NotFullText              (G11)
TestStreamPreview_FreezeThenDetach_NewPreviewRebuilds         (G11)
```

### 9.5 finalize/分片(2 个,P3)

```
TestFinalize_LongMessage_TruncationNoticeNotResidual           (G12)
TestFinalize_CodeBlock_FenceClosedAndReopened                  (切分)
```

### 9.6 回归 fixture(4 个,P3)

```
fixture: tool_hold_then_text_aggregates
fixture: multi_tool_only_latest_progress
fixture: long_message_finalize_no_truncation_residue
fixture: tool_intermediate_state_not_shown
```

### 9.7 反向更新(5 个,P0)

```
更新 TestProcessInteractiveEvents_StreamModeMergesToolProgressIntoPreview
更新 TestProcessInteractiveEvents_StreamModeToolHoldKeepsToolProgressInFinalReply
替换 TestWSStreamAssembler_KeepsOnlyLatestPendingTool → OnToolStart 版本
替换 TestWSStreamAssembler_FinalizeFlushesPendingTool → finish 版本
删除 TestWSStreamAssembler_ShouldHoldOnlyPureToolBlock
```

**总计**:新增 22 个测试 + 更新 5 个 + 新增 4 个 fixture。

---

## 10. TDD 执行检查清单

每个阶段开始前确认:

- [ ] Red:先写/更新测试,确认失败
- [ ] Green:改实现,确认测试通过
- [ ] Refactor:清理旧代码,确认测试仍通过
- [ ] 覆盖率:新增路径有测试
- [ ] 回归:旧测试全绿(除反向更新的 5 个)

每个阶段结束前确认:

- [ ] `go test ./platform/wecom ./core -run '<模块>'` 全绿
- [ ] `go vet ./...` 无警告
- [ ] 不变量 I1-I6 相关测试全绿(阶段一后)

---

## 11. 结论

**现有测试不充分**。核心问题:

1. **6 个不变量零覆盖** — 三区分离的正确性无法验证
2. **5 个测试会反向失败** — 改造后行为变更,测试仍是旧契约
3. **12 个路径完全未测** — freeze 重建、多工具、截断残留等
4. **assembler 无独立单测** — 都耦合在 WebSocket 集成测试里

**TDD 改造路径**:按阶段一→一.五→二→二.五→三→四→五的顺序,每阶段先 Red(写失败测试)再 Green(实现)。阶段一的 6 个不变量测试是整个改造的基线,**必须最先写**。

**关键原则**:改造不是"改代码后跑测试看有没有挂",而是"先写表达新行为的测试,再改代码让它通过"。
