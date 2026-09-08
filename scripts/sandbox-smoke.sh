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

# What the sandbox actually needs, probed as the sandbox will use it.
#
# Two things were wrong with asking `unshare -n`. It omits the user namespace
# nvx pairs the network one with, so it is refused for anyone but root and this
# script skipped on every unprivileged machine including the CI runner it was
# written for. And creating the namespace is not the same as being allowed to
# use it: Ubuntu 24.04 hardens unprivileged user namespaces through AppArmor, so
# the clone succeeds, CAP_NET_ADMIN inside it does not, and nvx's loopback setup
# gets EPERM and fails closed. Only bringing loopback up answers the question.
if ! command -v ip >/dev/null 2>&1; then
  echo "iproute2 not installed; nvx's loopback setup needs \`ip\`. Skipping." >&2
  exit 0
fi
if ! unshare -Urn -- ip link set lo up >/dev/null 2>&1; then
  echo "This host does not allow loopback to be configured inside an unprivileged" >&2
  echo "user namespace, so nvx's network isolation cannot start and it fails closed." >&2
  echo "On Ubuntu 24.04 this is AppArmor; lift it with:" >&2
  echo "  sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0" >&2
  echo "Skipping Linux sandbox smoke." >&2
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

# A runtime Landlock actually permits, for the reason spelled out in
# scripts/sandbox-enforcement-linux.sh: the ruleset is an allowlist covering
# /usr /lib /lib64 /bin /sbin /etc and nvx's own versions/, bin/ and current/,
# and a hosted runner's Node lives in /opt/hostedtoolcache, which is on none of
# them. Until now this script never reached the question, because its namespace
# check skipped it on every unprivileged machine.
export NVX_HOME="$PROJ/nvxhome"
mkdir -p "$NVX_HOME"
echo "Installing an nvx-managed runtime (Landlock does not permit exec outside its allowlist)..."
if ! "$NVX" -y install 22 >/dev/null 2>&1 || ! "$NVX" -y default 22 >/dev/null 2>&1; then
  echo "::warning::could not install an nvx-managed runtime (network?); skipping Linux sandbox smoke" >&2
  exit 0
fi

"$NVX" init-shims >/dev/null

# --strict, or this measures nothing. Without it nvx applies the default policy,
# which does not contain an arbitrary directory, and every run below reported
# "Running directly (not sandboxed)" -- so the host-write assertion could only
# ever fail and the workdir-write one could only ever pass. The script has been
# skipping for long enough that neither was observed.
echo "Testing sandboxed node via shim..."
PROBE="$PROJ/probe.txt"
"$NVX" -y --strict shim node -e "require('fs').writeFileSync('probe.txt','ok')"
if [[ ! -f "$PROBE" ]]; then
  echo "workdir write failed" >&2
  exit 1
fi

HOST_PROBE="$HOME/nvx-smoke-host-probe.txt"
rm -f "$HOST_PROBE"
if "$NVX" -y --strict shim node -e "require('fs').writeFileSync(process.env.HOME + '/nvx-smoke-host-probe.txt','pwned')" 2>/dev/null; then
  if [[ -f "$HOST_PROBE" ]]; then
    rm -f "$HOST_PROBE"
    echo "host profile write should be blocked" >&2
    exit 1
  fi
fi
rm -f "$HOST_PROBE"

# --connect: the sandbox reaches one named service on this machine, and only
# because it was named.
#
# Linux is the platform where the negative half is structural rather than a
# permission: the sandbox has its own network namespace, so 127.0.0.1 in there is
# not this machine's. The check runs anyway and runs first, because a namespace
# that silently failed to be created would leave the sandbox sharing this one --
# and then the positive result below would prove nothing at all.
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
# otherwise -- which is what the unreachable-without-it check needs.
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
  echo "a contained process reached 127.0.0.1:$SVC_PORT with no --connect; it is sharing this machine's network namespace" >&2
  exit 1
fi
if ! grep -q "FAILED" <<<"$NOCONNECT"; then
  echo "$NOCONNECT" >&2
  echo "the unreachable-without-it check did not run: the probe neither connected nor reported a failure" >&2
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

# network.mode: loopback reaches the same service, and the default mode does not.
#
# The mode's definition lives in the egress proxy: a loopback destination is
# permitted without an allow_hosts entry, and only in this mode. On this platform
# it was unreachable until 2026-09-08, because loopback shared offline's seccomp
# filter -- which denies connect() outright, so the contained process could not
# reach the proxy that implements the mode. Nothing noticed: the only test of the
# mode was of that proxy rule, in a state where the proxy could not be consulted.
#
# Asked as a CONNECT to the proxy, for the reason spelled out in
# scripts/sandbox-smoke-egress.sh: nothing in Node core reads HTTPS_PROXY, so an
# ordinary request measures a direct connection, which inside this namespace dies
# without the allowlist ever being consulted. The status code IS the decision --
# 200 tunnelled, 403 refused -- which an exit code cannot tell apart from "never
# reached the proxy".
#
# Both modes are run, and the default one first. Alone, a 200 in loopback mode
# would equally be what a sandbox with an accidental route out looks like.
echo "Testing network.mode loopback against the same service..."
LOOPBACK_PROBE=$(cat <<JS
const http = require('http');
const u = new URL(process.env.HTTPS_PROXY);
const req = http.request({
  host: u.hostname, port: u.port, method: 'CONNECT', path: '127.0.0.1:$SVC_PORT',
  headers: { 'Proxy-Authorization': 'Basic ' +
    Buffer.from(decodeURIComponent(u.username) + ':' + decodeURIComponent(u.password)).toString('base64') },
});
req.on('connect', (res, socket) => { socket.destroy(); console.log('CONNECT=' + res.statusCode); process.exit(0); });
req.on('response', res => { console.log('CONNECT=' + res.statusCode); process.exit(0); });
req.on('error', e => { console.log('CONNECT=error ' + e.message); process.exit(0); });
req.end();
JS
)

DEFAULT_MODE_OUT="$("$NVX" -y --strict shim node -e "$LOOPBACK_PROBE" 2>&1 | grep '^CONNECT=' || true)"
echo "  proxy said (default mode): ${DEFAULT_MODE_OUT:-<nothing>}"
if [[ "$DEFAULT_MODE_OUT" == "CONNECT=200" ]]; then
  echo "the default mode tunnelled to a loopback service with no allow_hosts entry" >&2
  exit 1
fi
if [[ -z "$DEFAULT_MODE_OUT" ]]; then
  echo "the contained process never reached the proxy, so the mode was not exercised" >&2
  exit 1
fi

# Written to the global policy, which is the developer's own file. A project
# policy asking for this mode is a loosening and needs approval, deliberately.
cat > "$NVX_HOME/policy.json" <<'JSON'
{ "isolation": { "network": { "mode": "loopback" } } }
JSON
LOOPBACK_OUT="$("$NVX" -y --strict shim node -e "$LOOPBACK_PROBE" 2>&1 | grep '^CONNECT=' || true)"
echo "  proxy said (loopback mode): ${LOOPBACK_OUT:-<nothing>}"
if [[ "$LOOPBACK_OUT" != "CONNECT=200" ]]; then
  rm -f "$NVX_HOME/policy.json"
  echo "network.mode loopback did not reach a service on 127.0.0.1" >&2
  exit 1
fi

# And a raw connection, which is the half a proxy cannot carry.
#
# client.js dials 127.0.0.1:$SVC_PORT itself and knows nothing about HTTP_PROXY,
# so this only passes if the loopback traffic is being redirected out of the
# namespace. The very same command was run above under the default mode and
# reported FAILED, which is the control -- that is what proves this is the mode
# doing it rather than the sandbox having a route it should not.
#
# This is what "at their own addresses, over any protocol" means, and what
# separates the mode from --connect: no port named anywhere, nothing in the
# command line, and a client that was never told it was in a sandbox.
set +e
RAW_OUT="$(PATH="$NVX_HOME/bin:$PATH" "$NVX" -y --strict shim node "$PROJ/client.js" "$SVC_PORT" 2>&1)"
RAWRC=$?
set -e
rm -f "$NVX_HOME/policy.json"
if [[ $RAWRC -ne 0 ]] || ! grep -q "GOT SERVICE_OK" <<<"$RAW_OUT"; then
  echo "$RAW_OUT" >&2
  echo "a raw connection to 127.0.0.1:$SVC_PORT was not carried in loopback mode (exit $RAWRC)" >&2
  exit 1
fi
echo "  a raw connection reached the service too"

kill $SVC_PID 2>/dev/null || true
trap 'rm -rf "$PROJ"' EXIT

# An actual install, which is what the sandbox is mostly for and what no test on
# this platform had ever run. Windows has had one since a contained `npm install`
# was found to hang forever; Linux and macOS asserted only that a contained
# `node -e` could write a file. An install exercises a different set of things --
# the runtime's own child processes, a writable HOME for the npm cache, the
# registry through the egress proxy -- and the docker provider turned out to fail
# on two of those the first time anyone tried one.
#
# No --strict: an install is contained at the default level, so this is the path
# a person actually takes.
echo "Installing a package through the sandbox..."
PKG="$PROJ/pkgtest"
mkdir -p "$PKG"
cd "$PKG"
printf '%s' '{"name":"probe","version":"1.0.0","dependencies":{"ms":"2.1.3"}}' > package.json
# The shim directory leads PATH, which is what `nvx env` and init-shims leave
# behind and the arrangement the README describes. It is also the one that
# broke: npm resolves node through PATH, found nvx's node SHIM there, and the
# shim could not resolve a version inside the sandbox because NVX_HOME is
# scrubbed and HOME is the throwaway guest profile. A session that had also run
# `nvx use` worked, which is why this went unseen.
set +e
INSTALL="$(PATH="$NVX_HOME/bin:$PATH" "$NVX" -y shim npm install 2>&1)"
IRC=$?
set -e
if [[ $IRC -ne 0 ]]; then
  echo "$INSTALL" >&2
  echo "a contained npm install failed (exit $IRC)" >&2
  exit 1
fi
# It was contained. Without this the install could pass by running outside the
# sandbox entirely, which is the failure this script exists to catch.
if ! grep -q "isolation active" <<<"$INSTALL"; then
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

# The sandbox has a /proc, and it is its own rather than the host's.
#
# Both halves matter. Without any /proc, Bun cannot run a script or an install
# at all -- it reads /proc/self to size its stack and reported "JSON document is
# too deeply nested" against a 65-byte package.json. With the HOST's /proc, a
# contained process could read every process on the machine: cmdline for all of
# them, environ for the user's own, which is where credentials are. So the
# assertion is: readable, and containing almost nothing.
cat > proc.js <<'JS'
const fs = require('fs');
let limits; try { limits = 'ok' } catch { limits = 'unreadable' }
try { fs.readFileSync('/proc/self/limits'); } catch (e) { limits = 'unreadable(' + e.code + ')'; }
const pids = fs.readdirSync('/proc').filter(n => /^\d+$/.test(n));
console.log('PROC_SELF=' + limits);
console.log('PROC_PIDS=' + pids.length);
JS
set +e
PROC="$("$NVX" -y --strict shim node proc.js 2>&1)"
PRC=$?
set -e
if [[ $PRC -ne 0 ]] || ! grep -q "PROC_SELF=ok" <<<"$PROC"; then
  echo "$PROC" >&2
  echo "a contained process cannot read its own /proc; Bun will not run in here" >&2
  exit 1
fi
PROC_PIDS="$(grep -oE "PROC_PIDS=[0-9]+" <<<"$PROC" | head -1 | cut -d= -f2)"
HOST_PIDS="$(ls -d /proc/[0-9]* | wc -l)"
if [[ -z "$PROC_PIDS" || "$PROC_PIDS" -gt 16 ]]; then
  echo "$PROC" >&2
  echo "the sandbox sees $PROC_PIDS processes (the host has $HOST_PIDS); that is the host's /proc, not its own" >&2
  exit 1
fi

echo "Linux sandbox smoke passed."
