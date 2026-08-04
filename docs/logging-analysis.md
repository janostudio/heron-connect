# cc-connect-qhn 日志方案分析与优化建议

## 一、当前日志方案

### 1.1 日志库

使用 Go 标准库 `log/slog`，零第三方依赖。

### 1.2 日志级别

| 级别 | 配置值 | 使用场景 |
|------|--------|----------|
| debug | `"debug"` | 消息路由、授权判断、文件下载、WebSocket 连接细节 |
| info | `"info"`（默认） | 启动/关闭、配置加载、功能启用、命令执行 |
| warn | `"warn"` | 配置不完整、API 重试、功能降级 |
| error | `"error"` | 致命错误、API 调用失败、启动失败 |

### 1.3 输出方式

| 方式 | 说明 |
|------|------|
| stdout | 默认，纯文本 `TextHandler` |
| 文件 | daemon 模式，路径 `~/.cc-connect-qhn/logs/cc-connect-qhn.log` |
| 日志轮转 | 自实现 `RotatingWriter`，单文件最大 10MB，保留 1 个 `.1` 备份 |
| 审计日志 | 企微 `~/.cc-connect-qhn/audit/wecom_access/{project}.jsonl`（JSON Lines） |
| 飞书 SDK 日志 | `sanitizingLogger` 包装，自动脱敏 URL 敏感参数 |

### 1.4 配置入口

```toml
[log]
level = "info"
```

- 启动时通过 `setupLogger(cfg.Log.Level, logWriter)` 初始化
- Management API 支持 `log_level` 热更新
- daemon 模式额外支持环境变量：`CC_LOG_FILE`、`CC_LOG_MAX_SIZE`

### 1.5 日志格式示例

```
time=2025-07-29T10:00:00.000Z level=INFO msg="cc-connect-qhn is running" projects=2
```

---

## 二、现有问题分析

### 2.1 缺少远程日志上报

| 问题 | 影响 |
|------|------|
| 日志仅在本地，多实例部署时无法集中查看 | 排查问题需要逐个登录机器 |
| 无持久化保障，磁盘故障日志丢失 | 安全审计不可追溯 |
| 无聚合分析能力 | 无法统计错误率、响应时间趋势等 |

### 2.2 纯文本格式不利于采集

当前 `TextHandler` 输出的 `key=value` 格式：

- 无标准 schema，日志采集器（Promtail/Fluentd）需要额外解析规则
- 嵌套结构（如 JSON 字段）在纯文本中无法表达
- 日志级别、时间戳字段名不标准（`time=` vs 标准 `timestamp`）

### 2.3 结构化字段使用不统一

各模块使用 `slog.With()` 传递的 key 没有统一规范：

```go
// 各模块风格不统一
logger.With("project_id", id)     // snake_case
logger.With("projectId", id)      // camelCase
logger.With("project", name)      // 缩写
```

### 2.4 缺少日志采样

高频操作（如 WebSocket 心跳、消息路由）产生大量重复日志：

- 可能快速占满 10MB 日志文件
- 干扰关键信息的发现
- 无 `slog` 级别的采样/限流机制

### 2.5 缺少链路追踪 ID

- 无 trace ID 贯穿请求链路
- 跨模块（engine → platform → observer）排查困难
- 无法关联 Claude Code 会话与平台消息

---

## 三、优化方案

### 3.1 远程日志上报（推荐 Grafana Loki）

#### 为什么选 Loki

| 对比维度 | Loki | ELK | SigNoz |
|----------|------|-----|--------|
| 部署复杂度 | 低（单二进制） | 高（Java 重资源） | 中 |
| 资源占用 | 低 | 高 | 中 |
| 免费额度 | Grafana Cloud 50GB/14天 | 无 | 自托管免费 |
| Go 生态 | 原生支持 | 需 Filebeat | 需 Collector |
| 与 Grafana 集成 | 原生 | 需配置 | 原生 |

#### 接入方式

```
cc-connect-qhn → JSON 日志文件 → Promtail → Loki → Grafana
```

##### Step 1：切换到 JSON 格式输出

```go
// 改动点：cmd/cc-connect-qhn/main.go setupLogger
func setupLogger(level slog.Leveler, w io.Writer) {
    opts := &slog.HandlerOptions{
        Level:     level,
        AddSource: true, // 保留调用位置
    }
    handler := slog.NewJSONHandler(w, opts)
    logger := slog.New(handler)
    slog.SetDefault(logger)
}
```

输出变为：

```json
{
  "time": "2025-07-29T10:00:00.000Z",
  "level": "INFO",
  "msg": "cc-connect-qhn is running",
  "source": {"function": "main", "file": "cmd/main.go", "line": 42},
  "projects": 2
}
```

##### Step 2：统一结构化字段规范

定义标准 key，所有模块统一使用：

```go
package logkeys

const (
    ProjectID  = "project_id"
    Platform   = "platform"
    SessionID  = "session_id"
    UserID     = "user_id"
    TraceID    = "trace_id"
    Command    = "command"
    Duration   = "duration_ms"
    StatusCode = "status_code"
)
```

##### Step 3：接入 Trace ID

在请求入口生成 trace ID，通过 context 传递：

```go
import "github.com/google/uuid"

func WithTraceID(ctx context.Context) context.Context {
    traceID := uuid.New().String()
    return context.WithValue(ctx, logkeys.TraceID, traceID)
}

func Logger(ctx context.Context) *slog.Logger {
    traceID, _ := ctx.Value(logkeys.TraceID).(string)
    return slog.Default().With(logkeys.TraceID, traceID)
}
```

##### Step 4：部署 Promtail

```yaml
# promtail-config.yaml
server:
  http_listen_port: 9080

clients:
  - url: http://localhost:3100/loki/api/v1/push  # 或 Grafana Cloud 地址

scrape_configs:
  - job_name: cc-connect-qhn
    static_configs:
      - targets:
          - localhost
        labels:
          job: cc-connect-qhn
          __path__: /Users/*/.cc-connect-qhn/logs/*.log
    pipeline_stages:
      - json:
          expressions:
            level: level
            message: msg
            timestamp: time
      - labels:
          level:
      - timestamp:
          source: timestamp
          format: RFC3339
```

##### Step 5：Grafana 查询

```logql
{job="cc-connect-qhn"} |= "error"
{job="cc-connect-qhn"} | json | level="ERROR" | line_format "{{.msg}}"
{job="cc-connect-qhn"} | json | rate(counter) [5m] by (level)
```

### 3.2 日志采样

高频路径增加采样控制：

```go
type SamplingHandler struct {
    slog.Handler
    interval time.Duration
    lastLog  map[string]time.Time
    mu       sync.Mutex
}

func (h *SamplingHandler) Handle(ctx context.Context, r slog.Record) error {
    if r.Level < slog.LevelWarn {
        key := r.Message
        h.mu.Lock()
        if last, ok := h.lastLog[key]; ok && time.Since(last) < h.interval {
            h.mu.Unlock()
            return nil // 采样丢弃
        }
        h.lastLog[key] = time.Now()
        h.mu.Unlock()
    }
    return h.Handler.Handle(ctx, r)
}
```

### 3.3 审计日志增强

当前企微审计日志（JSONL）可进一步扩展：

```json
{
  "timestamp": "2025-07-29T10:00:00.000Z",
  "trace_id": "uuid",
  "user_id": "wecom_user_123",
  "project_id": "my-project",
  "action": "message_sent",
  "authorized": true,
  "rule": "allow_all",
  "duration_ms": 150,
  "message_length": 2048
}
```

---

## 四、方案对比总览

| 方案 | 改动量 | 收益 | 适用场景 |
|------|--------|------|----------|
| **仅切换 JSON 格式** | 极小（1 行改动） | 日志可被采集器解析 | 准备接入采集器 |
| **+ 统一字段规范** | 小（各模块改 slog.With 调用） | 查询体验提升 | 已有 Grafana/Loki |
| **+ 接入 Loki** | 中（部署 Promtail + Loki） | 远程查看、聚合分析 | 多实例、长期运行 |
| **+ Trace ID** | 中（改请求入口 + 传递 context） | 全链路追踪 | 复杂问题排查 |
| **+ 日志采样** | 小（加 Handler 包装） | 减少 90%+ 高频日志 | WebSocket 密集场景 |
| **+ Grafana Cloud 免费层** | 中（注册 + 配置 push URL） | 零运维 | 个人/小团队项目 |

---

## 五、推荐实施路径

### 第一阶段：基础改造（低风险）

1. `slog.NewTextHandler` → `slog.NewJSONHandler`
2. 定义 `logkeys` 常量包，统一 key 命名
3. 新增 `Logger(ctx)` 辅助函数

### 第二阶段：接入 Loki

1. 本地部署 Loki + Promtail（Docker Compose）
2. 配置 Promtail 采集 JSON 日志
3. 接入 Grafana 可视化

### 第三阶段：增强

1. 引入 Trace ID，全链路贯通
2. 高频路径增加采样
3. 审计日志结构化扩展

---

## 六、免费平台参考

| 平台 | 免费额度 | 适合场景 |
|------|----------|----------|
| **Grafana Cloud Loki** | 50GB 存储 / 14天保留 | 首选，与 Loki 原生集成 |
| **Better Stack Logtail** | 1GB/月 / 3天保留 | 快速接入，免部署 |
| **Axiom** | 500MB/月 | 高性能查询 |
| **自托管 Loki** | 无限（取决于磁盘） | 完全掌控，适合长期运行 |

> **推荐 Grafana Cloud**：50GB 免费存储对个人项目绰绰有余，无需运维，与 Grafana 原生集成，Go 生态友好。
