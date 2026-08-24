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

# NOT mktemp for the "outside" fixture. buildSeatbeltProfile grants write access
# to /dev, /private/tmp, /private/var/tmp and /private/var/folders so a contained
# process has a usable temp directory -- and macOS mktemp returns a path under
# /var/folders, which is a symlink into /private/var/folders. The first version
# of this probe put its forbidden path there and reported that the sandbox had
# been escaped, when the write had landed in a root the profile deliberately
# allows.
#
# The real home is genuinely outside every write root. This script runs outside
# the sandbox, so $HOME here is the actual home; the contained process gets an
# ephemeral one, which is why the paths below are passed absolute rather than
# through `~`.
OUTSIDE="$HOME/.nvx-enforcement-probe"
rm -rf "$OUTSIDE"
mkdir -p "$OUTSIDE"
trap 'rm -rf "$PROJ" "$OUTSIDE"' EXIT

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

// Must be DENIED: UDP to an external host. Asserted separately from TCP because
// the profile's `(deny default)` covers both and nothing checked the second, so
// "raw TCP/UDP blocked" was half measured and half assumed. A missing reply
// would not count -- an unanswered packet looks exactly like a delivered one --
// so only an error from the OS counts as denied.
//
// It is refused at BIND, not at send: sending on an unbound UDP socket makes
// node bind one implicitly, and Seatbelt rejects that with EPERM on 0.0.0.0.
// That is a stronger refusal than the send-level one this expected, and it
// arrives as an 'error' EVENT -- without this handler it is an unhandled error
// that kills node before the report is written, which is how the first version
// of this check failed on a real runner rather than recording a pass.
const dgram = require('dgram');
const sock = dgram.createSocket('udp4');
let udpDone = false;
function udp(result) {
  if (udpDone) return;
  udpDone = true;
  out.push('UDP_EGRESS=' + result);
  try { sock.close(); } catch (e) {}
  step();
}
sock.on('error', () => udp('DENIED'));
sock.send(Buffer.from('x'), 53, '1.1.1.1', (err) => udp(err ? 'DENIED' : 'ALLOWED'));
setTimeout(() => udp('TIMEOUT'), 8000);

// Must be DENIED: no host is allowlisted, so this must not complete.
const req = https.get('https://example.com', () => { out.push('EGRESS=ALLOWED'); step(); });
req.on('error', () => { out.push('EGRESS=DENIED'); step(); });
req.setTimeout(15000, () => { req.destroy(); out.push('EGRESS=TIMEOUT'); step(); });

// Both async checks must land before the report is written, or whichever
// finishes second is missing from it and reads as a failed assertion.
let pending = 2;
function step() {
  if (--pending > 0) return;
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
expect "UDP_EGRESS=DENIED"    "a contained process sent a UDP packet to an external host; the profile is (deny default) and that must cover UDP as well as TCP"

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

# Phase 2: the direction a denial-only check cannot reach.
#
# Everything above runs with an EMPTY allowlist, so it can only ever show that
# things are refused -- which a sandbox that had failed to start would also show.
# Allowlisting a host and requiring it to SUCCEED is what separates enforcement
# from breakage, and it was the largest macOS cell still resting on the profile's
# text rather than on a runner.
#
# CONNECT to the proxy directly rather than an ordinary HTTPS request: Node's
# classic https API ignores HTTPS_PROXY, so a plain request goes direct and is
# refused no matter how correct the allowlist is. Its status code IS the
# allowlist decision -- 200 tunnelled, 403 refused -- which also tells a refusal
# apart from an unreachable proxy, as an exit code cannot.
echo "Phase 2: an allowlisted host must be reachable through the proxy..."
cat > .nvx-policy.json <<'POLICY'
{
  "isolation": {
    "enabled": true,
    "level": "strict",
    "network": {
      "mode": "proxy",
      "default_allow": ["example.com:443"],
      "prompt_unknown": false
    }
  }
}
POLICY

cat > connect.js <<'CONNECT'
const http = require('http');
const raw = process.env.HTTPS_PROXY || process.env.https_proxy || '';
if (!raw) { console.log('CONNECT=no-proxy-env'); process.exit(0); }
const u = new URL(raw);
const req = http.request({
  host: u.hostname, port: u.port, method: 'CONNECT', path: 'example.com:443',
  headers: { 'Proxy-Authorization': 'Basic ' +
    Buffer.from(decodeURIComponent(u.username) + ':' + decodeURIComponent(u.password)).toString('base64') },
});
req.on('connect', (res, socket) => { socket.destroy(); console.log('CONNECT=' + res.statusCode); process.exit(0); });
req.on('response', res => { console.log('CONNECT=' + res.statusCode); process.exit(0); });
req.on('error', e => { console.log('CONNECT=error ' + e.message); process.exit(0); });
req.setTimeout(20000, () => { req.destroy(); console.log('CONNECT=timeout'); process.exit(0); });
req.end();
CONNECT

# The policy above widens what the sandbox may reach, and nvx refuses to honour a
# widening policy it has not been told to trust. This script wrote it a few lines
# up, which is not the case that guard exists for.
export NVX_TRUST_YES=true
OUT2="$("$NVX" -y --strict shim node connect.js 2>&1 | grep '^CONNECT=' || true)"
echo "  proxy said: ${OUT2:-<nothing>}"
case "$OUT2" in
  CONNECT=200) ;;
  CONNECT=403)
    echo "FAIL: an allowlisted host was refused by the allowlist. The sandbox is denying" >&2
    echo "      everything rather than enforcing a policy, which every check above would" >&2
    echo "      have passed regardless. This is the regression this phase exists for." >&2
    fail=1
    ;;
  CONNECT=502)
    # The proxy accepted the request and could not reach the host itself, so the
    # allowlist did permit it -- which is the claim. Reading the status rather
    # than an exit code is what makes this distinguishable from a refusal; an
    # offline runner would otherwise look like a broken allowlist.
    echo "note: the proxy allowed the host but could not reach it (502); this runner has no" >&2
    echo "      outbound access. The allowlist decision was still correct." >&2
    ;;
  *)
    echo "FAIL: unexpected proxy response for an allowlisted host: ${OUT2:-<nothing>}" >&2
    fail=1
    ;;
esac

if [[ $fail -ne 0 ]]; then
  echo "macOS enforcement probe FAILED." >&2
  exit 1
fi

echo "macOS enforcement probe passed: writes contained, egress denied for TCP and UDP,"
echo "an allowlisted host reachable through the proxy, reads allowed as documented."
