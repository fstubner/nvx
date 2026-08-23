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

# -U as well as -n. nvx asks for the network namespace together with a user
# namespace, which is what gives an ordinary user the CAP_SYS_ADMIN to request
# one; a bare `unshare -n` is refused for anyone but root and made this smoke
# test skip on every unprivileged machine, including the CI runner it was added
# for.
if ! unshare -Un -- true 2>/dev/null; then
  echo "Network namespace unavailable in this environment; skipping Linux sandbox smoke." >&2
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

echo "Linux sandbox smoke passed."
