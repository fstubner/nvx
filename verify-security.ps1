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
# Narrower suppressions live at the source as #nosec comments with a reason.
# Tool versions are PINNED, not @latest. A gate whose strictness drifts with
# upstream releases turns red without a code change, and then gets ignored.
# Upgrading is a deliberate act: newer gosec adds rules (the v2.2x taint-analysis
# set, G702/G703/G704) that report findings this codebase has never triaged.
$GosecExclude = 'G204,G304,G301,G306,G103'

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

# 1. govulncheck
$govulncheckPath = Join-Path $GoBinDir 'govulncheck.exe'
if (-not (Test-Path $govulncheckPath)) {
    Write-Host "Installing govulncheck..." -ForegroundColor Yellow
    go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
}
Invoke-Check "govulncheck" { & $govulncheckPath ./... }

# 2. gosec
$gosecPath = Join-Path $GoBinDir 'gosec.exe'
if (-not (Test-Path $gosecPath)) {
    Write-Host "Installing gosec..." -ForegroundColor Yellow
    go install github.com/securego/gosec/v2/cmd/gosec@v2.28.0
}
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
