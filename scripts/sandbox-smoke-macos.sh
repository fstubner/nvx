#!/usr/bin/env bash
# macOS Seatbelt runtime smoke — requires sandbox-exec
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS-only smoke test; skipping." >&2
  exit 0
fi

if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ." >&2
  exit 1
fi

PROJ="$(mktemp -d)"
trap 'rm -rf "$PROJ"' EXIT
cd "$PROJ"

"$NVX" init-shims >/dev/null

echo "Testing sandboxed node via shim..."
PROBE="$PROJ/probe.txt"
"$NVX" shim node -e "require('fs').writeFileSync('probe.txt','ok')"
if [[ ! -f "$PROBE" ]]; then
  echo "workdir write failed" >&2
  exit 1
fi

echo "macOS sandbox smoke passed."
