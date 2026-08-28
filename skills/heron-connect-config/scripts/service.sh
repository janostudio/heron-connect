#!/usr/bin/env bash
# 后台运行 heron-connect 的 TOML 配置：start / stop / restart / status / logs / uninstall。
# 底层封装 `heron-connect daemon`（自动安装为 launchd / systemd / Windows Task Scheduler）。
#
# 用法：只要指定一份 config.toml 就能跑起来，无需手动起系统服务。
#
#   ./scripts/service.sh --config /path/to/config.toml start
#   ./scripts/service.sh --config /path/to/config.toml stop
#   ./scripts/service.sh --config /path/to/config.toml restart
#   ./scripts/service.sh --config /path/to/config.toml status
#   ./scripts/service.sh --config /path/to/config.toml logs [-f]
#   ./scripts/service.sh --config /path/to/config.toml uninstall
#
# 可选：
#   --binary /path/to/heron-connect   指定 heron-connect 二进制（默认用 PATH 里的）
#   --log-file /path/to.log           指定日志文件
#   --log-max-size N                 日志轮转阈值（MB，默认 50）
#
# 说明：
#   - heron-connect daemon 要求配置文件名为 config.toml 且位于某目录下
#     （cmd/heron-connect/daemon.go 会检查 <workdir>/config.toml）。
#     本脚本按配置文件名在 <skill>/state/<name>/ 建一个 config.toml 软链指向你的文件，
#     因此任意路径、任意文件名的 toml 都能直接用。
#   - 每个 config 文件对应独立的 state 目录与系统服务，互不干扰。
#   - start 会以 --force 重新安装，把当前 toml 应用到服务；改完 toml 重跑 start 即生效。

set -euo pipefail

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

CONFIG=""
BINARY="heron-connect"
LOG_FILE=""
LOG_MAX=50
ACTION=""

usage() {
  sed -n '3,24p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 1
}

# ---- 解析参数：--config 必须，后跟一个 action ----
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)   CONFIG="$2"; shift 2;;
    --config=*) CONFIG="${1#*=}"; shift;;
    --binary)   BINARY="$2"; shift 2;;
    --binary=*) BINARY="${1#*=}"; shift;;
    --log-file) LOG_FILE="$2"; shift 2;;
    --log-file=*) LOG_FILE="${1#*=}"; shift;;
    --log-max-size) LOG_MAX="$2"; shift 2;;
    --log-max-size=*) LOG_MAX="${1#*=}"; shift;;
    -h|--help)  usage;;
    -*)         echo "unknown option: $1" >&2; usage;;
    *)          ACTION="$1"; shift;;
  esac
done

if [[ -z "$CONFIG" ]]; then
  echo "error: --config is required" >&2; usage
fi
if [[ -z "$ACTION" ]]; then
  echo "error: missing action (start|stop|restart|status|logs|uninstall)" >&2; usage
fi
case "$ACTION" in
  start|stop|restart|status|logs|uninstall) ;;
  *) echo "error: unknown action: $ACTION" >&2; usage;;
esac

CONFIG="$(cd "$(dirname "$CONFIG")" && pwd)/$(basename "$CONFIG")"
if [[ ! -f "$CONFIG" ]]; then
  echo "error: config not found: $CONFIG" >&2; exit 1
fi
if ! command -v "$BINARY" >/dev/null 2>&1; then
  echo "error: heron-connect binary not found on PATH: $BINARY" >&2
  echo "       install it, or pass --binary /path/to/heron-connect" >&2
  exit 1
fi

# ---- 归一化：state/<name>/config.toml -> 用户文件 ----
NAME="$(basename "$CONFIG" .toml)"
STATE_DIR="$SKILL_DIR/state/$NAME"
mkdir -p "$STATE_DIR"
ln -sfn "$CONFIG" "$STATE_DIR/config.toml"

# ---- 拼接 daemon install 参数（仅 start 需要） ----
DAEMON_BIN=( "$BINARY" daemon )
INSTALL_ARGS=( install --work-dir "$STATE_DIR" --force )
[[ -n "$LOG_FILE" ]] && INSTALL_ARGS+=( --log-file "$LOG_FILE" )
INSTALL_ARGS+=( --log-max-size "$LOG_MAX" )

case "$ACTION" in
  start)
    echo "==> heron-connect daemon install (config: $CONFIG)"
    "${DAEMON_BIN[@]}" "${INSTALL_ARGS[@]}"
    ;;
  stop)    "${DAEMON_BIN[@]}" stop    ;;
  restart) "${DAEMON_BIN[@]}" restart ;;
  status)  "${DAEMON_BIN[@]}" status  ;;
  logs)
    shift; "${DAEMON_BIN[@]}" logs "$@"
    ;;
  uninstall) "${DAEMON_BIN[@]}" uninstall ;;
esac
