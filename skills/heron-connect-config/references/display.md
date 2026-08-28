# 显示与交互配置（[display]）

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

控制中间消息（思考过程、工具调用）在聊天里怎么展示。

## 全局 [display]

```toml
[display]
mode = "full"                # full(默认) | compact | quiet | stream
#  full:    思考+工具消息每条单独发
#  compact: 隐藏思考/工具，每段文本独立发
#  quiet:   隐藏思考/工具，全部文本合并一张卡片
#  stream:  隐藏思考，工具进度与正文合并到同一条持续更新消息
thinking_messages = true     # 是否显示思考消息（默认 true）
thinking_max_len = 300       # 思考消息最大字符数（默认 300，0=不截断）
tool_messages = true         # 是否显示工具进度（默认 true）
tool_max_len = 500           # 工具消息最大字符数（默认 500）
card_mode = "legacy"         # legacy | rich（飞书 Card 2.0）
```

## 覆盖优先级

**平台级 `[projects.platforms.display]` > 项目级 `[projects.display]` > 全局
`[display]` > 内置默认**。逐字段独立覆盖：设了的字段生效，没设的回落到上一级。

```toml
[[projects]]
name = "noisy-project"
[projects.display]           # 只覆盖这两个字段，其余继承全局
thinking_messages = false
tool_messages = false
```

典型用途：全局详细展示，但某个话痨项目单独静音，反之亦然。

> 想让**同一个项目**里 Web 管理后台和 IM 机器人显示不同详细度？用一个虚拟
> `type = "web"` 平台条目单独给 Web 会话配 `[projects.platforms.display]`，
> 见 `references/platforms.md` 的「虚拟 `type = "web"` 平台」。

## 其他交互相关（简要）

- `[instant_reply]`：收到消息立即回「🤔 Thinking...」类确认。
  `enabled = false`（默认），`content = "自定义文案"`。
- `[stream_preview]`：实时流式预览（默认开）。
  `disabled_platforms`、`interval_ms`、`min_delta_chars`、`max_chars`。
- `[rate_limit]`：会话级入站限流，`max_messages`/`window_secs`（默认 20/60s）。
- `[outgoing_rate_limit]`：出站限流防 IM 封号，`max_per_second`/`burst`，
  可 `platforms` 按平台覆盖。
