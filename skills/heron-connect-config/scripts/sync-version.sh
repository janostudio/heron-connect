#!/usr/bin/env bash
# Sync heron-connect-config skill version with heron-connect's own version.
#
# heron-connect 的版本源是 npm/package.json（见 memory: heron_release_flow）。
# 本脚本读取它，并更新本 skill 的 SKILL.md frontmatter 里的
# `metadata.version` 以及正文「本 Skill 对应 heron-connect **vX.Y.Z**」，
# 保证 skill 与 heron-connect 版本同步。版本号只保存在 SKILL.md 一处来源。
#
# Usage:
#   ./scripts/sync-version.sh                # 同步到仓库内 npm/package.json 的版本
#   ./scripts/sync-version.sh <path/to/package.json>   # 指定版本源文件
#
# 建议在 heron-connect 发版（make publish）后运行一次。

set -euo pipefail

SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKILL_FILE="$SKILL_DIR/SKILL.md"

# 版本源：默认取 heron-connect 仓库根的 npm/package.json
PKG_JSON="${1:-}"
if [[ -z "$PKG_JSON" ]]; then
  PKG_JSON="$(cd "$SKILL_DIR/../.." && pwd)/npm/package.json"
fi

if [[ ! -f "$PKG_JSON" ]]; then
  echo "error: version source not found: $PKG_JSON" >&2
  exit 1
fi

if ! command -v node >/dev/null 2>&1; then
  echo "error: node is required" >&2
  exit 1
fi

VER="$(node -e "console.log(require(process.argv[1]).version || '')" "$PKG_JSON")"
if [[ -z "$VER" ]]; then
  echo "error: no version field in $PKG_JSON" >&2
  exit 1
fi
VER="${VER#v}"   # 去掉可能的 v 前缀，保持纯数字版本

# 用 node 原地更新 frontmatter 的 metadata.version，以及正文的「vX.Y.Z」。
# 只替换 frontmatter 区域内的 version 字段，避免误改正文其它 version。
# 用单引号 heredoc，bash 不解析脚本内容（避免反斜杠/换行被破坏）。
OLD="$(
  node - "$SKILL_FILE" <<'NODE'
const fs = require('fs');
const s = fs.readFileSync(process.argv[2], 'utf8');
const m = /^---\n([\s\S]*?)\n---/.exec(s);
if (!m) process.exit(1);
const v = /^[ \t]*version:[ \t]*([^\s]+)/m.exec(m[1]);
process.stdout.write(v ? v[1] : '');
NODE
)"

node - "$SKILL_FILE" "$VER" <<'NODE'
const fs = require('fs');
let s = fs.readFileSync(process.argv[2], 'utf8');
const ver = process.argv[3];
// frontmatter 内 metadata.version（行可能带缩进，如 `  version: 1.1.8`）
s = s.replace(/^([ \t]*version:)[ \t]*[^\s]+/m, (m, key) => key + ' ' + ver);
// 正文「本 Skill 对应 heron-connect **vX.Y.Z**」
s = s.replace(/(本 Skill 对应 heron-connect \*\*v)\d+\.\d+\.\d+(\*\*)/, (m, a, b) => a + ver + b);
fs.writeFileSync(process.argv[2], s);
NODE

if [[ -n "$OLD" && "$OLD" == "$VER" ]]; then
  echo "heron-connect-config: version already ${VER} (unchanged)"
else
  echo "heron-connect-config: version ${OLD:-<none>} -> ${VER}"
fi
