#!/usr/bin/env bash
# Linux sandbox smoke test — run after go build
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ." >&2
  exit 1
fi

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "Linux-only smoke test; skipping." >&2
  exit 0
fi

KERNEL="$(uname -r | cut -d. -f1-2)"
MAJOR="${KERNEL%%.*}"
MINOR="${KERNEL#*.}"
if (( MAJOR < 5 || (MAJOR == 5 && MINOR < 13) )); then
  echo "Landlock requires Linux kernel 5.13+ (found $(uname -r)); skipping native sandbox smoke." >&2
  exit 0
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

HOST_PROBE="$HOME/nvx-smoke-host-probe.txt"
rm -f "$HOST_PROBE"
if "$NVX" shim node -e "require('fs').writeFileSync(process.env.HOME + '/nvx-smoke-host-probe.txt','pwned')" 2>/dev/null; then
  if [[ -f "$HOST_PROBE" ]]; then
    rm -f "$HOST_PROBE"
    echo "host profile write should be blocked" >&2
    exit 1
  fi
fi
rm -f "$HOST_PROBE"

echo "Linux sandbox smoke passed."
