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
# controlling terminal, as a user's shell is. Node cannot call ioctl, so the
# contained node spawns the runner's python3, which inherits the Seatbelt sandbox
# (a child does, unlike something launchd starts) and the controlling tty on
# fd 0.
#
# Two runs, like the launch-escape probe: once UNCONTAINED as the positive
# control, which must inject, and once contained, which must be refused. A
# contained refusal only counts when the control proved the vector works on this
# runner, because macOS restricts TIOCSTI itself (non-root needs the fd readable
# and the controlling terminal), and a refusal from a vector that never works
# here would say nothing about the sandbox.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS-only; skipping." >&2
  exit 0
fi
[[ -x "$NVX" ]] || { echo "Build nvx first: go build -o nvx ./cmd/nvx" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 not available; skipping the terminal-injection probe." >&2; exit 0; }

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

# TIOCSTI is _IOW('t', 114, char) = 0x80017472 on macOS. The probe tries to type
# one byte into its own stdin and says what the ioctl returned. no-tty means its
# stdin was not a terminal, so the attempt proved nothing.
cat > tiocsti.py <<'PY'
import os, fcntl, sys
TIOCSTI = 0x80017472
if not os.isatty(0):
    print("TIOCSTI=no-tty"); sys.exit(0)
try:
    fcntl.ioctl(0, TIOCSTI, b"X")
    print("TIOCSTI=ok")
except OSError as e:
    print("TIOCSTI=err:%d:%s" % (e.errno, os.strerror(e.errno)))
PY

# The contained side: node is what nvx contains, so node spawns python3. python3
# inherits the sandbox and the controlling tty on fd 0.
cat > run_tiocsti.js <<'JS'
const { spawnSync } = require('child_process');
const r = spawnSync('/usr/bin/python3', [process.env.TIOCSTI_PY], { stdio: ['inherit', 'inherit', 'inherit'] });
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

export TIOCSTI_PY="$PROJ/tiocsti.py"

# Line the probe prints, pulled out of whatever else the pty carried.
result_of() { grep -oE 'TIOCSTI=[^[:space:]]*' <<<"$1" | tail -1; }

echo "== control: uncontrolled python3 on a controlling terminal =="
control_raw="$(python3 harness.py python3 "$TIOCSTI_PY" 2>&1)"
control="$(result_of "$control_raw")"
echo "control: ${control:-<none>}"

echo "== contained: nvx --strict shim node, which spawns python3 =="
contained_raw="$(python3 harness.py "$NVX" -y --strict shim node "$PROJ/run_tiocsti.js" 2>&1)"
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
