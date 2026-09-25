#!/usr/bin/env bash
# Can a Seatbelt-contained process get code run OUTSIDE the sandbox by asking
# launchd or LaunchServices to start it?
#
# The profile allows every mach-lookup, because dyld needs system services to
# load a binary at all. launchd and LaunchServices are Mach services too, and a
# process they start is their child, not ours, so it would not inherit the
# profile. If that held, write containment would be one `launchctl submit` away
# from meaningless.
#
# It does not hold: on the macos-latest runner on 2026-09-25 both vectors worked
# unsandboxed and were refused contained (launchctl exits 1, open fails with
# LaunchServices error -54). That is macOS refusing a sandboxed caller, not
# anything nvx's profile says, so it is kept as a regression check. If a macOS
# release changes it, this is where it shows.
#
# Each vector is run twice: once unsandboxed as the positive control, once
# contained. A vector only counts as DENIED when the control proved it works on
# this runner, because a refusal from a vector that never works here says
# nothing about the sandbox.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NVX="$ROOT/nvx"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS-only; skipping." >&2
  exit 0
fi
[[ -x "$NVX" ]] || { echo "Build nvx first: go build -o nvx ./cmd/nvx" >&2; exit 1; }

PROJ="$(mktemp -d)"
# Outside every write root the profile grants, for the reason given in
# sandbox-enforcement-macos.sh: mktemp lands under /private/var/folders.
OUTSIDE="$HOME/.nvx-launchservices-probe"
rm -rf "$OUTSIDE"; mkdir -p "$OUTSIDE"
trap 'rm -rf "$PROJ" "$OUTSIDE"; launchctl remove nvx.probe.control 2>/dev/null; launchctl remove nvx.probe.contained 2>/dev/null' EXIT

cd "$PROJ"
cat > .nvx-policy.json <<'POLICY'
{ "isolation": { "enabled": true, "level": "strict",
  "network": { "mode": "offline", "default_allow": [], "prompt_unknown": false } } }
POLICY

# A minimal app bundle, which is what `open` hands to LaunchServices. Terminal
# with a .command file was the first attempt, and on a CI runner it never ran
# the file even unsandboxed, so it proved nothing. A bundle whose executable is
# a shell script needs no signing and no GUI. It must live in the project, the
# one place the contained process can write it.
mkdir -p Escape.app/Contents/MacOS
cat > Escape.app/Contents/Info.plist <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>Escape</string>
  <key>CFBundleIdentifier</key><string>run.nvx.probe.escape</string>
  <key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
PLIST
cat > Escape.app/Contents/MacOS/Escape <<'APP'
#!/bin/sh
echo escaped > "$1"
APP
chmod +x Escape.app/Contents/MacOS/Escape

cat > probe.js <<'PROBE'
const { spawnSync } = require('child_process');
const [vector, target, label] = process.argv.slice(2);
let r;
if (vector === 'launchctl') {
  r = spawnSync('/bin/launchctl', ['submit', '-l', label, '--', '/bin/sh', '-c', `echo escaped > '${target}'`]);
} else if (vector === 'open') {
  r = spawnSync('/usr/bin/open', ['-n', '-g', 'Escape.app', '--args', target]);
} else if (vector === 'direct') {
  r = spawnSync('/bin/sh', ['-c', `echo escaped > '${target}'`]);
}
console.log(`PROBE ${vector} status=${r.status} err=${(r.stderr || '').toString().trim().slice(0, 200)}`);
PROBE

export NVX_TRUST_YES=true
waitfor() { for _ in $(seq 1 20); do [[ -e "$1" ]] && return 0; sleep 0.5; done; return 1; }

fail=0
for vector in direct launchctl open; do
  ctl="$OUTSIDE/$vector-control"
  box="$OUTSIDE/$vector-contained"

  if [[ $vector == direct ]]; then
    ctl_ok=yes   # a plain write needs no control; the enforcement script covers it
  else
    node probe.js "$vector" "$ctl" nvx.probe.control
    waitfor "$ctl" && ctl_ok=yes || ctl_ok=no
    launchctl remove nvx.probe.control 2>/dev/null
  fi

  "$NVX" -y --strict shim node probe.js "$vector" "$box" nvx.probe.contained 2>&1 | grep '^PROBE' || true
  waitfor "$box" && escaped=yes || escaped=no
  launchctl remove nvx.probe.contained 2>/dev/null

  if [[ $escaped == yes ]]; then
    echo "RESULT $vector: ESCAPED (contained process caused a write outside the sandbox)"
    fail=1
  elif [[ $ctl_ok == yes ]]; then
    echo "RESULT $vector: DENIED (control worked, contained attempt did not)"
  else
    echo "RESULT $vector: INCONCLUSIVE (the unsandboxed control did not work on this runner)"
  fi
done

exit $fail
