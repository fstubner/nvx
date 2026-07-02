#!/usr/bin/env bash
# Egress block smoke — sandboxed fetch to non-allowlisted host must fail
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ." >&2
  exit 1
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

echo "Testing blocked egress via sandboxed node..."
if "$NVX" shim node -e "require('https').get('https://example.com',()=>process.exit(0)).on('error',()=>process.exit(1))"; then
  echo "expected blocked egress to fail" >&2
  exit 1
fi

echo "Egress block smoke passed."
