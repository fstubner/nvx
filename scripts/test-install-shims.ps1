# Covers install.ps1's shim step, against a stand-in nvx.exe.
#
# The installer used to leave the shims for the first PowerShell profile that ran
# `nvx env`. A first terminal that never loads one (cmd, Git Bash, an agent started
# from a GUI app) then had nvx on PATH and nothing to intercept with, and npm ran
# unprotected without saying so. The step now runs `nvx init-shims`, and what it
# does when that fails decides whether the install says so.
#
# The stand-in is a small Go program built here, because the step has to run a
# real executable: it records the arguments and the directory it was started in,
# and exits with the code the test asks for. Nothing installs anything. The step
# against the real nvx.exe is covered by scripts/test-install-iex.ps1, which checks
# that the shims exist afterwards.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $root 'install.ps1') -LibraryOnly

$failures = 0
$dir = Join-Path ([IO.Path]::GetTempPath()) ("nvx-install-shims-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $dir | Out-Null

function Check($label, $ok) {
    if ($ok) {
        Write-Host "  ok   $label"
    } else {
        Write-Host "  FAIL $label"
        $script:failures++
    }
}

$source = Join-Path $dir 'main.go'
Set-Content -Path $source -Encoding ascii -Value @'
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	wd, _ := os.Getwd()
	line := fmt.Sprintf("args=%q cwd=%s", os.Args[1:], wd)
	os.WriteFile(os.Getenv("STUB_LOG"), []byte(line), 0o600)
	fmt.Fprintln(os.Stderr, "stub nvx: progress goes to stderr")
	code, _ := strconv.Atoi(os.Getenv("STUB_EXIT"))
	os.Exit(code)
}
'@
$stub = Join-Path $dir 'nvx.exe'
Push-Location $dir
try { & go build -o $stub $source } finally { Pop-Location }
if ($LASTEXITCODE -ne 0) { Write-Error "could not build the stand-in nvx.exe"; exit 1 }

$log = Join-Path $dir 'stub.log'
$env:STUB_LOG = $log

function Run([int]$ExitCode) {
    $env:STUB_EXIT = "$ExitCode"
    Remove-Item $log -Force -ErrorAction SilentlyContinue
    try {
        Initialize-NvxShims -NvxExe $stub
        return $null
    } catch {
        return "$_"
    }
}

try {
    $start = (Get-Location).Path

    Write-Host "A shim step that succeeds:"
    $ok = Run 0
    Check "does not throw" ($null -eq $ok)
    $seen = Get-Content $log -Raw
    Check "runs init-shims and nothing else" ($seen -match '^args=\["init-shims"\] ')
    Check "starts in the Windows directory, not wherever the installer was run" ($seen -match ('cwd=' + [regex]::Escape($env:SystemRoot) + '$'))
    Check "puts the caller back where it was" ((Get-Location).Path -eq $start)

    Write-Host "A shim step that fails:"
    $bad = Run 3
    Check "throws" ($null -ne $bad)
    Check "says what the exit code was" ($bad -match 'exited 3')
    Check "puts the caller back where it was" ((Get-Location).Path -eq $start)
} finally {
    Remove-Item Env:STUB_LOG, Env:STUB_EXIT -ErrorAction SilentlyContinue
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Error "$failures installer shim check(s) failed"
    exit 1
}
# The stand-in leaves $LASTEXITCODE at 3 after the failing case, and a runner that
# calls this script with `pwsh -command` exits with that value.
$global:LASTEXITCODE = 0
Write-Host "Installer shim checks passed."
