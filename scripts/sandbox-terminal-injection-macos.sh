#!/usr/bin/env bash
# Can a Seatbelt-contained process type into the terminal nvx runs on?
#
# nvx gives the contained process its own stdin, which is nvx's terminal, shared
# with the user's shell. ioctl(fd, TIOCSTI, &c) pushes a byte into that
# terminal's input queue, and after nvx exits the shell reads it as typed input,
# so a package postinstall could leave a command and an Enter to run as the user
# outside the sandbox. This is the macOS analogue of the Linux TIOCSTI escape and
# of bubblewrap's CVE-2017-5226. The Seatbelt profile denies it with
# `(deny file-ioctl (ioctl-command 2147578994))` (see seatbeltTerminalInputDeny),
# and file-ioctl is denied by default besides. This checks a real kernel honours
# that.
#
# The attempt runs on a real controlling terminal, built with forkpty so the
# process that runs the probe is the session leader and the pty is its
# controlling terminal, as a user's shell is. The ioctl is done by a small C
# helper compiled here, not by a script runtime: /usr/bin/python3 on a runner is
# an Xcode shim that writes an xcrun cache outside the sandbox's writable paths,
# so it misbehaves contained. node is what nvx contains, so the contained node
# spawns the helper, which inherits the Seatbelt sandbox (a child does, unlike
# something launchd starts) and the controlling tty on fd 0. The helper path is
# passed as an argument, not an environment variable, because nvx scrubs the
# environment of a contained process.
#
# Two runs, like the launch-escape probe: once UNCONTAINED as the positive
# control, which must inject, and once contained, which must be refused. A
# contained refusal only counts when the control proved the vector works on this
# runner, because macOS restricts TIOCSTI itself (XNU tty.c: a non-root caller
# needs the fd readable and its controlling terminal), and a refusal from a
# vector that never works here would say nothing about the sandbox.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS-only; skipping." >&2
  exit 0
fi
[[ -x "$NVX" ]] || { echo "Build nvx first: go build -o nvx ./cmd/nvx" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 not available to host the controlling terminal; skipping." >&2; exit 0; }
command -v cc >/dev/null 2>&1 || { echo "cc not available to build the probe; skipping." >&2; exit 0; }

PROJ="$(mktemp -d)"
export NVX_HOME="$(mktemp -d)"
trap 'rm -rf "$PROJ" "$NVX_HOME"' EXIT
cd "$PROJ"

# strict so a bare runtime run is contained, offline so nothing here needs the
# network. A trusted project policy, as the other macOS probes use.
cat > .nvx-policy.json <<'POLICY'
{ "isolation": { "enabled": true, "level": "strict",
  "network": { "mode": "offline", "default_allow": [], "prompt_unknown": false } } }
POLICY
export NVX_TRUST_YES=true

echo "Installing an nvx-managed runtime..."
if ! "$NVX" -y install 22 >/dev/null 2>&1 || ! "$NVX" -y default 22 >/dev/null 2>&1; then
  echo "FAIL: could not install an nvx-managed runtime into $NVX_HOME (network?)." >&2
  echo "      The contained run needs one, so nothing here can be checked." >&2
  exit 1
fi

# The ioctl, in C so it needs no script runtime at run time. TIOCSTI comes from
# the system headers (sys/ttycom.h via sys/ioctl.h), so the number is the
# kernel's own. It types one byte into its stdin and says what the ioctl
# returned; no-tty means stdin was not a terminal, so the attempt proved nothing.
cat > tiocsti.c <<'C'
#include <stdio.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <errno.h>
#include <string.h>
int main(void) {
    if (!isatty(0)) { printf("TIOCSTI=no-tty\n"); fflush(stdout); return 0; }
    char c = 'X';
    if (ioctl(0, TIOCSTI, &c) == 0) printf("TIOCSTI=ok\n");
    else printf("TIOCSTI=err:%d:%s\n", errno, strerror(errno));
    fflush(stdout);
    return 0;
}
C
cc -o tiocsti tiocsti.c || { echo "FAIL: could not build the probe helper." >&2; exit 1; }

# The contained side: node is what nvx contains, so node spawns the helper. The
# helper inherits the sandbox and the controlling tty on fd 0. Its path is
# resolved against the working directory, which is the sandbox's own view of the
# project, because a bare name would be searched on PATH.
cat > run_tiocsti.js <<'JS'
const { spawnSync } = require('child_process');
const path = require('path');
const arg = process.argv[2];
if (!arg) { console.log('TIOCSTI=no-helper-arg'); process.exit(1); }
const helper = path.resolve(arg);
const r = spawnSync(helper, [], { stdio: ['inherit', 'inherit', 'inherit'] });
if (r.error) { console.log('TIOCSTI=spawn-error:' + (r.error.code || r.error.message)); process.exit(1); }
process.exit(r.status === null ? 1 : r.status);
JS

# forkpty gives the child a controlling terminal, the setup the escape needs.
# The parent reads the pty until the child exits, so the probe's line comes back
# however the child wrote it.
cat > harness.py <<'PY'
import os, sys, select, time
def run(argv):
    pid, fd = os.forkpty()
    if pid == 0:
        try:
            os.execvp(argv[0], argv)
        except Exception as e:
            sys.stderr.write("exec failed: %s\n" % e)
        os._exit(127)
    out = b""
    end = time.time() + 60
    while time.time() < end:
        try:
            r, _, _ = select.select([fd], [], [], 1)
        except OSError:
            break
        if not r:
            continue
        try:
            d = os.read(fd, 65536)
        except OSError:
            break
        if not d:
            break
        out += d
    try:
        os.waitpid(pid, 0)
    except OSError:
        pass
    try:
        os.close(fd)
    except OSError:
        pass
    sys.stdout.write(out.decode("utf-8", "replace"))

run(sys.argv[1:])
PY

# Line the probe prints, pulled out of whatever else the pty carried.
result_of() { grep -oE 'TIOCSTI=[^[:space:]]*' <<<"$1" | tail -1; }

echo "== control: the helper on a controlling terminal, uncontained =="
control_raw="$(python3 harness.py ./tiocsti 2>&1)"
control="$(result_of "$control_raw")"
echo "control: ${control:-<none>}"

echo "== contained: nvx --strict shim node, which spawns the helper =="
contained_raw="$(python3 harness.py "$NVX" -y --strict shim node run_tiocsti.js tiocsti 2>&1)"
contained="$(result_of "$contained_raw")"
echo "contained: ${contained:-<none>}"

fail=0
case "$contained" in
  TIOCSTI=ok)
    echo "RESULT: ESCAPED -- a contained process typed into the terminal."
    echo "---- contained output ----"; echo "$contained_raw"
    fail=1
    ;;
  TIOCSTI=err:*)
    if [[ "$control" == "TIOCSTI=ok" ]]; then
      echo "RESULT: DENIED -- the control injected, the contained attempt was refused ($contained)."
    else
      echo "RESULT: INCONCLUSIVE -- the contained attempt was refused, but the control did not inject (control=${control:-<none>}),"
      echo "        so this run does not show the sandbox is what refused it."
      echo "---- control output ----"; echo "$control_raw"
      # On CI the control is expected to work; a developer machine may differ.
      if [[ -n "${CI:-}" ]]; then fail=1; fi
    fi
    ;;
  *)
    echo "RESULT: NOT RUN -- the contained probe never reported a TIOCSTI result (got '${contained:-<none>}')."
    echo "---- contained output ----"; echo "$contained_raw"
    fail=1
    ;;
esac

if [[ $fail -eq 0 ]]; then
  echo "macOS terminal-injection probe passed: a contained process cannot type into the terminal."
fi
exit $fail
