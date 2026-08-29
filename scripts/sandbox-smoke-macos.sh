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

# A throwaway NVX_HOME, the way sandbox-smoke.sh already does it. Without one,
# `init-shims` below writes into the developer's real ~/.nvx and replaces the
# installed shims with the build under test. The Windows siblings did the same
# and were fixed first; this was found by sweeping the rest rather than by
# running it, since it needs macOS.
export NVX_HOME="$PROJ/nvxhome"
mkdir -p "$NVX_HOME"

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
