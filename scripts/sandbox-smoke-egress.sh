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

FETCH="require('https').get('https://example.com',()=>process.exit(0)).on('error',()=>process.exit(1))"

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
if "$NVX" -y --strict shim node -e "$FETCH"; then
  echo "expected blocked egress to fail" >&2
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

if ! "$NVX" -y --strict shim node -e "$FETCH"; then
  echo "an allowlisted host was blocked; the sandbox is denying everything, not enforcing a policy" >&2
  echo "(this phase needs outbound network access; a offline CI runner will fail here)" >&2
  exit 1
fi

echo "Egress smoke passed: denied what it should, allowed what it should."
