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

# --strict, and the containment line checked, for the reason the Linux sibling
# spells out: `node -e` is the user's own code, which the default level does not
# contain, so this ran outside the sandbox and reported success either way.
# Nothing in its output ever said "Seatbelt isolation active", which is how it
# was noticed.
echo "Testing sandboxed node via shim..."
PROBE="$PROJ/probe.txt"
set +e
NODE_OUT="$("$NVX" -y --strict shim node -e "require('fs').writeFileSync('probe.txt','ok')" 2>&1)"
rc=$?
set -e
echo "$NODE_OUT"
if [[ $rc -eq 0 && -f "$PROBE" ]] && ! grep -q "Seatbelt isolation active" <<<"$NODE_OUT"; then
  echo "the probe wrote its file, but nothing says it was contained" >&2
  exit 1
fi
if [[ $rc -ne 0 || ! -f "$PROBE" ]]; then
  echo "sandboxed node failed (rc=$rc). Recent Seatbelt denials:" >&2
  log show --last 90s --style compact \
    --predicate 'eventMessage CONTAINS[c] "deny" AND (eventMessage CONTAINS[c] "node" OR process == "sandboxd" OR senderImagePath CONTAINS[c] "Sandbox")' \
    2>/dev/null | tail -40 >&2 || echo "(could not read sandbox log)" >&2
  exit 1
fi

# An actual install: see the Linux sibling for why. It exercises the runtime's
# own child processes, a writable HOME for the npm cache and the registry
# through the egress proxy, none of which a `node -e` touches -- and the docker
# provider failed on two of those the first time anyone ran one.
#
# No --strict here: an install is contained at the default level, so this is the
# path a person actually takes.
# An nvx-managed runtime, so the install exercises the runtime nvx pins rather
# than whatever node the machine happens to have. It is also what makes the PATH
# arrangement below meaningful: the fix being covered puts the pinned runtime's
# own bin directory on the contained PATH, and there is no pinned runtime to put
# there otherwise.
echo "Installing an nvx-managed runtime..."
if ! "$NVX" -y install 22 >/dev/null 2>&1 || ! "$NVX" -y default 22 >/dev/null 2>&1; then
  echo "::warning::could not install an nvx-managed runtime (network?); skipping the macOS install phase" >&2
  echo "macOS sandbox smoke passed (install phase skipped)."
  exit 0
fi

echo "Installing a package through the sandbox..."
PKG="$PROJ/pkgtest"
mkdir -p "$PKG"
cd "$PKG"
printf '%s' '{"name":"probe","version":"1.0.0","dependencies":{"ms":"2.1.3"}}' > package.json
# Shim directory leading PATH: what `nvx env` leaves behind, and the
# arrangement in which a contained install was measured to fail on Linux -- npm
# resolves node through PATH, found nvx's shim, and the shim could not resolve a
# version inside the sandbox.
set +e
INSTALL="$(PATH="$NVX_HOME/bin:$PATH" "$NVX" -y shim npm install 2>&1)"
IRC=$?
set -e
if [[ $IRC -ne 0 ]]; then
  echo "$INSTALL" >&2
  echo "a contained npm install failed (exit $IRC)" >&2
  exit 1
fi
if ! grep -q "Seatbelt isolation active" <<<"$INSTALL"; then
  echo "$INSTALL" >&2
  echo "the install ran, but nothing says it was contained" >&2
  exit 1
fi
if [[ ! -f node_modules/ms/package.json ]]; then
  echo "$INSTALL" >&2
  echo "the install reported success but installed nothing" >&2
  exit 1
fi

echo "macOS sandbox smoke passed."
