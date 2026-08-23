#!/usr/bin/env bash
# Linux containment enforcement — does Landlock + netns actually enforce?
#
# The companion to scripts/sandbox-enforcement-macos.sh, and deliberately not
# identical to it. Linux restricts reads and macOS does not, so the two scripts
# assert opposite outcomes for the same attempt. That difference is the point:
# it is the strongest per-platform claim the project makes, and until now it
# rested on reading landlockReadOnlyRules rather than on watching it hold.
#
# The existing smoke script checks that a write to `process.env.HOME` does not
# land in the real home. Inside the sandbox HOME is the ephemeral guest profile,
# so that write is *supposed* to succeed and the check passes as long as the
# redirect works -- a sandbox with no write restriction at all would pass it.
# Everything here uses absolute paths for that reason.
#
# Both halves are asserted: what must be denied, and what must still be allowed.
# A sandbox that refuses everything is a broken launch, not enforcement, and only
# the positive controls tell them apart.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "Linux-only; skipping." >&2
  exit 0
fi
if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ." >&2
  exit 1
fi
if ! command -v node >/dev/null 2>&1; then
  echo "Node.js not available; skipping Linux enforcement probe." >&2
  exit 0
fi

# Landlock is what restricts the filesystem here. Without it there is nothing to
# measure, and saying so beats reporting a pass.
KERNEL="$(uname -r | cut -d. -f1-2)"
if [[ "$(printf '%s\n5.13\n' "$KERNEL" | sort -V | head -1)" != "5.13" ]]; then
  echo "Kernel $KERNEL predates Landlock (5.13+); skipping Linux enforcement probe." >&2
  exit 0
fi

# The network half needs a namespace. Its absence skips only the egress
# assertion -- the filesystem ones are still worth running.
EGRESS_TESTABLE=1
if ! unshare -n -- true 2>/dev/null; then
  echo "::warning::network namespaces unavailable; the egress assertion will be skipped" >&2
  EGRESS_TESTABLE=0
fi

# Not mktemp: hosted runners mount /tmp noexec, so a runtime installed there
# cannot be executed at all -- "permission denied" on fork/exec, before Landlock
# is even consulted. The home directory is on an ordinary filesystem.
PROBE_ROOT="$HOME/.nvx-enforcement-probe"
rm -rf "$PROBE_ROOT"
mkdir -p "$PROBE_ROOT"
PROJ="$PROBE_ROOT/project"
mkdir -p "$PROJ"

# Not mktemp for the fixture. The writable roots are the guest home and the work
# directory, and on macOS the same mistake put the fixture inside an allowed
# temp root and reported a false escape. The real home is outside the read
# allowlist (/usr /lib /lib64 /bin /sbin /etc and specific /dev nodes) and
# outside every writable root, so it tests both directions at once.
OUTSIDE="$PROBE_ROOT/outside"
mkdir -p "$OUTSIDE"
trap 'rm -rf "$PROBE_ROOT"' EXIT

SECRET="$OUTSIDE/credentials"
printf 'SECRET-CONTENT-DO-NOT-LEAK\n' > "$SECRET"
FORBIDDEN_WRITE="$OUTSIDE/should-not-exist"

# A runtime Landlock actually permits.
#
# landlockReadOnlyRules grants read+exec on /usr /lib /lib64 /bin /sbin /etc and
# on nvx's own versions/, bin/ and current/ -- and nothing else, because it is an
# allowlist with no deny rule. The hosted runner's Node lives in
# /opt/hostedtoolcache, so a contained process cannot exec it: the first
# privileged run of this probe got "fork/exec .../node: permission denied" with
# the sandbox working exactly as specified.
#
# Installing an nvx-managed runtime is both the fix and the realistic case --
# managing runtimes is what nvx is for. NVX_HOME is scratch so a developer
# running this does not gain a runtime in their real ~/.nvx.
export NVX_HOME="$PROBE_ROOT/nvxhome"
mkdir -p "$NVX_HOME"
echo "Installing an nvx-managed runtime (Landlock does not permit exec outside its allowlist)..."
if ! "$NVX" -y install 22 >/dev/null 2>&1 || ! "$NVX" -y default 22 >/dev/null 2>&1; then
  echo "::warning::could not install an nvx-managed runtime (network?); skipping Linux enforcement probe" >&2
  exit 0
fi

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
// Arguments, not environment: nvx scrubs the environment on the way into the
// sandbox, so anything set outside it arrives undefined.
const secret = process.argv[2];
const forbidden = process.argv[3];
const report = process.argv[4];
const testEgress = process.argv[5] === '1';

try { fs.writeFileSync(forbidden, 'escaped'); out.push('WRITE_OUTSIDE=ALLOWED'); }
catch (e) { out.push('WRITE_OUTSIDE=DENIED'); }

try { fs.writeFileSync('inside.txt', 'ok'); out.push('WRITE_INSIDE=ALLOWED'); }
catch (e) { out.push('WRITE_INSIDE=DENIED'); }

// The claim that separates Linux from macOS: Landlock is an allowlist and the
// home directory is not on it.
try {
  const got = fs.readFileSync(secret, 'utf8');
  out.push(got.includes('SECRET-CONTENT') ? 'READ_OUTSIDE=ALLOWED' : 'READ_OUTSIDE=GARBLED');
} catch (e) { out.push('READ_OUTSIDE=DENIED'); }

// Reading its own project must still work, or "reads are restricted" would be
// indistinguishable from a sandbox that reads nothing.
try {
  fs.readFileSync('probe.js', 'utf8');
  out.push('READ_INSIDE=ALLOWED');
} catch (e) { out.push('READ_INSIDE=DENIED'); }

function finish() {
  fs.writeFileSync(report, out.join('\n') + '\n');
  process.exit(0);
}

if (!testEgress) { out.push('EGRESS=SKIPPED'); finish(); }

const req = https.get('https://example.com', () => { out.push('EGRESS=ALLOWED'); done(); });
req.on('error', () => { out.push('EGRESS=DENIED'); done(); });
req.setTimeout(15000, () => { req.destroy(); out.push('EGRESS=TIMEOUT'); done(); });

let finished = false;
function done() {
  if (finished) return;
  finished = true;
  finish();
}
PROBE

REPORT="$PROJ/report.txt"
echo "Running contained probe..."
set +e
"$NVX" -y --strict shim node probe.js "$SECRET" "$FORBIDDEN_WRITE" "$REPORT" "$EGRESS_TESTABLE"
rc=$?
set -e

if [[ ! -f "$REPORT" ]]; then
  # A host that cannot host the sandbox is not a failing product. Ubuntu 24.04
  # restricts unprivileged user namespaces via AppArmor, so nvx's Landlock
  # launch fails with "operation not permitted" and it refuses to run rather
  # than running uncontained -- the fail-closed stance working. CI runs this
  # step privileged for that reason; an unprivileged run says so and skips,
  # exactly as the Windows probes do when a runner refuses AppContainers.
  if [[ "$(id -u)" != "0" ]]; then
    echo "::warning::the sandbox could not launch unprivileged on this host (Ubuntu restricts" >&2
    echo "unprivileged user namespaces); re-run as root to assert containment. Skipping." >&2
    exit 0
  fi
  echo "FAIL: the contained probe wrote no report (nvx exit $rc), running as root." >&2
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

expect "WRITE_OUTSIDE=DENIED" "a contained process wrote outside the project and the guest home"
expect "WRITE_INSIDE=ALLOWED" "a contained process could not write its own project, so the sandbox is broken rather than strict, and every denial here proves nothing"
expect "READ_OUTSIDE=DENIED"  "a contained process read a file in the home directory; Landlock is an allowlist and the home directory is not on it. This is the claim that distinguishes Linux from macOS"
expect "READ_INSIDE=ALLOWED"  "a contained process could not read its own project, so 'reads are restricted' cannot be told apart from a sandbox that reads nothing"

if [[ "$EGRESS_TESTABLE" == "1" ]]; then
  expect "EGRESS=DENIED" "a contained process reached a host with an empty allowlist"
else
  echo "note: egress not asserted (no network namespace on this host)" >&2
fi

# The file must genuinely be absent, not merely reported as denied by a probe
# that lied to itself.
if [[ -e "$FORBIDDEN_WRITE" ]]; then
  echo "FAIL: the forbidden path exists on disk; the write escaped the sandbox." >&2
  fail=1
fi

if [[ $fail -ne 0 ]]; then
  echo "Linux enforcement probe FAILED." >&2
  exit 1
fi

echo "Linux enforcement probe passed: writes contained, reads restricted, egress denied."
