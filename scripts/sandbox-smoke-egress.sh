#!/usr/bin/env bash
# Egress block smoke — sandboxed fetch to non-allowlisted host must fail
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ." >&2
  exit 1
fi

if [[ "$(uname -s)" == "Linux" ]] && ! unshare -n -- true 2>/dev/null; then
  echo "Network namespace unavailable; skipping egress smoke." >&2
  exit 0
fi

if [[ "$(uname -s)" == "Linux" ]] && [[ "$(uname -r | cut -d. -f1-2)" < "5.13" ]]; then
  echo "Skipping egress smoke (Landlock/network namespace requires kernel 5.13+)." >&2
  exit 0
fi

PROJ="$(mktemp -d)"
trap 'rm -rf "$PROJ"' EXIT
cd "$PROJ"

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

echo "Phase 1: a non-allowlisted host must be blocked..."
if "$NVX" shim node -e "$FETCH"; then
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

if ! "$NVX" shim node -e "$FETCH"; then
  echo "an allowlisted host was blocked; the sandbox is denying everything, not enforcing a policy" >&2
  echo "(this phase needs outbound network access; a offline CI runner will fail here)" >&2
  exit 1
fi

echo "Egress smoke passed: denied what it should, allowed what it should."
