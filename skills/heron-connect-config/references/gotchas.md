# 常见坑与排查

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。权威字段来源：仓库根 `config.example.toml`。

## 常见坑

1. **TOML 数组 vs 表**：多个 provider / 多个 platform / 多个 project 用
   `[[...]]`（数组），单个配置对象用 `[projects.agent]` 这类表。
   `[projects.agent.options]` 这种带后缀的是**表**，不是数组。
2. **字符串值全部支持 `${VAR}` 替换**——永远优先用环境变量存密钥，别明文。
3. **work_dir 必须是绝对路径**，agent 进程在工作目录下启动。
4. **允许的用户**：`allow_from`/`admin_from` 用的是 IM 侧 User ID，`/whoami` 查。
5. **流式安全余量**：企业微信等平台流式输出阈值要**低于物理上限**，预留展示余量。
6. **会话串号**：多 Web 客户端共用同一 `session_key` 会导致 agent_session 绑定被覆盖
   进而 mismatch → 回收 → kill → 空响应；每连接用独立 key。
7. **`wecom` / `line` 需要公网 IP**；其余大多走长连接无需公网。
8. **改动配置后重启生效**：`heron-connect --config ./config.toml`，或
   `heron-connect daemon restart`。
9. **验证**：`heron-connect doctor`（若实现该 agent 的诊断）、`heron-connect --help`
   查看子命令。所有字段以仓库根 `config.example.toml` 为权威。

## 排错命令速查

```bash
heron-connect --config ./config.toml          # 前台启动，看启动日志
heron-connect doctor                          # agent 健康诊断（按实现）
heron-connect web                             # 打开 Web 管理后台
heron-connect daemon install/start/logs -f    # 守护进程
heron-connect cron add/...                    # 定时任务管理
heron-connect feishu setup --project <name>   # 平台快速配置（各平台可能不同）
heron-connect provider add --project <name> --name <p> --api-key <k>  # 加 provider
```

## 后台运行（推荐用 service.sh）

推荐用本 skill 的脚本封装，只需指定一份 toml：

```bash
skills/heron-connect-config/scripts/service.sh --config /path/to/config.toml start
skills/heron-connect-config/scripts/service.sh --config /path/to/config.toml stop
skills/heron-connect-config/scripts/service.sh --config /path/to/config.toml restart
skills/heron-connect-config/scripts/service.sh --config /path/to/config.toml status
skills/heron-connect-config/scripts/service.sh --config /path/to/config.toml logs -f
skills/heron-connect-config/scripts/service.sh --config /path/to/config.toml uninstall
```

改完 toml 重跑 `start` 即生效（内部 `--force` 重装）。底层等价命令：

```bash
heron-connect daemon install --work-dir ~/.heron-connect --force   # 安装并启动
heron-connect daemon start
heron-connect daemon stop
heron-connect daemon restart
heron-connect daemon status
heron-connect daemon logs -f
heron-connect daemon uninstall
```
支持 Linux systemd / macOS launchd / Windows Task Scheduler。
