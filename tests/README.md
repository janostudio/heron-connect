# tests 测试设计说明

本文档说明 `tests/` 目录下测试用例的整体分层、设计目标和维护方式。这个项目的测试不是按单一维度组织，而是按“反馈速度 + 依赖成本 + 真实性”分层：越往上越快、越确定；越往下越接近真实运行环境。

## 1. 总体思路

测试体系主要解决四类问题：

- 低成本快速回归：用 fake / mock 覆盖核心流程，保证日常改动不会破坏基础行为。
- 真实组件联动：用真实 agent 二进制和真实配置验证 Engine 与外部 agent 适配层是否还能工作。
- 发布前合同校验：把最容易回归的用户可见行为固化成 deterministic contract test，避免“逻辑没报错但输出变了”。
- 性能基线：对热点路径保留基准测试，观察吞吐和延迟变化。

因此，`tests/` 不是单一的“集成测试目录”，而是一个分层测试集合。

当前目录大致可以理解为：

| 目录 | 目标 | 依赖 | 典型特征 |
| --- | --- | --- | --- |
| `tests/mocks` | 提供测试替身 | 无外部依赖 | `MockAgent`、`MockPlatform`、`FakeAgentSession` 等复用组件 |
| `tests/e2e` | 快速 smoke / regression | 主要依赖 fake/mock | 用 build tag 区分 `smoke` 和 `regression` |
| `tests/integration` | 组件级和真实 agent 联动 | 可能需要 CLI、API key、`config.test.toml` | 更接近真实运行路径，缺条件时 `Skip` |
| `tests/release_local` | 本地可重复的发布门禁 | 无外部账号、无 IM 平台依赖 | 对输出合同、配置矩阵、附件链路做精确断言 |
| `tests/performance` | 基准测试 | 无外部平台依赖 | 通过 benchmark 观察延迟、吞吐、资源开销 |

从当前规模看，`tests/` 下大约包含：

- `tests/e2e`: 44 个测试
- `tests/integration`: 56 个测试
- `tests/release_local`: 28 个测试
- `tests/performance`: 14 个 benchmark

## 2. 分层设计

### 2.1 `tests/mocks`: 统一测试替身层

这一层不直接验证业务，而是给其他测试层提供可组合的替身对象。

主要分两类：

- `tests/mocks/mock_agent.go`、`tests/mocks/mock_platform.go`
  - 基于 `testify/mock`，适合做“我只关心有没有调用、传参对不对”的测试。
  - 常见于 `tests/e2e` 和部分 `tests/integration` 的轻量联动验证。
- `tests/mocks/fake/*`
  - 提供可运行的 fake agent / fake session / fake message / fake response。
  - 适合模拟事件流、会话生命周期、超时、权限请求等“要真的跑一段逻辑”的场景。

这里的设计重点是：

- 把测试替身集中维护，避免每个测试文件重复造轮子。
- 区分 mock 和 fake 的职责。
  - mock 更适合断言“被调用了什么”。
  - fake 更适合驱动“系统如何响应一串事件”。

### 2.2 `tests/e2e`: 低成本广覆盖的 smoke / regression

这个目录名字叫 `e2e`，但实际上更接近“高层回归测试”。它强调的是覆盖系统主路径，而不一定要求每一层都是真实外部依赖。

#### `smoke_test.go`

通过 `//go:build smoke` 控制，只做快速健康检查，目标是“几分钟内告诉你项目有没有明显坏掉”。

覆盖点包括：

- 配置加载与非法配置拒绝
- agent / platform 工厂注册和最小初始化
- session 基本生命周期
- 基础消息流和事件类型
- command registry / command parsing
- workspace 切换、rate limiter、markdown、webhook 等基础能力

这一层的特点是：

- 不追求完全真实，只追求主干功能是否还能跑通。
- 用 fake agent 和 mock platform 保持速度和稳定性。
- 适合放“每次提交都应该很快验证”的内容。

#### `regression_test.go`

通过 `//go:build regression` 控制，目标是把历史上容易回归的关键路径固定下来。

覆盖点明显更宽，包括：

- 完整消息管线
- 并发 agent / timeout / graceful shutdown
- 权限模式、secret / token redact、命令注入防护
- rate limit、streaming response、session CRUD / history / persistence
- card / button / cron / config reload / atomic write / dedup
- workspace isolation 以及部分平台相关行为

这一层的设计意图是：

- 不把所有行为都塞进单元测试，而是保留一组“高价值回归样本”。
- 测试名直接反映风险点，便于在发布前快速扫描关键回归面。
- 仍然尽量避免真实外部依赖，保证可重复执行。

### 2.3 `tests/integration`: 真实联动与特殊交互场景

这一层统一用 `//go:build integration` 控制。和 `tests/e2e` 的核心区别是：这里更强调真实组件之间的边界，而不是只验证抽象流程。

这一层实际上又分成三种子类型。

#### A. 真实 agent CLI 联动

代表文件：

- `tests/integration/agent_integration_test.go`
- `tests/integration/e2e_session_test.go`
- `tests/integration/filter_sessions_test.go`
- `tests/integration/e2e_helpers_test.go`

设计方式：

- 用真实 `core.CreateAgent(...)` 创建 agent 适配器。
- 通过 `findAgentBin()`、`exec.LookPath()` 检查二进制是否在 PATH 中。
- 缺少 CLI、API Key、`config.test.toml` 时直接 `t.Skip(...)`，而不是制造伪失败。
- 平台侧仍然使用 mock / recording platform，避免真实 IM 平台依赖。

这批测试主要验证：

- `heron-connect` 对真实 agent 会话的创建、切换、列出、删除和持久化
- `/provider switch` 等需要真实 provider wiring 的行为
- Claude Code / Codex 的会话文件扫描与 external session filtering
- 不同 agent 在统一 Engine 中的行为是否一致

特别要注意：

- `tests/integration/agent_integration_test.go` 已经接近“真实对话回路测试”。
- `tests/integration/e2e_session_test.go` 才是最接近真正 E2E 的一组测试，因为它需要真实配置和真实 agent 响应。
- `tests/e2e` 和 `tests/integration/e2e_session_test.go` 的“e2e”含义不同，前者偏回归分层，后者偏真实环境联动。

#### B. Engine 级组件联动

代表文件：

- `tests/integration/engine_platform_test.go`

这类测试不依赖真实外部服务，但会把多个真实组件一起拼起来，例如：

- Engine 与 Platform 的消息流
- 多 agent session 协调
- rate limiter、dedup、command registry、role manager
- cron store、card rendering、attachment handling

它的定位是：

- 高于单元测试，低于真实外部依赖测试。
- 重点验证“组件之间接口接起来后还能不能按预期工作”。

#### C. 复杂状态机 / 特殊历史 bug 场景

代表文件：

- `tests/integration/unsolicited_events_test.go`
- `tests/integration/multi_workspace_shared_test.go`

这两类测试的共同点是：

- 场景复杂，单元测试难表达。
- 需要真实 Engine 状态流转，但不需要真实外部平台。
- 经常会自己定义最小 fake session / fake platform，以便精准控制事件时序。

例如：

- `unsolicited_events_test.go`
  - 专门测试 agent 在用户回合结束后继续发事件时，Engine 如何处理“非请求触发”的背景事件。
  - 核心风险是 stale event 被错误重放、错误归因、或者在下一轮被 drain 掉。
- `multi_workspace_shared_test.go`
  - 专门测试多项目共享 workspace binding 的同步与覆盖规则。
  - 核心风险是不同 project / channel / shared route 之间串线。

这一层本质上是在补“跨回合、跨项目、跨状态”的集成状态机测试。

### 2.4 `tests/release_local`: 本地可重复的发布门禁

这是整个 `tests/` 目录里最有工程价值的一层之一。它不依赖真实 IM 平台、不依赖真实 provider 账号，但覆盖的都是发布前非常容易回归、而且用户能直接感知的行为。

这一层没有 build tag，意味着它是 deterministic 的，适合在本地和 CI 中稳定执行。

当前拆成四个子包。

#### `release_local/config_matrix`

核心目标：验证配置解析、默认值、覆盖关系、非法值 fail-fast。

覆盖方式：

- 每个测试动态写一个最小 TOML 文件
- 直接 `config.Load()`
- 精确断言 project override 是否覆盖 global config
- 精确断言 invalid config 的错误文案是否包含预期信息

适合放：

- 默认值
- 继承/覆盖优先级
- 配置合法性校验

#### `release_local/engine_matrix`

核心目标：验证 `ReceiveMessage` 入口下命令和普通消息的分流矩阵。

覆盖方式：

- 自定义 `matrixAgent`、`matrixSession`、`matrixPlatform`
- 通过记录 prompt 和输出文本，验证 Engine 在不同命令输入下的路由行为

已覆盖的重点包括：

- session 生命周期命令
- alias
- disabled command
- banned words
- custom prompt command
- unknown slash command 的降级行为

适合放“命令入口行为是否改变了”的回归。

#### `release_local/media_pipeline`

核心目标：验证附件在 Engine 内部的完整流转，不让图片/文件在排队、转发、回显中丢失或重复。

覆盖方式：

- 自定义 recording agent / session
- 记录 `Send(prompt, images, files)` 实际收到的参数
- 同时观察平台侧收到的文本、图片、文件输出

已覆盖的重点包括：

- inbound images/files 是否能到 agent
- attachment-only message 是否也能触发处理
- queued message 是否保留文件
- `SendToSessionWithAttachments` 的文本/图片/文件输出合同
- 禁用附件发送时的行为
- 多 session 场景下附件发送的约束

#### `release_local/turn_contract`

核心目标：把“一个用户回合从输入到最终展示”的外部合同固定下来。

这是发布门禁里最细的一层，关注的不是内部实现，而是用户最终会看到什么。

覆盖方式：

- 自定义 `turnAgent`、`turnSession`、`turnPlatform`
- 精确控制 event 序列：`thinking`、`tool_use`、`tool_result`、`permission_request`、`result`
- 检查平台最终文本、按钮、metadata、stream preview、card mode 等输出是否符合合同

已覆盖的重点包括：

- 文本 / 图片 / 文件输入的统一回合合同
- side-channel echo 与 final reply 的去重规则
- thinking / tool event 的显示规则
- hidden tool event 的隐藏规则
- permission interaction 在阻塞发送时的处理
- stream preview 的 finalize、truncate、配置矩阵
- reply metadata 与 display visibility 配置矩阵
- rich card 模式下工具步骤和最终元信息是否在一个 card 中保留

如果某个 bug 的本质是“最终用户看到的输出变了”，通常应该优先补到这里，而不是补到更松散的 smoke test。

### 2.5 `tests/performance`: 性能基准层

通过 `//go:build performance` 控制，全部是 benchmark。

当前 benchmark 关注的对象主要是热点基础设施：

- 单消息延迟
- 并发吞吐
- session switch / session creation / send-receive
- rate limiter / concurrent rate limiter
- message dedup
- command registry lookup
- card rendering
- cron store operations
- multi-agent coordination

这一层不是功能正确性测试，而是提供性能趋势基线。

## 3. 关键设计原则

### 3.1 先分层，再决定依赖成本

这个项目不会把所有事情都扔进“真实集成测试”。判断标准通常是：

- 纯逻辑 / 输出合同问题：优先放 `release_local`
- 高层回归样本：优先放 `tests/e2e`
- 真实 agent 适配、会话文件读取、provider wiring：放 `tests/integration`
- 性能问题：放 `tests/performance`

这样做的好处是：

- 大部分问题可以在 deterministic 测试里快速暴露。
- 只有确实需要真实二进制和真实配置时，才进入 integration 层。

### 3.2 尽量避免真实平台依赖

即使是 integration 测试，也通常只把 agent 变成“真实”，而把 platform 保持为 mock / capture / record 平台。这样能验证 Engine 到 agent 的关键链路，同时避免 Telegram、Feishu、Discord 之类平台网络依赖带来的不稳定性。

### 3.3 外部条件不足时优先 `Skip`

真实集成测试的策略很明确：

- 没有 agent CLI: `Skip`
- 没有 API Key: `Skip`
- 没有 `config.test.toml`: `Skip`

原因不是放松要求，而是区分“环境不具备”与“代码真的坏了”。这能让 CI 信号更干净。

### 3.4 断言以用户可见合同为中心

很多测试并不直接断言内部字段，而是断言：

- agent 最终收到的 prompt 是什么
- platform 最终看到了哪些文本 / 图片 / 文件
- `/list`、`/switch`、`/delete` 这些命令对用户输出了什么

这说明测试设计更偏向黑盒/灰盒，而不是死盯内部实现细节。这样在重构时更稳定。

### 3.5 复杂时序自己造最小状态机

对于 unsolicited event、workspace live sync、stream preview、permission gate 这类复杂问题，测试会直接在测试文件里定义最小可控的 fake agent/session/platform，而不是强依赖通用 fake。这种做法的目的，是让每个复杂场景都能完全控制时序和状态变化。

## 4. 运行入口

`Makefile` 已经把主要测试入口分好了层级。

常用命令：

```bash
make test-fast
make test-full
make test-smoke
make test-e2e
make test-release-local
make test-performance
```

它们背后的测试意图分别是：

- `make test-fast`
  - 单元测试 + `smoke`
  - 用于快速反馈
- `make test-full`
  - 单元测试 + `smoke` + `regression`
  - 用于 PR 级别回归
- `make test-release-local`
  - deterministic 的发布前门禁
  - 重点检查 config、core 某些关键用例、Feishu 局部行为和 `tests/release_local`
- `make test-performance`
  - benchmark 基线

如果要手动运行 integration 层，需要显式加 tag：

```bash
go test -tags=integration ./tests/integration/...
```

如果要运行 smoke / regression / performance：

```bash
go test -tags=smoke ./tests/e2e/...
go test -tags=regression ./tests/e2e/...
go test -bench=. -benchmem -tags=performance ./tests/performance/...
```

## 5. 新增测试时的落点建议

可以按下面的判断方式选择目录：

| 你要验证的问题 | 建议放置位置 |
| --- | --- |
| 配置默认值、覆盖规则、非法值 | `tests/release_local/config_matrix` |
| `/new`、`/list`、alias、banned words、slash command 路由 | `tests/release_local/engine_matrix` |
| 图片/文件在 Engine 和 platform 间的流转 | `tests/release_local/media_pipeline` |
| thinking/tool/preview/card/final reply 的展示合同 | `tests/release_local/turn_contract` |
| 轻量高层回归样本 | `tests/e2e` |
| 需要真实 agent CLI 或真实 provider wiring | `tests/integration` |
| 性能和吞吐变化 | `tests/performance` |

一个简单原则是：

- 能 deterministic 地测，就不要先上 integration。
- 只有 deterministic 测不了真实风险时，再用真实 agent 测。

## 6. 结论

这个项目的 `tests/` 设计不是“单元测试之外全都叫集成测试”，而是明确做了分层：

- `mocks` 负责复用测试替身
- `e2e` 负责快速 smoke 和回归样本
- `integration` 负责真实 agent / 复杂联动
- `release_local` 负责 deterministic 发布合同
- `performance` 负责性能基线

整体上，这套设计追求的是三件事：

- 日常改动能快速获得反馈
- 关键用户行为能被稳定锁定
- 真实外部依赖只在必要时才引入

如果后续要继续扩展测试，最重要的不是“再多加一个测试”，而是把新问题放到正确的层里，这样整个测试体系才会持续可维护。
