# Windows containment enforcement — does the AppContainer actually enforce?
#
# The third of a set. scripts/sandbox-enforcement-linux.sh and
# scripts/sandbox-enforcement-macos.sh assert the same shape for their platforms,
# and the three deliberately expect different outcomes: Linux and Windows restrict
# reads, macOS does not.
#
# Why this exists when sandbox-smoke.ps1 already runs a contained process: that
# script checks a host write by writing to `process.env.USERPROFILE`, and inside
# the sandbox USERPROFILE is the redirected guest profile. The write is *supposed*
# to succeed there, so the check passes as long as redirection works — it would
# pass unchanged against a sandbox that restricted nothing. Everything here uses
# absolute paths resolved OUTSIDE the sandbox and passed as arguments. The same
# flaw was found and fixed in the Linux script; Windows had it too and had no
# read assertion at all.
#
# Both halves are asserted: what must be denied, and what must still be allowed.
# A sandbox that refuses everything is a broken launch rather than a strict one,
# and only the positive controls tell them apart.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"

if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe .)"
}
if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    Write-Host "Node.js not available; skipping Windows enforcement probe."
    exit 0
}

# Set-Location changes the SESSION's location, not just this script's, so every
# exit path has to put it back or the next script in the same CI step cannot be
# found. Learned in sandbox-smoke.ps1.
$startLocation = Get-Location

$probeRoot = Join-Path $env:USERPROFILE ".nvx-enforcement-probe"
Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
$proj = Join-Path $probeRoot "project"
$outside = Join-Path $probeRoot "outside"
New-Item -ItemType Directory -Force -Path $proj, $outside | Out-Null

# The fixture lives under the real user profile on purpose. nvx grants the
# working directory and the guest home and nothing else, so this path is outside
# every writable root -- and %USERPROFILE% is the interesting case rather than an
# arbitrary one, because Windows ships an ALL APPLICATION PACKAGES ACE on it that
# nvx neither grants nor can revoke. That ACE makes directory NAMES visible to any
# AppContainer; README says so, and says contents are not. This asserts the second
# half.
$secret = Join-Path $outside "credentials"
Set-Content -Path $secret -Value "SECRET-CONTENT-DO-NOT-LEAK" -Encoding utf8
$forbiddenWrite = Join-Path $outside "should-not-exist"

try {
    Set-Location $proj

    # A runtime the sandbox is allowed to execute, installed under a scratch
    # NVX_HOME so a developer running this does not gain a runtime in their real
    # ~/.nvx. Matches what the Linux and macOS scripts do.
    $env:NVX_HOME = Join-Path $probeRoot "nvxhome"
    New-Item -ItemType Directory -Force -Path $env:NVX_HOME | Out-Null
    Write-Host "Installing an nvx-managed runtime..."
    & $nvx -y install 22 2>&1 | Out-Null
    & $nvx -y default 22 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "::warning::could not install an nvx-managed runtime (network?); skipping Windows enforcement probe"
        exit 0
    }

    @{
        isolation = @{
            enabled = $true
            level   = "strict"
            network = @{ mode = "proxy"; default_allow = @(); prompt_unknown = $false }
        }
    } | ConvertTo-Json -Depth 5 | Set-Content -Path ".nvx-policy.json" -Encoding utf8

    & $nvx init-shims | Out-Null

    # Can this host create an AppContainer at all? GitHub-hosted Windows runners
    # cannot -- CreateProcess returns "Access is denied" for every executable,
    # including cmd.exe. Probing once and skipping with that reason keeps the
    # environment's limitation from being reported as a product failure, while
    # still failing normally everywhere the sandbox does work.
    #
    # This is why Windows is the one platform whose enforcement is not gated in
    # hosted CI, and why the matrix says "measured" for it rather than "CI". Run
    # this script on a real Windows machine before cutting a release.
    $launch = & $nvx shim node -e "process.exit(0)" 2>&1 | Out-String
    if ($launch -match 'AppContainer launch failed') {
        Write-Host "This host cannot create AppContainer children; skipping the containment assertions."
        Write-Host ("  " + $launch.Trim())
        exit 0
    }

    # Arguments, not environment variables: nvx scrubs the environment on the way
    # into the sandbox, which is the point of it. Both the macOS and Linux probes
    # were written with PROBE_* env vars first and got `undefined` -- the
    # containment working exactly as designed, breaking the test measuring it.
    $probeJs = @'
const fs = require('fs');
const https = require('https');
const out = [];
const secret = process.argv[2];
const forbidden = process.argv[3];
const report = process.argv[4];

try { fs.writeFileSync(forbidden, 'escaped'); out.push('WRITE_OUTSIDE=ALLOWED'); }
catch (e) { out.push('WRITE_OUTSIDE=DENIED'); }

try { fs.writeFileSync('inside.txt', 'ok'); out.push('WRITE_INSIDE=ALLOWED'); }
catch (e) { out.push('WRITE_INSIDE=DENIED'); }

// The claim that separates Windows and Linux from macOS: a contained process
// cannot read credentials outside the project by absolute path.
try {
  const got = fs.readFileSync(secret, 'utf8');
  out.push(got.includes('SECRET-CONTENT') ? 'READ_OUTSIDE=ALLOWED' : 'READ_OUTSIDE=GARBLED');
} catch (e) { out.push('READ_OUTSIDE=DENIED'); }

// Reading its own project must still work, or "reads are restricted" cannot be
// told apart from a sandbox that reads nothing.
try {
  fs.readFileSync('probe.js', 'utf8');
  out.push('READ_INSIDE=ALLOWED');
} catch (e) { out.push('READ_INSIDE=DENIED'); }

function finish() {
  fs.writeFileSync(report, out.join('\n') + '\n');
  process.exit(0);
}

// No host is allowlisted, so this must not complete. The AppContainer holds no
// network capability at all, so the OS refuses the connection and DNS does not
// resolve -- this does not depend on the request honouring HTTP_PROXY, which
// Node's classic https API does not do.
const req = https.get('https://example.com', () => { out.push('EGRESS=ALLOWED'); done(); });
req.on('error', () => { out.push('EGRESS=DENIED'); done(); });
req.setTimeout(15000, () => { req.destroy(); out.push('EGRESS=TIMEOUT'); done(); });

let finished = false;
function done() {
  if (finished) return;
  finished = true;
  finish();
}
'@
    $utf8 = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText((Join-Path $proj "probe.js"), $probeJs, $utf8)

    $report = Join-Path $proj "report.txt"
    Write-Host "Running contained probe..."
    & $nvx -y --strict shim node probe.js $secret $forbiddenWrite $report
    $rc = $LASTEXITCODE

    if (-not (Test-Path $report)) {
        Write-Host "FAIL: the contained probe wrote no report (nvx exit $rc)." -ForegroundColor Red
        Write-Host "      The launch above succeeded, so this is not the host refusing AppContainers:"
        Write-Host "      either the sandbox refused to launch node, or it blocked the report write."
        exit 1
    }

    Write-Host "--- contained process reported ---"
    Get-Content $report
    Write-Host "----------------------------------"

    $lines = @(Get-Content $report)
    $fail = $false
    function Expect($want, $why) {
        if ($script:lines -notcontains $want) {
            Write-Host "FAIL: expected $want - $why" -ForegroundColor Red
            $script:fail = $true
        }
    }

    Expect "WRITE_OUTSIDE=DENIED" "a contained process wrote outside the project and the guest home"
    Expect "WRITE_INSIDE=ALLOWED" "a contained process could not write its own project, so the sandbox is broken rather than strict, and every denial here proves nothing"
    Expect "READ_OUTSIDE=DENIED"  "a contained process read a file under the user profile; on Windows reads outside the project are contained, and this is the claim that distinguishes it from macOS"
    Expect "READ_INSIDE=ALLOWED"  "a contained process could not read its own project, so 'reads are restricted' cannot be told apart from a sandbox that reads nothing"
    Expect "EGRESS=DENIED"        "a contained process reached a host with an empty allowlist"

    # What EGRESS=DENIED does NOT cover, stated because this script passed on a
    # machine that was printing the loopback-exemption warning at the time.
    #
    # The probe makes a DIRECT connection to an external host, and the
    # AppContainer holds no network capability, so it is refused whatever the
    # allowlist says. A leftover pre-0.5.0 loopback exemption does not change
    # that -- it makes services on 127.0.0.1 reachable, and any of those that
    # forwards traffic (a debugging proxy, ssh -D, a dev-server proxy route)
    # makes egress arbitrary. Asserting that would mean standing up a listener
    # outside the sandbox and expecting it to be refused, which this does not do.
    # `nvx doctor` reports the exemption and exits non-zero; that is the check
    # for it, not this line.
    $exempt = (& CheckNetIsolation LoopbackExempt -s 2>&1 | Out-String)
    if ($exempt -match 'nvx' -or $exempt -match 'S-1-15-2-') {
        Write-Host "note: a loopback exemption is registered on this machine. Egress denial above is" -ForegroundColor Yellow
        Write-Host "      direct-connection only; loopback-forwarded egress is NOT asserted here." -ForegroundColor Yellow
    }

    # The file must genuinely be absent, not merely reported as denied by a probe
    # that lied to itself.
    if (Test-Path $forbiddenWrite) {
        Write-Host "FAIL: the forbidden path exists on disk; the write escaped the sandbox." -ForegroundColor Red
        $fail = $true
    }

    if ($fail) {
        Write-Host "Windows enforcement probe FAILED." -ForegroundColor Red
        exit 1
    }

    Write-Host "Windows enforcement probe passed: writes contained, reads restricted, egress denied." -ForegroundColor Green
}
finally {
    Set-Location $startLocation
    Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
}
