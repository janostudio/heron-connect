#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROJECT_DIR="$ROOT_DIR"
CONFIG_PATH="${1:-/Users/jahweijiang/Documents/lead-agent/bridge/auto-bugfix/config.toml}"
LOG_ROOT="$ROOT_DIR/upgradefeature/logs/auto-bugfix"
TS="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$LOG_ROOT/$TS"
LOG_FILE="$RUN_DIR/cc-connect.log"
LATEST_LINK="$LOG_ROOT/latest"

mkdir -p "$RUN_DIR"
rm -f "$LATEST_LINK"
ln -s "$RUN_DIR" "$LATEST_LINK"

cat <<EOF
Project dir : $PROJECT_DIR
Config path : $CONFIG_PATH
Run dir     : $RUN_DIR
Log file    : $LOG_FILE

Tips:
  tail -f "$LOG_FILE"
  ls -la "$LATEST_LINK"
EOF

cd "$PROJECT_DIR"

{
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] start cc-connect"
  echo "command: GOCACHE=$(pwd)/.gocache go run ./cmd/cc-connect-qhn --force --config $CONFIG_PATH"
  GOCACHE="$(pwd)/.gocache" go run ./cmd/cc-connect-qhn --force --config "$CONFIG_PATH"
} 2>&1 | tee "$LOG_FILE"

