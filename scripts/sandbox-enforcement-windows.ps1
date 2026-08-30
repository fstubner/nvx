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

# Capturing a native command's stderr is incompatible with
# $ErrorActionPreference = 'Stop': `2>&1` turns every stderr line into an
# ErrorRecord, and 'Stop' makes the first one terminate the script. nvx writes
# its ordinary progress lines to stderr, so this gate aborted at the runtime
# install below having asserted nothing, and did so with a PowerShell error
# rather than one of its own FAIL messages. It had been that way since the
# script was written, through two releases, while the enforcement matrix cited
# it as where the word "measured" comes from.
#
# The install already had a careful $LASTEXITCODE check that never got to run.
# Relaxing the preference globally would disarm that check and every other one,
# so it is relaxed per call instead: $ErrorActionPreference is scoped
# dynamically, and setting it inside this function applies to the call and
# nothing else. Every site that captures output goes through here; the two bare
# invocations that let stderr through to the console are unaffected and stay as
# they are.
function Invoke-NativeCapture {
    param([Parameter(Mandatory)][string]$Exe, [string[]]$Arguments = @())
    $ErrorActionPreference = 'Continue'
    $out = & $Exe @Arguments 2>&1 | Out-String
    return [pscustomobject]@{ Output = $out; ExitCode = $LASTEXITCODE }
}

if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe .)"
}
if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    # A failure, not a skip. This is the hand-run release gate on a machine the
    # maintainer controls, and "no Node here" is a broken setup rather than an
    # environment nvx has to tolerate. Exiting 0 made a green run mean nothing.
    Write-Host "FAIL: Node.js is not on PATH, so nothing can be contained and nothing was asserted." -ForegroundColor Red
    Write-Host "      This is the pre-release gate; install Node and run it again."
    exit 1
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
    # Both commands checked, and a failure is fatal.
    #
    # $LASTEXITCODE reflects only the LAST command, so a failed `install`
    # followed by a successful `default` read as success. And the failure exited
    # 0 with a warning, so this gate could pass having asserted nothing -- the
    # same warn-instead-of-fail shape that let three Linux checks report success
    # for months, and that ci.yml was changed to reject.
    Write-Host "Installing an nvx-managed runtime..."
    $installCode = (Invoke-NativeCapture $nvx @('-y', 'install', '22')).ExitCode
    $defaultCode = (Invoke-NativeCapture $nvx @('-y', 'default', '22')).ExitCode
    if ($installCode -ne 0 -or $defaultCode -ne 0) {
        Write-Host "FAIL: could not install an nvx-managed runtime (install=$installCode default=$defaultCode)." -ForegroundColor Red
        Write-Host "      Without one the sandbox has nothing it is permitted to execute, so this gate"
        Write-Host "      would assert nothing. If this is a network problem, fix it and re-run rather"
        Write-Host "      than treating a green run as evidence."
        exit 1
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
    $launch = (Invoke-NativeCapture $nvx @('shim', 'node', '-e', 'process.exit(0)')).Output
    if ($launch -match 'AppContainer launch failed') {
        # Only the two shapes a HOST refusal takes, not any launch failure.
        #
        # This skipped on the mere presence of "AppContainer launch failed", so a
        # regression that broke every launch would have read as "this host cannot
        # host one" and the gate would have passed having asserted nothing -- the
        # exact failure mode the Linux probe was rewritten to close. The two
        # strings below are the ones requireAppContainerLaunch documents for a
        # hosted runner refusing every executable, cmd.exe included.
        #
        # What this still cannot distinguish: a regression whose error happens to
        # be "Access is denied". Nothing available from PowerShell can create an
        # AppContainer independently of nvx to settle that, so it is narrowed
        # rather than closed, and said so here rather than left to be assumed.
        if ($launch -match 'Access is denied' -or $launch -match 'The system cannot find the file specified') {
            Write-Host "This host cannot create AppContainer children; skipping the containment assertions."
            Write-Host ("  " + $launch.Trim())
            exit 0
        }
        Write-Host "FAIL: the sandbox could not launch, and not in a way this host is known to refuse." -ForegroundColor Red
        Write-Host "      A hosted runner refuses with 'Access is denied' or 'The system cannot find the"
        Write-Host "      file specified'. This is neither, so treat it as a regression rather than an"
        Write-Host "      environment limit:"
        Write-Host ("  " + $launch.Trim())
        exit 1
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

    # ------------------------------------------------------------------
    # One sandbox must not reach a service inside another project's sandbox.
    #
    # Windows permits loopback WITHIN an AppContainer package. While every nvx
    # sandbox shared one package, any port a contained process bound was
    # reachable from every other contained process on the machine: measured
    # 2026-08-29, a sandbox in an unrelated project with an empty allowlist read
    # a listener inside another project's sandbox, while the same sandbox could
    # not reach a listener on the host. That turns the egress allowlist into a
    # suggestion -- one sandbox relays for another -- and hands two projects a
    # channel the filesystem isolation exists to deny.
    #
    # Packages are per project now. This asserts it end to end rather than
    # trusting the naming: the whole defect was that the naming looked fine and
    # the network did not care.
    #
    # The positive control is what makes the denial mean anything. A listener
    # that never started, or a client that cannot run, also produces "refused".
    # So the same connection is made from the listener's OWN project, where it
    # must succeed -- same project, same package, same trust domain, which is the
    # boundary this draws rather than one it hides.
    Write-Host "Testing cross-sandbox loopback..."
    $other = Join-Path $probeRoot "otherproject"
    New-Item -ItemType Directory -Force -Path $other | Out-Null
    Copy-Item (Join-Path $proj ".nvx-policy.json") (Join-Path $other ".nvx-policy.json")
    $utf8n = New-Object System.Text.UTF8Encoding $false

    $ready = Join-Path $proj "listener-ready.txt"
    [System.IO.File]::WriteAllText((Join-Path $proj "listener.js"), @'
const net = require('net');
const fs = require('fs');
net.createServer(s => s.end('SANDBOX_A_SECRET'))
   .listen(20781, '127.0.0.1', () => fs.writeFileSync(process.argv[2], 'ready'));
setTimeout(() => process.exit(0), 120000);
'@, $utf8n)

    $clientJs = @'
const net = require('net');
const fs = require('fs');
const s = net.connect(20781, '127.0.0.1');
let b = '';
s.on('data', d => b += d);
s.on('end', () => { fs.writeFileSync(process.argv[2], 'REACHED:' + b.trim()); process.exit(0); });
s.on('error', e => { fs.writeFileSync(process.argv[2], 'REFUSED:' + e.code); process.exit(0); });
setTimeout(() => { fs.writeFileSync(process.argv[2], 'REFUSED:TIMEOUT'); process.exit(0); }, 15000);
'@
    [System.IO.File]::WriteAllText((Join-Path $other "client.js"), $clientJs, $utf8n)
    [System.IO.File]::WriteAllText((Join-Path $proj "client.js"), $clientJs, $utf8n)

    Remove-Item $ready -Force -ErrorAction SilentlyContinue
    # The job's output is kept, not discarded. "The listener never started" with
    # no reason is the kind of failure that costs an hour; this is a gate, and a
    # gate that cannot say why it failed is only half a gate.
    $listenerLog = Join-Path $probeRoot "listener.log"
    $listener = Start-Job -ScriptBlock {
        param($nvxPath, $dir, $home2, $readyPath, $logPath)
        Set-Location $dir
        $env:NVX_HOME = $home2
        $env:NVX_YES = "true"
        & $nvxPath -y --strict shim node listener.js $readyPath *>&1 |
            Out-File -FilePath $logPath -Encoding utf8
    } -ArgumentList $nvx, $proj, $env:NVX_HOME, $ready, $listenerLog

    for ($i = 0; $i -lt 120 -and -not (Test-Path $ready); $i++) { Start-Sleep -Milliseconds 500 }
    if (-not (Test-Path $ready)) {
        Stop-Job $listener -ErrorAction SilentlyContinue
        Remove-Job $listener -Force -ErrorAction SilentlyContinue
        Write-Host "FAIL: the contained listener never started, so nothing below could be asserted." -ForegroundColor Red
        if (Test-Path $listenerLog) {
            Write-Host "      Its output:"
            Get-Content $listenerLog | Select-Object -Last 12 | ForEach-Object { Write-Host "        $_" }
        } else {
            Write-Host "      It produced no output at all."
        }
        $fail = $true
    } else {
        $crossReport = Join-Path $other "cross.txt"
        Set-Location $other
        $null = Invoke-NativeCapture $nvx @('-y', '--strict', 'shim', 'node', 'client.js', $crossReport)
        $sameReport = Join-Path $proj "same.txt"
        Set-Location $proj
        $null = Invoke-NativeCapture $nvx @('-y', '--strict', 'shim', 'node', 'client.js', $sameReport)

        Stop-Job $listener -ErrorAction SilentlyContinue
        Remove-Job $listener -Force -ErrorAction SilentlyContinue

        $cross = if (Test-Path $crossReport) { (Get-Content $crossReport -Raw).Trim() } else { "(no report)" }
        $same  = if (Test-Path $sameReport)  { (Get-Content $sameReport -Raw).Trim() }  else { "(no report)" }
        Write-Host "  cross-project: $cross"
        Write-Host "  same-project : $same"

        if (-not $cross.StartsWith("REFUSED")) {
            Write-Host "FAIL: a sandbox in another project reached this sandbox's loopback listener ($cross)." -ForegroundColor Red
            Write-Host "      That defeats the egress allowlist by relay and joins two projects that must stay apart."
            $fail = $true
        }
        if (-not $same.StartsWith("REACHED")) {
            Write-Host "FAIL: a sandbox in the listener's OWN project could not reach it ($same)." -ForegroundColor Red
            Write-Host "      Without this the refusal above proves nothing: a listener that never accepted"
            Write-Host "      any connection would satisfy it."
            $fail = $true
        }
    }

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
    # Only nvx's own exemption is relevant, and matching the SID *shape* was wrong:
    # S-1-15-2- prefixes every AppContainer SID, so this fired on a machine whose
    # nvx exemption had just been removed and which still carried four unrelated
    # ones (a Windows WebView host and three orphans from uninstalled apps). It
    # told the maintainer he still had the problem he had just fixed.
    $exempt = (Invoke-NativeCapture 'CheckNetIsolation' @('LoopbackExempt', '-s')).Output
    if ($exempt -match 'nvx') {
        Write-Host "note: an nvx loopback exemption is registered on this machine. Egress denial above" -ForegroundColor Yellow
        Write-Host "      is direct-connection only; loopback-forwarded egress is NOT asserted here." -ForegroundColor Yellow
        Write-Host "      Run 'nvx doctor' for the removal command." -ForegroundColor Yellow
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
