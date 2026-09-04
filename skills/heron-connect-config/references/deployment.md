# 部署 / 后台运行（daemon）

> 版本：随 heron-connect（见 `../SKILL.md` frontmatter 的 `metadata.version`）。

heron-connect **内置跨平台 daemon 管理器**，`heron-connect daemon` 一条命令即可把服务装成
系统级守护进程（开机自启、崩溃重启、日志轮转），**业务不需要自己写 nohup / systemd unit**。

## 核心结论：按环境选方案

| 环境 | 方案 | 说明 |
|------|------|------|
| Linux（有 systemd） | `heron-connect daemon install` | 自动装 systemd unit（root=系统级 / 普通用户=user 级） |
| Linux 容器（无 systemd） | `nohup` / `tmux` / `screen` | systemd.go 明确：容器里找不到 systemctl 时用这三者 |
| macOS | `heron-connect daemon install` | 自动装 launchd LaunchAgent（`com.heron-connect.service`） |
| Windows | `heron-connect daemon install` | 自动装 Task Scheduler 任务（`heron-connect`） |

> ⚠️ 关键区分：**daemon 不是"后台跑个进程"，而是装进系统 init 系统**（systemd/launchd/
> schtasks），具备开机自启 + 进程退出自动拉起。容器里没有 init 系统，只能退回 nohup 系方案。

## 一、推荐：`heron-connect daemon`（有 init 系统时）

### 安装

```bash
# 指向 config 文件
heron-connect daemon install --config ~/.heron-connect/config.toml

# 或指向包含 config.toml 的目录
heron-connect daemon install --work-dir ~/.heron-connect
```

可选参数：`--config PATH`、`--work-dir DIR`、`--log-file PATH`、`--log-max-size N`(MB)、
`--force`(覆盖重装)。日志参数优先级 **CLI > TOML `[log]` > 默认(10MB/7天)**，安装时会
自动从 config.toml 的 `[log]` 段读 file/size/retention。

### 控制

```bash
heron-connect daemon start
heron-connect daemon stop
heron-connect daemon restart
heron-connect daemon status        # 显示 Platform(systemd/launchd/schtasks)、PID、Running
heron-connect daemon logs          # tail 日志
heron-connect daemon logs -f       # 跟随
heron-connect daemon logs -n 100   # 最后 100 行
heron-connect daemon uninstall     # 移除服务
```

### 元数据

安装信息存 `~/.heron-connect/daemon.json`（log_file/work_dir/binary_path 等），
`status`/`logs` 据此定位日志，无需解析 service 定义。

## 二、兜底：nohup / tmux / screen（容器或无 systemd）

daemon 安装时若检测不到 `systemctl` 会直接报错提示改用 nohup。手动方案：

```bash
# nohup（最简）
unset CLAUDECODE && nohup heron-connect --config /path/to/config.toml \
  > /path/to/heron-connect.log 2>&1 &

# 或 tmux / screen 保持会话
tmux new -s heron -d 'heron-connect --config /path/to/config.toml'
```

> 注意：在 Claude Code 会话内启动子进程要先 `unset CLAUDECODE`（见 quickstart/gotchas）。

## 三、现成脚本：`scripts/service.sh`（本 skill 附带）

`service.sh` 就是**基础的启/停/重启/看日志封装**，业务拿到即用，无需自己写 nohup 或
systemd unit。它底层还是调 `heron-connect daemon`，只是帮你省去记子命令、并自动处理
config 路径。

```bash
./scripts/service.sh --config /path/to/config.toml start
./scripts/service.sh --config /path/to/config.toml stop
./scripts/service.sh --config /path/to/config.toml restart
./scripts/service.sh --config /path/to/config.toml status
./scripts/service.sh --config /path/to/config.toml logs [-f]
./scripts/service.sh --config /path/to/config.toml uninstall
```

特点：
- 只要给一份 config.toml 就能跑，任意路径/任意文件名都行（脚本在 skill 的 `state/<name>/`
  建软链指向你的文件，规避 daemon 要求「配置文件名必须是 config.toml 且在某目录下」的限制）。
- `start` 会 `--force` 重装，把当前 toml 应用进去；改完 toml 重跑 `start` 即生效。
- 可选 `--binary` 指定二进制、`--log-file`、`--log-max-size`（默认 50MB）。
- 每个 config 对应独立 state 目录与系统服务，所以一份脚本也能管多份配置。

> 一句话：单份配置直接用 `heron-connect daemon install --config ...` 也完全够；
> `service.sh` 只是把这一套包成傻瓜式脚本，二者能力等价。

## 四、多实例 / 多配置

一个进程一个 config；要跑多套配置，用 `service.sh` 分别对每份 toml 起独立服务（各自独立
state 目录 + 系统服务），或用 daemon 的 `--work-dir` 指向不同目录。多 project 则在**同一份**
config.toml 里写多个 `[[projects]]`，无需多进程（见 project-agent.md）。

## 五、日志

- 默认日志：`~/.heron-connect/logs/heron-connect.log`
- daemon 模式自动轮转（按大小 + 按天，见 advanced.md 的 `[log]` 段）
- `heron-connect daemon logs -f` 实时看，无需手动 tail
