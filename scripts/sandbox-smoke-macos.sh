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

# Baseline: can sandbox-exec run node at all on this machine with a permissive
# profile? Distinguishes "our profile is too strict" from "sandbox-exec/arm64
# can't run this binary regardless".
ALLOWALL="$PROJ/allowall.sb"
printf '(version 1)\n(allow default)\n' > "$ALLOWALL"
set +e
/usr/bin/sandbox-exec -f "$ALLOWALL" "$(command -v node)" -e "process.exit(0)"
baseline_rc=$?
set -e
echo "baseline (allow default) sandbox-exec node rc=$baseline_rc"

# Trace exactly which Seatbelt operations node needs (logged under allow-default),
# so the strict profile can be completed instead of guessed at.
TRACE_PROF="$PROJ/trace.sb"
TRACE_OUT="$PROJ/trace.out"
printf '(version 1)\n(allow default)\n(trace "%s")\n' "$TRACE_OUT" > "$TRACE_PROF"
set +e
/usr/bin/sandbox-exec -f "$TRACE_PROF" "$(command -v node)" -e "require('fs').writeFileSync('trace-probe.txt','ok')" >/dev/null 2>&1
set -e
echo "=== operation types node needs (from trace) ==="
grep -oE '\(allow [a-z0-9*-]+' "$TRACE_OUT" 2>/dev/null | sort | uniq -c || echo "(no trace output)"
echo "=== end trace ==="

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
