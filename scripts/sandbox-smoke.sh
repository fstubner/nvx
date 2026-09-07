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

echo "Linux sandbox smoke passed."
