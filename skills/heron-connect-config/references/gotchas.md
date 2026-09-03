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

## 后台运行（daemon 服务）

**推荐直接用 `daemon install`，无需 shell 脚本**。一条命令完成安装/更新/重启（幂等）：

```bash
# 安装并启动（已装过则覆盖重装 = 先停旧再启新）
heron-connect daemon install --config /path/to/config.toml --force

# 可选：覆盖日志参数（优先级 CLI > TOML > 默认）
heron-connect daemon install --config /path/to/config.toml --force \
  --log-file /path/to/app.log --log-max-size 20 --log-retention-days 30
```

**cwd 说明**：daemon 模式下进程 cwd = config.toml 所在目录（`--config` 的父目录）。
所以 toml 里的相对路径（`work_dir`、`cron_data_dir`、`[log].file`）都基于该目录解析，
整个目录可整体迁移，无需写死绝对地址。

日常管理：

```bash
heron-connect daemon status          # 状态 / PID / 日志路径
heron-connect daemon logs -f         # 实时看日志（-f 跟随，-n N 看最近 N 行）
heron-connect daemon stop            # 停止
heron-connect daemon start           # 启动（需已 install）
heron-connect daemon restart --force # 重启（先杀旧进程）
heron-connect daemon uninstall       # 卸载（日志与会话数据保留）
```

支持 Linux systemd / macOS launchd / Windows Task Scheduler。

> 另有 `scripts/service.sh` 脚本封装（`service.sh --config <path> <start|stop|...>`），
> 本质就是把任意文件名的 toml 软链成 `config.toml` 再走 `daemon install`。
> 若你的配置文件名就叫 `config.toml`，直接 `daemon install --config` 即可，无需脚本。
