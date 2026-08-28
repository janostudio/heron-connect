# Provider（API 服务商）配置

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

两种方式：**全局 `[[providers]]`**（多项目共享，项目用 `provider_refs` 按名引用）
或**项目内 `[[projects.agent.providers]]`**（`/provider` 命令在聊天中切换）。两者结构相同。

## 全局 provider

```toml
[[providers]]
name = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"
agent_types = ["claudecode"]        # 限定只给这些 agent 用

[[providers]]
name = "minimaxi-claude"
api_key = "${MINIMAXI_API_KEY}"
base_url = "https://api.minimaxi.chat/v1"
agent_types = ["claudecode"]
model = "claude-sonnet-4-6"

[[providers]]
name = "dashscope"
api_key = "${DASHSCOPE_API_KEY}"
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"
model = "glm-5.1"
thinking = "disabled"
```

项目内引用：

```toml
[projects.agent]
type = "claudecode"
# provider = "relay"              # 当前激活
# provider_refs = ["minimaxi", "dashscope"]
```

## 项目内 provider（/provider 切换）

```toml
[projects.agent.providers]
# [[projects.agent.providers]]   # 多个需用数组语法
# name = "relay"
# api_key = "sk-xxx"
# base_url = "https://api.relay-service.com"
# model = "claude-sonnet-4-6"
# thinking = "disabled"          # 第三方供应商不支持 adaptive thinking 时用
#
# # 预配置 /model 命令可选的模型列表
# [[projects.agent.providers.models]]
# model = "claude-sonnet-4-6"
# alias = "sonnet"
# [[projects.agent.providers.models]]
# model = "claude-opus-4-8"
# alias = "opus"
```

## Provider 字段

| 字段 | 说明 |
|------|------|
| `name` | 必填，唯一标识 |
| `api_key` | 密钥（用 `${VAR}`） |
| `base_url` | OpenAI 兼容 base URL |
| `model` | 默认模型 |
| `models` | 模型列表（`model` + `alias`），供 `/model` 命令 |
| `thinking` | `"disabled"` 等，覆盖 thinking 参数（部分第三方不支持） |
| `env` | 环境变量 map（Bedrock/Vertex 等特殊环境） |
| `agent_types` | 限定给哪些 agent 用 |
| `endpoints` / `agent_models` / `agent_model_lists` | 按 agent type 的 base URL / 模型覆盖 |
| `codex` | Codex 专属字段（`env_key`/`wire_api`/`http_headers`） |

## CLI 增删

```bash
heron-connect provider add --project my-backend --name relay --api-key sk-xxx
```
