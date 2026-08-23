#!/usr/bin/env bash
# macOS containment enforcement — does the Seatbelt profile actually enforce?
#
# The existing macOS smoke test checks that a sandboxed process can write its own
# working directory. That would pass against a sandbox blocking nothing, which is
# why SECURITY.md has had to say macOS is "intended-and-untested" while every
# equivalent Windows claim is backed by a probe.
#
# This asserts the things that must be DENIED, plus the things that must still be
# ALLOWED. Both halves are required: a sandbox that refuses everything is not
# enforcement, it is a broken launch, and only the positive controls tell them
# apart. That distinction is not hypothetical here -- a Windows egress test once
# reported success while the sandbox was blocking its own test server.
#
# It also asserts a WEAKNESS on purpose. macOS allows filesystem reads, so a
# contained process can read credentials by absolute path. That is deliberate
# (the dynamic linker needs system libraries whose paths vary by OS version, and
# a strict read allowlist stops processes launching) and it is documented in
# README, SECURITY.md, PRODUCT.md and docs/enforcement-matrix.md. Pinning it here
# means that if the profile is ever tightened, this fails and forces those four
# documents to be updated together -- rather than the docs quietly staying wrong
# in either direction.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS-only; skipping." >&2
  exit 0
fi
if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ." >&2
  exit 1
fi
if ! command -v node >/dev/null 2>&1; then
  echo "Node.js not available; skipping macOS enforcement probe." >&2
  exit 0
fi

# Its absence is a finding, not a reason to pass quietly: nvx's macOS containment
# is built on sandbox-exec, which Apple has deprecated. If a future runner image
# drops it, this should be loud.
if [[ ! -x /usr/bin/sandbox-exec ]]; then
  echo "FAIL: /usr/bin/sandbox-exec is missing. nvx's macOS containment cannot work here," >&2
  echo "      and any claim that macOS is sandboxed would be false on this host." >&2
  exit 1
fi

PROJ="$(mktemp -d)"
OUTSIDE="$(mktemp -d)"
trap 'rm -rf "$PROJ" "$OUTSIDE"' EXIT

# The secret lives outside the project, reachable only by absolute path. Not in
# $HOME, because nvx redirects HOME to an ephemeral guest profile and a test that
# went through `~` would be measuring the redirect rather than the sandbox.
SECRET="$OUTSIDE/credentials"
printf 'SECRET-CONTENT-DO-NOT-LEAK\n' > "$SECRET"
FORBIDDEN_WRITE="$OUTSIDE/should-not-exist"

cd "$PROJ"
cat > .nvx-policy.json <<'POLICY'
{
  "isolation": {
    "enabled": true,
    "level": "strict",
    "network": {
      "mode": "proxy",
      "default_allow": [],
      "prompt_unknown": false
    }
  }
}
POLICY

cat > probe.js <<'PROBE'
const fs = require('fs');
const https = require('https');
const out = [];
// Arguments, not environment variables: nvx scrubs the environment on the way
// into the sandbox, which is the point of it. The first version of this probe
// passed PROBE_* vars and they arrived undefined -- the containment working
// exactly as designed, breaking the test measuring it.
const secret = process.argv[2];
const forbidden = process.argv[3];
const report = process.argv[4];

// Must be DENIED: a write outside the project is the guarantee macOS makes.
try { fs.writeFileSync(forbidden, 'escaped'); out.push('WRITE_OUTSIDE=ALLOWED'); }
catch (e) { out.push('WRITE_OUTSIDE=DENIED'); }

// Must be ALLOWED: the control that stops "denies everything" passing as
// enforcement.
try { fs.writeFileSync('inside.txt', 'ok'); out.push('WRITE_INSIDE=ALLOWED'); }
catch (e) { out.push('WRITE_INSIDE=DENIED'); }

// Documented as ALLOWED on macOS. Asserted so a change in either direction is
// caught rather than silently diverging from four documents.
try {
  const got = fs.readFileSync(secret, 'utf8');
  out.push(got.includes('SECRET-CONTENT') ? 'READ_OUTSIDE=ALLOWED' : 'READ_OUTSIDE=GARBLED');
} catch (e) { out.push('READ_OUTSIDE=DENIED'); }

// Must be DENIED: no host is allowlisted, so this must not complete.
const req = https.get('https://example.com', () => { out.push('EGRESS=ALLOWED'); done(); });
req.on('error', () => { out.push('EGRESS=DENIED'); done(); });
req.setTimeout(15000, () => { req.destroy(); out.push('EGRESS=TIMEOUT'); done(); });

let finished = false;
function done() {
  if (finished) return;
  finished = true;
  fs.writeFileSync(report, out.join('\n') + '\n');
  process.exit(0);
}
PROBE

REPORT="$PROJ/report.txt"
echo "Running contained probe..."
set +e
"$NVX" -y --strict shim node probe.js "$SECRET" "$FORBIDDEN_WRITE" "$REPORT"
rc=$?
set -e

if [[ ! -f "$REPORT" ]]; then
  echo "FAIL: the contained probe wrote no report (nvx exit $rc)." >&2
  echo "      Either the sandbox refused to launch node, or it blocked the report write." >&2
  exit 1
fi

echo "--- contained process reported ---"
cat "$REPORT"
echo "----------------------------------"

fail=0
expect() {
  local want="$1" why="$2"
  if ! grep -qx "$want" "$REPORT"; then
    echo "FAIL: expected $want — $why" >&2
    fail=1
  fi
}

expect "WRITE_OUTSIDE=DENIED" "a contained process wrote outside the project; write containment is the guarantee macOS makes"
expect "WRITE_INSIDE=ALLOWED" "a contained process could not write its own project, so the sandbox is broken rather than strict, and every denial above proves nothing"
expect "EGRESS=DENIED"        "a contained process reached a host with an empty allowlist; egress control is the other guarantee macOS makes"

# The documented weakness. A change here is not necessarily a regression -- it
# may be an improvement -- but it must not go unnoticed, because four documents
# describe the current behaviour.
if ! grep -qx "READ_OUTSIDE=ALLOWED" "$REPORT"; then
  echo "FAIL: reads outside the project are no longer allowed on macOS." >&2
  echo "      That may be an improvement, but README, SECURITY.md, PRODUCT.md and" >&2
  echo "      docs/enforcement-matrix.md all state that macOS does NOT contain reads." >&2
  echo "      Update them in the same change that tightened the profile." >&2
  fail=1
fi

# Belt and braces: the file must genuinely still be absent, not merely reported
# as denied by a probe that lied to itself.
if [[ -e "$FORBIDDEN_WRITE" ]]; then
  echo "FAIL: the forbidden path exists on disk; the write escaped the sandbox." >&2
  fail=1
fi

if [[ $fail -ne 0 ]]; then
  echo "macOS enforcement probe FAILED." >&2
  exit 1
fi

echo "macOS enforcement probe passed: writes contained, egress denied, reads allowed as documented."
