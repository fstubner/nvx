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

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js not available; skipping macOS sandbox smoke." >&2
  exit 0
fi

PROJ="$(mktemp -d)"
trap 'rm -rf "$PROJ"' EXIT
cd "$PROJ"

"$NVX" init-shims >/dev/null

echo "Testing sandboxed node via shim..."
PROBE="$PROJ/probe.txt"
set +e
"$NVX" shim node -e "require('fs').writeFileSync('probe.txt','ok')"
rc=$?
set -e
if [[ $rc -ne 0 || ! -f "$PROBE" ]]; then
  echo "sandboxed node failed (rc=$rc). Recent Seatbelt denials:" >&2
  log show --last 90s --style compact \
    --predicate 'eventMessage CONTAINS[c] "deny" AND (eventMessage CONTAINS[c] "node" OR process == "sandboxd" OR senderImagePath CONTAINS[c] "Sandbox")' \
    2>/dev/null | tail -40 >&2 || echo "(could not read sandbox log)" >&2
  exit 1
fi

echo "macOS sandbox smoke passed."
