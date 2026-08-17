# verify-security.ps1
# Runs the local security and vulnerability scans.
#
# This script used to end with an unconditional "All security scans completed
# successfully!". PowerShell does not trap non-zero exits from native executables,
# and $LASTEXITCODE was never checked, so it printed success no matter what the
# tools found -- measured: gosec exiting 1 with 53 issues, reported as a pass.
# Every check below now records its real exit code and the script fails if any did.

$ErrorActionPreference = 'Stop'

# gosec exclusions, each with a reason. Anything not listed here is expected to be
# clean, so a new finding fails the run instead of joining a backlog.
#   G204 - subprocess launched with a variable. nvx exists to run user-named
#          binaries; this fires on essentially every launch path.
#   G304 - file inclusion via variable. Same: paths come from policy and CLI args.
#   G301/G306 - directory and file permissions on created paths, already chosen
#          deliberately (0700 for nvx-owned trees).
#   G103 - use of unsafe. Unavoidable: the Windows sandbox calls Win32 APIs
#          directly, which requires unsafe.Pointer. 48 of the 48 hits are in the
#          syscall layer.
#   G702 - command injection by taint analysis. Both hits are exec.Command on a
#          runtime path nvx itself resolved. Running a user-named binary is what
#          this tool is; same reasoning as G204 above.
#   G703 - path traversal by taint analysis. 22 hits, essentially all os.Stat or
#          filepath.Join on paths derived from CLI arguments and policy, most of
#          them read-only existence checks. Same reasoning as G304 above.
# Triaged 2026-08-17: of 34 findings, one was real (registry URLs built from an
# unescaped package name, fixed) and the rest fell into the categories above.
# G115 and the remaining G704 are annotated at the source instead, because integer
# conversion and outbound requests are classes worth keeping enabled elsewhere.
# Narrower suppressions live at the source as #nosec comments with a reason.
# Tool versions are PINNED, not @latest. A gate whose strictness drifts with
# upstream releases turns red without a code change, and then gets ignored.
# Upgrading is a deliberate act: newer gosec adds rules (the v2.2x taint-analysis
# set, G702/G703/G704) that report findings this codebase has never triaged.
$GosecExclude = 'G204,G304,G301,G306,G103,G702,G703'

# Where `go install` actually puts binaries. This used to be hardcoded to
# $env:USERPROFILE\go\bin, which is wrong wherever GOPATH or GOBIN is set
# elsewhere; the tool would be installed and then invoked at a path that does not
# exist.
$GoBinDir = (go env GOBIN)
if ([string]::IsNullOrWhiteSpace($GoBinDir)) {
    $GoBinDir = Join-Path (go env GOPATH) 'bin'
}

$script:Failures = @()

function Invoke-Check {
    param(
        [string]$Name,
        [scriptblock]$Body
    )
    Write-Host "`n$Name..." -ForegroundColor Cyan
    $global:LASTEXITCODE = 0
    & $Body
    $code = $LASTEXITCODE
    if ($code -ne 0) {
        Write-Host "  $Name FAILED (exit $code)" -ForegroundColor Red
        $script:Failures += $Name
    } else {
        Write-Host "  $Name passed" -ForegroundColor Green
    }
}

Write-Host "Running local security checks for nvx..." -ForegroundColor Cyan

# Ensure-Tool guarantees the tool on disk is BOTH the pinned version and built by
# the Go toolchain now in use, reinstalling it otherwise.
#
# The previous logic installed only when the binary was missing, so a stale one was
# reused forever. That is not theoretical: after a Go upgrade, binaries built by the
# old toolchain could not parse the newer source at all -- govulncheck reported
# "uses version go1.19 of the source-processing packages but runs version go1.26 of
# 'go list'" and gosec panicked inside x/tools. Both failures looked like the code
# was at fault.
#
# `go version -m` reports both facts from the binary's embedded build info, which is
# the only reliable source: gosec built via `go install` reports its own --version
# as "dev". Checking is fast; installing unconditionally costs ~17s per run, which
# is enough to stop people running the gate.
function Ensure-Tool {
    param(
        [string]$Label,
        [string]$Exe,
        [string]$Module,
        [string]$Version,
        [string]$ModPath
    )
    $goVer = (go env GOVERSION)

    if (Test-Path $Exe) {
        $info = & go version -m $Exe 2>$null
        $builtBy = (($info | Select-Object -First 1) -replace '^.*:\s*', '').Trim()
        $modLine = $info | Where-Object { $_ -match "^\s*mod\s" -and $_ -match [regex]::Escape($ModPath) } | Select-Object -First 1
        $modVer = if ($modLine) { ($modLine.Trim() -split '\s+')[2] } else { '' }
        if ($builtBy -eq $goVer -and $modVer -eq $Version) {
            return
        }
        $haveV = if ($modVer) { $modVer } else { 'unknown' }
        $haveG = if ($builtBy) { $builtBy } else { 'unknown' }
        Write-Host "Reinstalling ${Label}: have $haveV built by $haveG, want $Version built by $goVer" -ForegroundColor Yellow
    } else {
        Write-Host "Installing $Label $Version..." -ForegroundColor Yellow
    }
    go install "$Module@$Version"
}

$govulncheckPath = Join-Path $GoBinDir 'govulncheck.exe'
$gosecPath = Join-Path $GoBinDir 'gosec.exe'

Ensure-Tool -Label 'govulncheck' -Exe $govulncheckPath -Module 'golang.org/x/vuln/cmd/govulncheck' -Version 'v1.7.0' -ModPath 'golang.org/x/vuln'
Ensure-Tool -Label 'gosec' -Exe $gosecPath -Module 'github.com/securego/gosec/v2/cmd/gosec' -Version 'v2.28.0' -ModPath 'github.com/securego/gosec/v2'

# Run the copies just verified, never whatever is first on PATH -- a different build
# of either tool there would silently change what this gate checks.
Invoke-Check "govulncheck" { & $govulncheckPath ./... }
Invoke-Check "gosec" { & $gosecPath "-exclude=$GosecExclude" ./... }

# 3. go vet
Invoke-Check "go vet" { go vet ./... }

Write-Host ""
if ($script:Failures.Count -gt 0) {
    Write-Host "SECURITY CHECKS FAILED: $($script:Failures -join ', ')" -ForegroundColor Red
    Write-Host "govulncheck reports against the Go toolchain in use; if it is the only failure, check 'go version' before assuming the code is at fault." -ForegroundColor Yellow
    exit 1
}

Write-Host "All security scans passed." -ForegroundColor Green
exit 0
