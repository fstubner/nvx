# Covers what the three Windows containment gates do when the host refuses to
# create AppContainer children, in both places they run.
#
#   scripts/sandbox-smoke.ps1
#   scripts/sandbox-smoke-egress.ps1
#   scripts/sandbox-enforcement-windows.ps1
#
# Each one probes for the refusal and then asserts nothing. On a developer
# machine that is a skip, because a host that cannot host the sandbox is not a
# product failure. On GitHub Actions it has to fail. The runners launch
# AppContainers now, and a gate that skips there is a green step that checked
# nothing, which is what these scripts did on a hosted runner until 2026-09-21.
#
# The scripts are run for real, each in a child process, against a stand-in
# nvx.exe that exits 0 for everything except `shim`, which it refuses the way
# the real one prints a host refusal. USERPROFILE points at a scratch directory,
# so what the scripts create goes there. Nothing is installed and nothing is
# contained.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent

$failures = 0
$dir = Join-Path ([IO.Path]::GetTempPath()) ("nvx-windows-gates-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $dir | Out-Null

function Check($label, $ok) {
    if ($ok) {
        Write-Host "  ok   $label"
    } else {
        Write-Host "  FAIL $label"
        $script:failures++
    }
}

# The stand-in. The refusal text is what nvx prints for a host that will not
# create the child, and the test can swap it for another launch failure.
$source = Join-Path $dir 'main.go'
Set-Content -Path $source -Encoding ascii -Value @'
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "shim" {
		fmt.Fprintln(os.Stderr, os.Getenv("STUB_LAUNCH_FAILURE"))
		os.Exit(1)
	}
}
'@
New-Item -ItemType Directory -Path (Join-Path $dir 'scripts') | Out-Null
Push-Location $dir
try { & go build -o (Join-Path $dir 'nvx.exe') $source } finally { Pop-Location }
if ($LASTEXITCODE -ne 0) { Write-Error "could not build the stand-in nvx.exe"; exit 1 }

# The gates look for node on PATH before they launch anything.
$bin = Join-Path $dir 'bin'
New-Item -ItemType Directory -Path $bin | Out-Null
Set-Content -Path (Join-Path $bin 'node.cmd') -Value '@exit /b 0' -Encoding ascii

$refusal = 'nvx: AppContainer launch failed: CreateProcess(AppContainer) C:\Windows\System32\cmd.exe: Access is denied.'
$other = 'nvx: AppContainer launch failed: CreateProcess(AppContainer): The parameter is incorrect.'

# Runs one gate in a child of the shell running this test, and returns its exit
# code and everything it printed. $GitHubActions is what the child sees.
function Run-Gate([string]$Name, [string]$Failure, [bool]$GitHubActions) {
    # Windows PowerShell turns a child's stderr, captured with 2>&1, into errors
    # that Stop would throw. The child's failure is the thing being measured.
    $ErrorActionPreference = 'Continue'
    Copy-Item (Join-Path $PSScriptRoot $Name) (Join-Path $dir 'scripts')
    $saved = @{}
    foreach ($v in 'USERPROFILE', 'PATH', 'GITHUB_ACTIONS', 'STUB_LAUNCH_FAILURE', 'NVX_SMOKE_SKIP_APPCONTAINER') {
        $saved[$v] = [Environment]::GetEnvironmentVariable($v)
    }
    try {
        $env:USERPROFILE = Join-Path $dir 'home'
        New-Item -ItemType Directory -Force -Path $env:USERPROFILE | Out-Null
        $env:PATH = "$bin;$($saved['PATH'])"
        $env:STUB_LAUNCH_FAILURE = $Failure
        Remove-Item Env:NVX_SMOKE_SKIP_APPCONTAINER -ErrorAction SilentlyContinue
        if ($GitHubActions) { $env:GITHUB_ACTIONS = 'true' } else { Remove-Item Env:GITHUB_ACTIONS -ErrorAction SilentlyContinue }
        $output = & (Get-Process -Id $PID).Path -NoProfile -File (Join-Path $dir "scripts\$Name") 2>&1 | Out-String
        return [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = $output }
    } finally {
        foreach ($v in $saved.Keys) { [Environment]::SetEnvironmentVariable($v, $saved[$v]) }
    }
}

try {
    foreach ($gate in 'sandbox-smoke.ps1', 'sandbox-smoke-egress.ps1', 'sandbox-enforcement-windows.ps1') {
        Write-Host "${gate}:"

        $dev = Run-Gate $gate $refusal $false
        Check "on a developer machine a refused launch is a skip" ($dev.ExitCode -eq 0 -and $dev.Output -match 'cannot create AppContainer children; skipping')

        $ci = Run-Gate $gate $refusal $true
        Check "on GitHub Actions a refused launch fails" ($ci.ExitCode -ne 0)
        Check "on GitHub Actions it says why" ($ci.Output -match 'cannot create AppContainer children')
        Check "on GitHub Actions it does not call it a skip" ($ci.Output -notmatch 'skipping')

        # Any other launch failure was always a failure, and still is in both places.
        $devOther = Run-Gate $gate $other $false
        $ciOther = Run-Gate $gate $other $true
        Check "another launch failure fails on a developer machine" ($devOther.ExitCode -ne 0)
        Check "another launch failure fails on GitHub Actions" ($ciOther.ExitCode -ne 0)
    }
} finally {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Error "$failures Windows gate check(s) failed"
    exit 1
}
# The gates above leave $LASTEXITCODE at 1 after the failing cases, and a runner
# that calls this script with `pwsh -command` exits with that value.
$global:LASTEXITCODE = 0
Write-Host "Windows gate checks passed."
