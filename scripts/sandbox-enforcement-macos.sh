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
# Reads are allowed outside the home directory, because the dynamic linker needs
# system libraries whose paths vary by OS version. Under the home directory they
# are denied except for the project, the guest home, nvx's runtimes and any
# allow_read_exec root. Until 2026-10-06 a contained process could read every
# file in the home directory outside the credential stores, other projects
# included, and this script asserted that as a documented weakness. It now
# requires such a read to be refused, with the project, the runtime and an
# allow_read_exec root still readable as the controls. The credential stores
# are denied as well, and phase 3 asserts that.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS-only; skipping." >&2
  exit 0
fi
if [[ ! -x "$NVX" ]]; then
  echo "Build nvx first: go build -o nvx ./cmd/nvx" >&2
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

# The project, nvx's home and an allow_read_exec root all sit under the real
# home, the layout a developer has, so the reads the profile reopens under the
# home are the ones exercised here. mktemp would put them under
# /private/var/folders, outside the home, where nothing is reopened because
# nothing was denied. sandbox-smoke-macos.sh covers an NVX_HOME outside the
# home.
PROJ="$HOME/nvx-enforcement-project-$$"
export NVX_HOME="$HOME/.nvx-enforcement-home-$$"
READ_EXEC_DIR="$HOME/nvx-enforcement-tool-$$"
rm -rf "$PROJ" "$NVX_HOME" "$READ_EXEC_DIR"
mkdir -p "$PROJ" "$NVX_HOME" "$READ_EXEC_DIR"
printf 'tool-file\n' > "$READ_EXEC_DIR/tool.txt"
# Inside nvx's home but outside its runtimes: the control plane, which holds
# grants, policy and other tools' credentials.
printf 'control-plane\n' > "$NVX_HOME/probe-control-plane"

# NOT mktemp for the "outside" fixture. Until 2026-10-06 buildSeatbeltProfile
# granted writes on all of /private/var/folders, where macOS mktemp puts its
# directories, and the first version of this probe put its forbidden path there
# and reported an escape that was the profile working as it then stood. The
# shared temp trees are now asserted separately below.
#
# The real home is genuinely outside every write root. This script runs outside
# the sandbox, so $HOME here is the actual home; the contained process gets an
# ephemeral one, which is why the paths below are passed absolute rather than
# through `~`.
OUTSIDE="$HOME/.nvx-enforcement-probe"
rm -rf "$OUTSIDE"
mkdir -p "$OUTSIDE"
# A throwaway home for phase 3, holding planted stand-ins for credential files
# so the real ones are never read or written.
FAKE_HOME="$(mktemp -d)"

# The shared temp and cache trees that uncontained programs read back: the
# system temp directories, and this user's own Darwin temp and cache
# directories, which every app the user runs keeps files in. A contained process
# gets its own temp directory in the guest home through TMPDIR, so it has no
# need to write any of these. getconf asks libSystem, which is where Apple's
# frameworks find these directories without reading TMPDIR.
PROBE_TAG="nvx-enforcement-probe-$$"
SHARED_TEMP_TARGETS=(
  "/private/tmp/$PROBE_TAG"
  "/private/var/tmp/$PROBE_TAG"
  "$(getconf DARWIN_USER_TEMP_DIR)$PROBE_TAG"
  "$(getconf DARWIN_USER_CACHE_DIR)$PROBE_TAG"
)
trap 'rm -rf "$PROJ" "$NVX_HOME" "$READ_EXEC_DIR" "$OUTSIDE" "$FAKE_HOME" "${SHARED_TEMP_TARGETS[@]}"' EXIT

# A file in the home directory that is no credential store, standing in for
# another project's source or secrets.
SECRET="$OUTSIDE/credentials"
printf 'SECRET-CONTENT-DO-NOT-LEAK\n' > "$SECRET"
FORBIDDEN_WRITE="$OUTSIDE/should-not-exist"

# An nvx-managed runtime under NVX_HOME, as the Linux probe uses. The runner's
# own node is under /Users/runner/hostedtoolcache, inside the home, which a
# contained process may not read. Running nvx's runtime is also the realistic
# case, since managing runtimes is what nvx is for.
cd "$PROJ"
echo "Installing an nvx-managed runtime..."
if ! "$NVX" -y install 22 >/dev/null 2>&1 || ! "$NVX" -y default 22 >/dev/null 2>&1; then
  echo "FAIL: could not install an nvx-managed runtime into $NVX_HOME (network?)." >&2
  echo "      Every contained run below needs one, so nothing here can be checked." >&2
  exit 1
fi

# The policies below add an allow_read_exec root and an allowlisted host, and
# nvx refuses to honour a widening policy it has not been told to trust. This
# script writes them itself, which is not the case that guard exists for.
export NVX_TRUST_YES=true

printf 'project-file\n' > "$PROJ/project-file.txt"
# Repository metadata. git runs uncontained, so a contained process must not be
# able to write it, while reading it still works.
mkdir -p .git/hooks
GIT_CONFIG_BODY="$(printf '[core]\n\trepositoryformatversion = 0')"
printf '%s\n' "$GIT_CONFIG_BODY" > .git/config
cat > .nvx-policy.json <<POLICY
{
  "isolation": {
    "enabled": true,
    "level": "strict",
    "filesystem": {
      "allow_read_exec": ["$READ_EXEC_DIR"]
    },
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

// Must be DENIED: the project's .git is read-only to a contained process. A
// hook or a config entry written there runs as the user on the next commit.
try { fs.writeFileSync('.git/hooks/pre-commit', '#!/bin/sh\n'); out.push('GIT_HOOK_WRITE=ALLOWED'); }
catch (e) { out.push('GIT_HOOK_WRITE=DENIED'); }
try { fs.appendFileSync('.git/config', '[core]\n\thooksPath = elsewhere\n'); out.push('GIT_CONFIG_WRITE=ALLOWED'); }
catch (e) { out.push('GIT_CONFIG_WRITE=DENIED'); }

// Must be ALLOWED: reading .git, and the files an install writes. Without these
// a profile that denied the whole project would pass the two checks above.
try { fs.readFileSync('.git/config', 'utf8'); out.push('GIT_READ=ALLOWED'); }
catch (e) { out.push('GIT_READ=DENIED'); }
try {
  fs.writeFileSync('package.json', '{"name":"probe"}\n');
  fs.mkdirSync('node_modules/dep', { recursive: true });
  fs.writeFileSync('node_modules/dep/index.js', 'module.exports = 1;\n');
  out.push('INSTALL_WRITE=ALLOWED');
} catch (e) { out.push('INSTALL_WRITE=DENIED'); }

// Must be DENIED: the shared temp and cache trees, in the order the script
// passes them. Uncontained programs read files back from all four.
['SYS_TMP_WRITE', 'SYS_VAR_TMP_WRITE', 'USER_TEMP_WRITE', 'USER_CACHE_WRITE'].forEach((name, i) => {
  try { fs.writeFileSync(process.argv[5 + i], 'escaped'); out.push(name + '=ALLOWED'); }
  catch (e) { out.push(name + (e.code === 'EPERM' || e.code === 'EACCES' ? '=DENIED' : '=ERROR ' + e.code)); }
});

// Must be ALLOWED: the controls for the denials above. The contained process's
// own temp directory, the device files shell scripts write to, and an Xcode
// tool started through its /usr/bin stand-in, which is how npm reaches git.
const cp = require('child_process');
const why = (e) => '# ' + String((e.stderr && e.stderr.length) ? e.stderr : e.message).trim().split('\n')[0];
try { fs.writeFileSync(require('path').join(require('os').tmpdir(), 'probe'), 'ok'); out.push('OWN_TMP_WRITE=ALLOWED'); }
catch (e) { out.push('OWN_TMP_WRITE=DENIED', why(e)); }
try {
  cp.execFileSync('/bin/sh', ['-c',
    'printf x >/dev/null && printf x >/dev/zero && printf "" >/dev/stdout && printf "" >/dev/fd/1 && printf "" >/dev/stderr'],
    { stdio: 'pipe' });
  out.push('DEV_WRITE=ALLOWED');
} catch (e) { out.push('DEV_WRITE=DENIED', why(e)); }
try { cp.execFileSync('/usr/bin/git', ['--version'], { stdio: 'pipe' }); out.push('XCRUN_TOOL=ALLOWED'); }
catch (e) { out.push('XCRUN_TOOL=DENIED', why(e)); }

// Must be DENIED by the OS: a file in the home directory outside the project,
// and one in nvx's home outside its runtimes. Only EPERM or EACCES counts, so a
// missing fixture cannot pass as a refusal.
const readCheck = (name, p, want) => {
  try {
    const got = fs.readFileSync(p, 'utf8');
    out.push(name + (got.includes(want) ? '=ALLOWED' : '=GARBLED'));
  } catch (e) {
    out.push(name + (e.code === 'EPERM' || e.code === 'EACCES' ? '=DENIED' : '=ERROR ' + e.code));
  }
};
readCheck('READ_OUTSIDE', secret, 'SECRET-CONTENT');
readCheck('NVX_HOME_READ', process.argv[10], 'control-plane');

// Must be ALLOWED: the controls for the two denials above. All three sit under
// the home directory too, so a profile that denied the whole home would fail
// here: the project, the runtime this process is running, and the
// allow_read_exec root the policy names.
readCheck('READ_INSIDE', 'project-file.txt', 'project-file');
try { fs.readFileSync(process.execPath); out.push('READ_RUNTIME=ALLOWED'); }
catch (e) { out.push('READ_RUNTIME=DENIED', why(e)); }
readCheck('READ_EXEC_ROOT', process.argv[9], 'tool-file');

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
"$NVX" -y --strict shim node probe.js "$SECRET" "$FORBIDDEN_WRITE" "$REPORT" "${SHARED_TEMP_TARGETS[@]}" \
  "$READ_EXEC_DIR/tool.txt" "$NVX_HOME/probe-control-plane"
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
expect "GIT_HOOK_WRITE=DENIED"   "a contained process created a git hook; .git must be read-only inside the project"
expect "GIT_CONFIG_WRITE=DENIED" "a contained process wrote .git/config; .git must be read-only inside the project"
expect "GIT_READ=ALLOWED"        "a contained process could not read .git/config; npm and install scripts read it"
expect "INSTALL_WRITE=ALLOWED"   "a contained process could not write package.json or node_modules, so the .git checks above prove nothing"
expect "SYS_TMP_WRITE=DENIED"     "a contained process wrote /private/tmp, which uncontained programs read"
expect "SYS_VAR_TMP_WRITE=DENIED" "a contained process wrote /private/var/tmp, which uncontained programs read"
expect "USER_TEMP_WRITE=DENIED"   "a contained process wrote the user's Darwin temp directory, where every app the user runs keeps its temp files"
expect "USER_CACHE_WRITE=DENIED"  "a contained process wrote the user's Darwin cache directory, where other apps and tools keep caches"
expect "OWN_TMP_WRITE=ALLOWED"    "a contained process could not write its own temp directory, so the temp denials above prove nothing"
expect "DEV_WRITE=ALLOWED"        "a contained shell could not write /dev/null, /dev/zero, /dev/stdout, /dev/fd/1 or /dev/stderr"
expect "XCRUN_TOOL=ALLOWED"       "a contained process could not run /usr/bin/git, which npm uses for git dependencies"
expect "READ_OUTSIDE=DENIED"      "a contained process read a file in the home directory outside the project; other projects there are as readable as that file"
expect "NVX_HOME_READ=DENIED"     "a contained process read nvx's home outside its runtimes, where grants, policy and other tools' credentials live"
expect "READ_INSIDE=ALLOWED"      "a contained process could not read its own project, so the read denials above prove nothing"
expect "READ_RUNTIME=ALLOWED"     "a contained process could not read the runtime it runs from NVX_HOME/versions"
expect "READ_EXEC_ROOT=ALLOWED"   "a contained process could not read a directory the policy names in allow_read_exec"

# On disk, outside the sandbox: nothing reported as denied landed anyway.
for p in "${SHARED_TEMP_TARGETS[@]}"; do
  if [[ -e "$p" ]]; then
    echo "FAIL: $p exists; a contained process wrote a shared temp or cache directory." >&2
    fail=1
  fi
done
if [[ -e .git/hooks/pre-commit ]]; then
  echo "FAIL: .git/hooks/pre-commit exists; a contained process created a git hook." >&2
  fail=1
fi
if [[ "$(cat .git/config)" != "$GIT_CONFIG_BODY" ]]; then
  echo "FAIL: .git/config changed; a contained process wrote it." >&2
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

# A contained run started in the home directory must not be able to write it,
# nor nvx's home below it. The working directory is a writable root, and nothing
# checked which directory it was: measured on this runner before the guard, all
# such writes landed.
HOME_WRITE="$OUTSIDE/written-from-home"
NVX_WRITE="$NVX_HOME/probe-written-from-home"
( cd "$HOME" && "$NVX" -y --strict shim node -e \
    "for(const p of process.argv.slice(1)){try{require('fs').writeFileSync(p,'x')}catch(e){}}" \
    "$HOME_WRITE" "$NVX_WRITE" >/dev/null 2>&1 ) || true
for p in "$HOME_WRITE" "$NVX_WRITE"; do
  if [[ -e "$p" ]]; then
    echo "FAIL: a contained run started in ~ wrote $p; the working directory reached the home directory or nvx's settings." >&2
    rm -f "$p"
    fail=1
  fi
done

# Phase 3: the user's credential stores are not readable.
#
# The profile allows reads broadly and then denies the credential stores under
# the real home, which for nvx is whatever HOME says when it starts. So nvx runs
# here with HOME pointing at a throwaway directory holding a planted .npmrc and
# SSH key, and NVX_HOME where the runs above had it. mktemp puts that home under
# /var/folders, a link to /private/var/folders, so this also checks that the
# deny names the resolved path Seatbelt matches.
#
# Each read reports by exit code: 0 read, 3 refused by the OS (EPERM or EACCES),
# anything else a failure to run at all. The project file and node's own binary
# are the controls that must still read, or a refusal proves nothing.
echo "Phase 3: credential files must be unreadable while other reads still work..."
NVX_HOME_DIR="${NVX_HOME:-$HOME/.nvx}"
mkdir -p "$FAKE_HOME/.ssh"
printf '//registry.npmjs.org/:_authToken=PLANTED-NPM-TOKEN\nregistry=https://planted.invalid/\n' > "$FAKE_HOME/.npmrc"
printf 'PLANTED-SSH-KEY\n' > "$FAKE_HOME/.ssh/id_test"
printf 'project-file\n' > "$PROJ/project-file.txt"

contained_read() {
  local rc=0
  HOME="$FAKE_HOME" NVX_HOME="$NVX_HOME_DIR" "$NVX" -y --strict shim node -e \
    "const p=process.argv[1]==='SELF'?process.execPath:process.argv[1];try{require('fs').readFileSync(p);process.exit(0)}catch(e){process.exit(e.code==='EPERM'||e.code==='EACCES'?3:4)}" \
    "$1" >/dev/null 2>&1 || rc=$?
  echo "$rc"
}
expect_read() {
  local path="$1" want="$2" why="$3" got
  got="$(contained_read "$path")"
  echo "  read $path: exit $got"
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: a contained read of $path exited $got, expected $want. $why" >&2
    fail=1
  fi
}
expect_read "$FAKE_HOME/.npmrc"       3 "The user's .npmrc holds registry tokens and must be unreadable"
expect_read "$FAKE_HOME/.ssh/id_test" 3 "Files under ~/.ssh must be unreadable"
expect_read "$PROJ/project-file.txt"  0 "The project must stay readable, or the denials above prove nothing"
expect_read "SELF"                    0 "Node's own binary must stay readable, or the denials above prove nothing"

# Contained npm still works with a user .npmrc present, and does not use it.
# The guest home has no .npmrc, so the planted registry must not appear.
npm_rc=0
NPM_OUT="$(HOME="$FAKE_HOME" NVX_HOME="$NVX_HOME_DIR" "$NVX" -y --strict shim npm config get registry 2>&1)" || npm_rc=$?
echo "  contained npm config get registry: exit $npm_rc"
if [[ $npm_rc -ne 0 ]]; then
  echo "FAIL: contained npm exited $npm_rc with a user .npmrc present:" >&2
  echo "$NPM_OUT" >&2
  fail=1
elif grep -q 'planted.invalid' <<<"$NPM_OUT"; then
  echo "FAIL: contained npm used the registry from the user's .npmrc. It must only see the guest home's." >&2
  fail=1
fi

# Phase 4: an NVX_HOME outside the home directory.
#
# Everything above has nvx's home under the real home, where the deny on the
# home covers it. NVX_HOME may be anywhere, so nvx's home is denied on its own
# as well, with its runtimes reopened. mktemp puts this one under /var/folders.
# A file in it outside the runtimes must be refused by the OS, and the runtime
# itself must still read, or the refusal proves nothing.
echo "Phase 4: nvx's home outside the home directory must be unreadable outside its runtimes..."
OUT_NVX_HOME="$(mktemp -d)"
trap 'rm -rf "$PROJ" "$NVX_HOME" "$READ_EXEC_DIR" "$OUTSIDE" "$FAKE_HOME" "$OUT_NVX_HOME" "${SHARED_TEMP_TARGETS[@]}"' EXIT
if ! NVX_HOME="$OUT_NVX_HOME" "$NVX" -y install 22 >/dev/null 2>&1 || ! NVX_HOME="$OUT_NVX_HOME" "$NVX" -y default 22 >/dev/null 2>&1; then
  echo "FAIL: could not install an nvx-managed runtime into $OUT_NVX_HOME (network?)." >&2
  fail=1
else
  printf 'control-plane\n' > "$OUT_NVX_HOME/probe-control-plane"
  NVX_HOME_DIR="$OUT_NVX_HOME"
  expect_read "$OUT_NVX_HOME/probe-control-plane" 3 "nvx's home outside its runtimes must be unreadable wherever NVX_HOME is"
  expect_read "SELF"                               0 "The runtime under an NVX_HOME outside the home must stay readable, or the denial above proves nothing"
fi

if [[ $fail -ne 0 ]]; then
  echo "macOS enforcement probe FAILED." >&2
  exit 1
fi

echo "macOS enforcement probe passed: writes contained, egress denied for TCP and UDP,"
echo "an allowlisted host reachable through the proxy, the home directory and credential files unreadable,"
echo "the project, the runtime and an allow_read_exec root readable."
