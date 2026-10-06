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
  echo "Build nvx first: go build -o nvx ./cmd/nvx" >&2
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
#
# -U as well as -n, and for the same reason nvx's own supervisor needs
# CLONE_NEWUSER: an ordinary user has no CAP_SYS_ADMIN, so a bare `unshare -n`
# is refused even where nvx goes on to create the namespace successfully. This
# probe said "network namespaces unavailable" on a host where the sandbox then
# reported a loopback-only namespace three lines later -- so the one assertion
# that proves egress is blocked was skipped exactly where it would have run.
#
# It probes by bringing loopback up rather than by creating the namespace,
# because Ubuntu 24.04's AppArmor hardening permits the second and refuses the
# first, and nvx needs the first.
EGRESS_TESTABLE=1
if ! command -v ip >/dev/null 2>&1 || ! unshare -Urn -- ip link set lo up >/dev/null 2>&1; then
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
  # A host that genuinely cannot host the sandbox is not a failing product --
  # some distributions forbid unprivileged user namespaces outright, and there
  # nvx refuses to run rather than running uncontained, which is the fail-closed
  # stance working.
  #
  # But this branch must decide that by TESTING it, not by looking at the uid.
  # It used to skip whenever the run was unprivileged, and blamed Ubuntu's
  # AppArmor restriction for it. On 2026-08-23 that was wrong: user namespaces
  # were available, the sandbox started fine, and the target failed to exec for
  # an unrelated reason (a nested user namespace whose uid_map write Landlock
  # denied -- see applyLinuxNamespaces). The step went green, having asserted
  # nothing, and the misattribution in this very message is what the failure was
  # chased as. A probe that explains away its own silence is worse than one that
  # fails.
  if ! unshare -Urn -- ip link set lo up >/dev/null 2>&1; then
    echo "::warning::this host does not allow loopback to be configured inside an" >&2
    echo "unprivileged user namespace, which the sandbox requires; nvx fails closed" >&2
    echo "here. On Ubuntu 24.04 lift it with" >&2
    echo "  sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0" >&2
    echo "Skipping without asserting containment." >&2
    exit 0
  fi
  echo "FAIL: the contained probe wrote no report (nvx exit $rc)." >&2
  echo "      User namespaces ARE available here, so this is not an environment limit:" >&2
  echo "      either the sandbox refused to launch node, or it blocked the report write." >&2
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

# A contained run started ABOVE nvx's own directory must not be able to write it.
#
# The working directory is a writable root, and nothing checked which directory
# it was: from ~ a contained process wrote ~/.nvx/grants and ~/.bashrc. Measured
# on Linux and macOS before the guard. PROBE_ROOT holds NVX_HOME, so starting
# here is the same shape as starting in ~ with the default NVX_HOME.
HOME_WRITE="$NVX_HOME/grants-probe-from-above"
( cd "$PROBE_ROOT" && "$NVX" -y --strict shim node -e \
    "try{require('fs').writeFileSync(process.argv[1],'x')}catch(e){}" "$HOME_WRITE" >/dev/null 2>&1 ) || true
if [[ -e "$HOME_WRITE" ]]; then
  echo "FAIL: a contained run started above NVX_HOME wrote into it; the working directory reached nvx's own settings." >&2
  fail=1
fi

# The project's dotenv files are unreadable, and their templates are not.
#
# The project has to be readable for an install to work, and .env lives in it.
# The link, copy and rename checks try to get the same bytes out under a name
# nothing covers. The template and the plain file are the controls: without
# them a sandbox that refused every read in the project would pass. upper/.ENV
# is a file of its own here, and is covered like .env, as it is on macOS.
#
# A contained process may create a dotenv file. The supervisor covers it a
# moment later, and from then on the process cannot read it again, move it or
# delete it. DOTENV_NEW retries the read for up to 2s.
echo "Dotenv: dotenv files must be unreadable while templates stay readable..."
DOTENV_SECRET='API_KEY=DOTENV-SECRET-DO-NOT-LEAK'
mkdir -p "$PROJ/sub" "$PROJ/upper"
printf '%s\n' "$DOTENV_SECRET" > "$PROJ/.env"
printf '%s\n' "$DOTENV_SECRET" > "$PROJ/sub/.env.local"
printf '%s\n' "$DOTENV_SECRET" > "$PROJ/upper/.ENV"
printf 'API_KEY=\n' > "$PROJ/.env.example"
printf 'plain\n' > "$PROJ/plain.txt"
cat > dotenv.js <<'DOTENV'
const fs = require('fs');
const out = [];
const outcome = (e) => ['EPERM', 'EACCES', 'EROFS', 'EBUSY', 'EXDEV'].includes(e.code) ? 'DENIED' : 'ERROR ' + e.code;
function read(name, p) {
  try { fs.readFileSync(p); out.push(name + '=ALLOWED'); }
  catch (e) { out.push(name + '=' + outcome(e)); }
}
function act(name, fn) {
  try { fn(); out.push(name + '=ALLOWED'); }
  catch (e) { out.push(name + '=' + outcome(e)); }
}
// Puts the bytes under a name no rule matches, then reads them there.
function leak(name, fn, dst) {
  try { fn(); } catch (e) { out.push(name + '=' + outcome(e)); return; }
  try { out.push(name + (fs.readFileSync(dst, 'utf8').includes('DOTENV-SECRET') ? '=LEAKED' : '=DENIED')); }
  catch (e) { out.push(name + '=' + outcome(e)); }
}
read('DOTENV_ROOT', '.env');
read('DOTENV_SUB', 'sub/.env.local');
if (process.argv[3]) read('DOTENV_CASE', process.argv[3]);
read('DOTENV_TEMPLATE', '.env.example');
read('DOTENV_CONTROL', 'plain.txt');
act('DOTENV_STAT', () => fs.statSync('.env'));
act('DOTENV_WRITE', () => fs.appendFileSync('.env', 'INJECTED=1\n'));
act('DOTENV_CREATE', () => { fs.mkdirSync('fresh', { recursive: true }); fs.writeFileSync('fresh/.env', 'X=1\n'); });
// Reads until refused. A read that still succeeds after 2s means nothing covered it.
(function readUntilRefused(name, p) {
  const until = Date.now() + 2000;
  for (;;) {
    try { fs.readFileSync(p); } catch (e) { out.push(name + '=' + outcome(e)); return; }
    if (Date.now() > until) { out.push(name + '=ALLOWED'); return; }
  }
})('DOTENV_NEW', 'fresh/.env');
act('DOTENV_NEW_RENAME', () => fs.renameSync('fresh/.env', 'fresh-moved.txt'));
act('DOTENV_NEW_DELETE', () => fs.unlinkSync('fresh/.env'));
leak('DOTENV_LINK', () => fs.linkSync('.env', 'linked.txt'), 'linked.txt');
leak('DOTENV_COPY', () => fs.copyFileSync('.env', 'copied.txt', fs.constants.COPYFILE_FICLONE), 'copied.txt');
leak('DOTENV_RENAME', () => fs.renameSync('.env', 'renamed.txt'), 'renamed.txt');
fs.writeFileSync(process.argv[2], out.join('\n') + '\n');
DOTENV

DOTENV_REPORT="$PROJ/dotenv-report.txt"
"$NVX" -y --strict shim node dotenv.js "$DOTENV_REPORT" upper/.ENV >/dev/null 2>&1 || true
if [[ ! -f "$DOTENV_REPORT" ]]; then
  echo "FAIL: the contained dotenv probe wrote no report." >&2
  fail=1
else
  cat "$DOTENV_REPORT"
  expect_dotenv() {
    if ! grep -qx "$1" "$DOTENV_REPORT"; then
      echo "FAIL: expected $1 — $2" >&2
      fail=1
    fi
  }
  expect_dotenv "DOTENV_ROOT=DENIED"      "a contained process read the project's .env"
  expect_dotenv "DOTENV_SUB=DENIED"       "a contained process read sub/.env.local"
  expect_dotenv "DOTENV_CASE=DENIED"      "a contained process read upper/.ENV"
  expect_dotenv "DOTENV_TEMPLATE=ALLOWED" "a contained process could not read .env.example, a template that holds no secrets"
  expect_dotenv "DOTENV_CONTROL=ALLOWED"  "a contained process could not read a plain project file, so the denials above prove nothing"
  expect_dotenv "DOTENV_STAT=ALLOWED"     "a contained process could not stat .env; only its contents are meant to be hidden"
  expect_dotenv "DOTENV_WRITE=DENIED"     "a contained process wrote a .env that existed at launch"
  expect_dotenv "DOTENV_CREATE=ALLOWED"   "creating a new .env is documented as allowed on Linux; update the documents with this"
  expect_dotenv "DOTENV_NEW=DENIED"       "a contained process could still read a .env it created, 2s after creating it"
  expect_dotenv "DOTENV_NEW_RENAME=DENIED" "a contained process renamed a .env it created after the supervisor covered it"
  expect_dotenv "DOTENV_NEW_DELETE=DENIED" "a contained process deleted a .env it created after the supervisor covered it"
  expect_dotenv "DOTENV_LINK=DENIED"      "a contained process read .env through a hard link"
  expect_dotenv "DOTENV_COPY=DENIED"      "a contained process read .env through a copy"
  expect_dotenv "DOTENV_RENAME=DENIED"    "a contained process read .env after renaming it"
fi
if [[ "$(cat "$PROJ/.env" 2>/dev/null)" != "$DOTENV_SECRET" ]]; then
  echo "FAIL: .env changed or moved; a contained process wrote or renamed it." >&2
  fail=1
fi

if [[ $fail -ne 0 ]]; then
  echo "Linux enforcement probe FAILED." >&2
  exit 1
fi

if [[ "$EGRESS_TESTABLE" == "1" ]]; then
  echo "Linux enforcement probe passed: writes contained, reads restricted, egress denied."
else
  echo "Linux enforcement probe passed: writes contained, reads restricted. Egress NOT asserted."
fi
