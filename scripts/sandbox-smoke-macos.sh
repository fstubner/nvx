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

# --connect: the sandbox reaches one named service on this machine, and only
# because it was named.
#
# Both directions are checked, and the order matters. Without the negative run
# first, a positive result proves nothing: if macOS were not enforcing the
# profile's network rules at all, the contained fetch would succeed whether or
# not --connect was passed, and this test would pass while measuring nothing.
echo "Testing --connect against a service on this machine..."
cat > "$PROJ/service.js" <<'JS'
const fs = require('fs'), http = require('http');
const s = http.createServer((q, r) => r.end('SERVICE_OK'));
s.listen(0, '127.0.0.1', () => fs.writeFileSync(process.argv[2], String(s.address().port)));
JS
node "$PROJ/service.js" "$PROJ/port.txt" &
SVC_PID=$!
trap 'kill $SVC_PID 2>/dev/null || true; rm -rf "$PROJ"' EXIT
for _ in $(seq 1 50); do [[ -s "$PROJ/port.txt" ]] && break; sleep 0.1; done
if [[ ! -s "$PROJ/port.txt" ]]; then
  echo "the stand-in host service never reported its port" >&2
  exit 1
fi
SVC_PORT="$(cat "$PROJ/port.txt")"

# Dials NVX_CONNECT_<port> when nvx published it, and the service's own port
# otherwise -- which is what the uncontained-reach check needs.
cat > "$PROJ/client.js" <<'JS'
const http = require('http');
const host = process.argv[2];
const port = process.env['NVX_CONNECT_' + host] || host;
http.get({ host: '127.0.0.1', port: Number(port) }, res => {
  let b = '';
  res.on('data', d => (b += d));
  res.on('end', () => console.log('GOT ' + b));
}).on('error', e => console.log('FAILED ' + e.code));
JS

set +e
NOCONNECT="$("$NVX" -y --strict shim node "$PROJ/client.js" "$SVC_PORT" 2>&1)"
set -e
if grep -q "GOT SERVICE_OK" <<<"$NOCONNECT"; then
  echo "$NOCONNECT" >&2
  echo "a contained process reached 127.0.0.1:$SVC_PORT with no --connect; the Seatbelt network rules are not containing it" >&2
  exit 1
fi
if ! grep -q "FAILED" <<<"$NOCONNECT"; then
  echo "$NOCONNECT" >&2
  echo "the uncontained-reach check did not run: the probe neither connected nor reported a failure" >&2
  exit 1
fi

set +e
CONNECTED="$("$NVX" -y --strict --connect "$SVC_PORT" shim node "$PROJ/client.js" "$SVC_PORT" 2>&1)"
CRC=$?
set -e
if [[ $CRC -ne 0 ]] || ! grep -q "GOT SERVICE_OK" <<<"$CONNECTED"; then
  echo "$CONNECTED" >&2
  echo "--connect did not reach the service (exit $CRC)" >&2
  exit 1
fi
kill $SVC_PID 2>/dev/null || true
trap 'rm -rf "$PROJ"' EXIT

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

# And that a nested lookup gets the runtime nvx pinned, not merely some node.
#
# This is the half an install alone does not show. Measured on a macOS runner
# with the PATH fix removed: the contained process was running the pinned
# v22.23.2, and a nested `node` resolved to the machine's own v24.20.0 -- npm
# found nvx's shim on PATH, and the shim's fallback picked whatever non-nvx node
# it could see. The install still succeeded, silently, under a runtime nobody
# chose. On a machine with no other node the same lookup fails outright instead,
# which is what Linux showed. Both are the same defect; only one of them is loud.
cat > nested.js <<'JS'
const cp = require('child_process');
const nested = cp.execSync('node -p process.version', { shell: '/bin/sh' }).toString().trim();
console.log(nested === process.version
  ? 'NESTED_OK ' + nested
  : 'NESTED_MISMATCH pinned=' + process.version + ' nested=' + nested);
JS
set +e
NESTED="$(PATH="$NVX_HOME/bin:$PATH" "$NVX" -y --strict shim node nested.js 2>&1)"
NRC=$?
set -e
if [[ $NRC -ne 0 ]] || ! grep -q "NESTED_OK" <<<"$NESTED"; then
  echo "$NESTED" >&2
  echo "a nested lookup inside the sandbox did not get the pinned runtime" >&2
  exit 1
fi

echo "macOS sandbox smoke passed."
