#!/usr/bin/env bash
# Egress block smoke — sandboxed fetch to non-allowlisted host must fail
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ." >&2
  exit 1
fi

# The precondition the sandbox actually has, probed the way it will use it --
# see scripts/sandbox-smoke.sh for why namespace creation is the wrong question.
if [[ "$(uname -s)" == "Linux" ]]; then
  if ! command -v ip >/dev/null 2>&1; then
    echo "iproute2 not installed; nvx's loopback setup needs \`ip\`. Skipping." >&2
    exit 0
  fi
  if ! unshare -Urn -- ip link set lo up >/dev/null 2>&1; then
    echo "This host does not allow loopback to be configured inside an unprivileged" >&2
    echo "user namespace, so nvx's network isolation cannot start and it fails closed." >&2
    echo "On Ubuntu 24.04 this is AppArmor; lift it with:" >&2
    echo "  sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0" >&2
    echo "Skipping egress smoke." >&2
    exit 0
  fi
fi

if [[ "$(uname -s)" == "Linux" ]] && [[ "$(uname -r | cut -d. -f1-2)" < "5.13" ]]; then
  echo "Skipping egress smoke (Landlock/network namespace requires kernel 5.13+)." >&2
  exit 0
fi

PROJ="$(mktemp -d)"
trap 'rm -rf "$PROJ"' EXIT
cd "$PROJ"

# An nvx-managed runtime, for the reason given in
# scripts/sandbox-enforcement-linux.sh: Landlock permits exec only beneath its
# allowlist, and a hosted runner's Node is in /opt/hostedtoolcache, which is
# outside it. This script skipped on every unprivileged machine until now, so it
# never met the problem.
export NVX_HOME="$PROJ/nvxhome"
mkdir -p "$NVX_HOME"
echo "Installing an nvx-managed runtime (Landlock does not permit exec outside its allowlist)..."
if ! "$NVX" -y install 22 >/dev/null 2>&1 || ! "$NVX" -y default 22 >/dev/null 2>&1; then
  echo "::warning::could not install an nvx-managed runtime (network?); skipping egress smoke" >&2
  exit 0
fi

cat > .nvx-policy.json <<'EOF'
{
  "isolation": {
    "enabled": true,
    "network": {
      "mode": "proxy",
      "default_allow": [],
      "prompt_unknown": false
    }
  }
}
EOF

"$NVX" init-shims >/dev/null

# Ask the proxy for a tunnel, rather than making an ordinary HTTPS request and
# hoping it goes through the proxy.
#
# It did not. Node's classic `https.get` ignores HTTPS_PROXY -- nothing in core
# reads it -- so the old probe attempted a direct connection every time. Inside a
# loopback-only network namespace that dies at DNS, which made phase 1 pass
# without the allowlist being consulted at all, and made phase 2 impossible to
# pass however correct the allowlist was.
#
# CONNECT is what a proxy-aware client sends, and its status code is the
# allowlist decision itself: 200 tunnel established, 403 refused. That also
# distinguishes "refused by policy" from "could not reach the proxy", which an
# exit code cannot.
CONNECT=$(cat <<'JS'
const http = require('http');
const u = new URL(process.env.HTTPS_PROXY);
const req = http.request({
  host: u.hostname, port: u.port, method: 'CONNECT', path: 'example.com:443',
  headers: { 'Proxy-Authorization': 'Basic ' +
    Buffer.from(decodeURIComponent(u.username) + ':' + decodeURIComponent(u.password)).toString('base64') },
});
req.on('connect', (res, socket) => { socket.destroy(); console.log('CONNECT=' + res.statusCode); process.exit(0); });
req.on('response', res => { console.log('CONNECT=' + res.statusCode); process.exit(0); });
req.on('error', e => { console.log('CONNECT=error ' + e.message); process.exit(0); });
req.end();
JS
)

# Phase 2's policy adds an allowlist entry, which widens what the sandbox permits,
# and nvx refuses to honour a widening policy it has not been told to trust --
# deliberately, and -y does not cover it. Without this the policy was ignored, the
# fetch ran uncontained, and phase 2 measured the host's internet connection
# rather than the allowlist. This script wrote the policy a few lines above, so
# trusting it here is not the case that guard exists for.
export NVX_TRUST_YES=true

# --strict for the same reason as scripts/sandbox-smoke.sh: the default policy
# does not contain an arbitrary temp directory, so every run below reported
# "Running directly (not sandboxed)".

echo "Phase 1: a non-allowlisted host must be blocked..."
OUT1="$("$NVX" -y --strict shim node -e "$CONNECT" 2>&1 | grep '^CONNECT=' || true)"
echo "  proxy said: ${OUT1:-<nothing>}"
if [[ "$OUT1" == "CONNECT=200" ]]; then
  echo "a host with an empty allowlist was tunnelled anyway" >&2
  exit 1
fi
if [[ -z "$OUT1" ]]; then
  echo "the contained process never reached the proxy, so the allowlist was not exercised" >&2
  exit 1
fi

# Phase 2 is the half that was missing. Asserting only that blocked traffic fails
# is satisfied perfectly by a sandbox that denies everything -- including by one
# that cannot start a process at all. Allowlisting the same host and requiring it
# to SUCCEED is what distinguishes enforcement from breakage.
echo "Phase 2: the same host, allowlisted, must succeed..."
cat > .nvx-policy.json <<'EOF'
{
  "isolation": {
    "enabled": true,
    "network": {
      "mode": "proxy",
      "default_allow": ["example.com:443"],
      "prompt_unknown": false
    }
  }
}
EOF

OUT2="$("$NVX" -y --strict shim node -e "$CONNECT" 2>&1 | grep '^CONNECT=' || true)"
echo "  proxy said: ${OUT2:-<nothing>}"
case "$OUT2" in
  CONNECT=200) ;;
  CONNECT=403)
    echo "an allowlisted host was refused by the allowlist; the sandbox is denying" >&2
    echo "everything, not enforcing a policy. This is the regression this phase exists for." >&2
    exit 1
    ;;
  CONNECT=502)
    # The proxy accepted the request and failed to reach the host itself, so the
    # allowlist did permit it -- which is the claim being made here. Separating
    # this from 403 is the point of reading the status instead of an exit code:
    # a machine with no outbound DNS would otherwise look identical to a broken
    # allowlist. Measured on WSL2, where the uncontained control cannot resolve
    # the host either.
    echo "note: the proxy allowed the host but could not reach it (502); this host has" >&2
    echo "no outbound access. The allowlist decision was correct in both phases." >&2
    ;;
  *)
    echo "unexpected proxy response for an allowlisted host: ${OUT2:-<nothing>}" >&2
    exit 1
    ;;
esac

echo "Egress smoke passed: denied what it should, allowed what it should."
