# Egress smoke — a sandboxed fetch must be blocked unless the host is allowlisted,
# and must succeed when it is. Both halves matter: a sandbox that denies everything
# passes the first on its own.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"
if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe .)"
}

# Capturing a native command's stderr is incompatible with
# $ErrorActionPreference = 'Stop' in Windows PowerShell: `2>&1` turns every stderr
# line into an ErrorRecord and 'Stop' makes the first one terminate the script.
# nvx writes its progress lines to stderr, so this exited 1 at the AppContainer
# probe below under powershell 5.1 -- the default shell -- while passing under
# pwsh 7, which is what ci.yml pins. Both sibling scripts had this and were fixed
# first; this one was missed, so the sweep is worth stating: every script under
# scripts/ that sets 'Stop' and redirects a native command's stderr needs this.
#
# Relaxed per call, not globally, so the exit-code checks below keep their teeth:
# $ErrorActionPreference is scoped dynamically, so setting it inside this function
# covers the call and nothing else.
function Invoke-NativeCapture {
    param([Parameter(Mandatory)][string]$Exe, [string[]]$Arguments = @())
    $ErrorActionPreference = 'Continue'
    $out = & $Exe @Arguments 2>&1 | Out-String
    return [pscustomobject]@{ Output = $out; ExitCode = $LASTEXITCODE }
}
if ($env:NVX_SMOKE_SKIP_APPCONTAINER -eq '1') {
    Write-Host "NVX_SMOKE_SKIP_APPCONTAINER=1 set; skipping egress smoke."
    exit 0
}
$startLocation = Get-Location

# Egress is enforced on every Windows host as of 0.5.0, with no elevation and no
# setup step: the AppContainer holds no network capability, and the only route out
# is the parent's proxy, reached over a UNIX socket and relayed by
# `nvx __appcontainer-exec` inside the container.
#
# This script used to gate its assertions on an elevated `nvx setup` having
# registered a loopback exemption, because before the relay that was the only way a
# sandbox could reach the proxy at all. That gate is why the contradiction it
# describes went unnoticed for so long: the assertions only ran on machines that
# happened to have completed setup, which on CI was none of them. Setup no longer
# registers an exemption, so keeping the gate would skip this forever.

# Set-Content -Encoding utf8 writes a BOM under Windows PowerShell 5.1, and nvx's
# JSON parser rejects it ("invalid character 'ï'"). That made every policy written
# here unparseable, so `nvx shim` exited non-zero before reaching the network --
# which is indistinguishable from "egress was blocked" and satisfied the first
# assertion below for entirely the wrong reason. Write the bytes explicitly instead,
# so the file is the same under 5.1 and pwsh.
function Write-PolicyFile {
    param([Parameter(ValueFromPipeline = $true)][string]$Json)
    process {
        [System.IO.File]::WriteAllText(
            (Join-Path (Get-Location).Path ".nvx-policy.json"),
            $Json,
            (New-Object System.Text.UTF8Encoding $false))
    }
}

# The target is registry.npmjs.org rather than example.com. It is the host nvx
# actually needs, it is already in the default allowlist, and example.com does not
# resolve on every network -- a DNS failure there is indistinguishable from a
# working block, so the "allowed" half would fail for a reason unrelated to nvx.
$target = "registry.npmjs.org"
$fetch = "require('https').get('https://$target/left-pad',r=>process.exit(0)).on('error',()=>process.exit(1))"

# Everything this script creates lives under one root the finally block removes,
# and it runs against a throwaway NVX_HOME.
#
# It used to work in $env:USERPROFILE\nvx-egress-smoke -- left behind on every
# run -- against the developer's REAL ~/.nvx: it ran `init-shims` there,
# overwriting the installed shims with the build under test, and tried to
# overwrite the installed nvx.exe, which succeeds on any machine where nvx is not
# currently running. It also mirrored a Node distribution into the real versions
# directory by resolving `node` through PATH, where on a machine with nvx
# installed `node` IS the nvx shim. Its sibling was fixed first and this one was
# missed.
$probeRoot = Join-Path $env:USERPROFILE ".nvx-egress-smoke-probe"
Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
$proj = Join-Path $probeRoot "wd"
New-Item -ItemType Directory -Force -Path $proj | Out-Null

$env:NVX_HOME = Join-Path $probeRoot "nvxhome"
New-Item -ItemType Directory -Force -Path $env:NVX_HOME | Out-Null

try {
Set-Location $proj

# Node 24 specifically, and installed rather than discovered: Node core ignores
# HTTP_PROXY without --use-env-proxy, which arrived in 24, so on anything older
# the allowlisted half could never pass -- the request would go direct and the
# sandbox would correctly refuse it. This used to probe the version on PATH and
# skip when it was too old, which meant the half that tells enforcement from
# breakage was silently not run on most machines. Pinning the runtime removes the
# skip rather than reporting it.
Write-Host "Installing an nvx-managed runtime..."
$installCode = (Invoke-NativeCapture $nvx @('-y', 'install', '24')).ExitCode
$defaultCode = (Invoke-NativeCapture $nvx @('-y', 'default', '24')).ExitCode
if ($installCode -ne 0 -or $defaultCode -ne 0) {
    Write-Host "FAIL: could not install an nvx-managed runtime (install=$installCode default=$defaultCode)." -ForegroundColor Red
    exit 1
}

# isolation.level is "strict" in both policies below because `node` is your own
# code, and at the default "standard" level nvx deliberately does not contain it --
# containment covers installs and ad-hoc tool runners. Without strict, `nvx shim
# node` logs "Running directly (not sandboxed)" and reaches the network freely, so
# this script was measuring an unsandboxed process and calling the result egress
# enforcement.
@'
{
  "isolation": {
    "enabled": true,
    "level": "strict",
    "network": {
      "mode": "proxy",
      "default_allow": [],
      "prompt_unknown": false
    }
  }
}
'@ | Write-PolicyFile

& $nvx init-shims | Out-Null

# A project policy that widens the allowlist needs approval, and without this the
# run blocks on an interactive prompt that never arrives in CI. The policy is
# written by this script, so approving it is the intent.
$env:NVX_YES = "true"

# Can this host create an AppContainer at all? GitHub-hosted Windows runners cannot
# (see the sibling smoke script). Probe once and skip with that reason, so the
# environment's limitation is not reported as a product failure.
$probe = (Invoke-NativeCapture $nvx @('shim', 'node', '-e', 'process.exit(0)')).Output
if ($probe -match 'AppContainer launch failed') {
    Write-Host "This host cannot create AppContainer children; skipping the egress assertions."
    Write-Host ("  " + $probe.Trim())
    exit 0
}

Write-Host "Testing blocked egress via sandboxed node..."
$blocked = Invoke-NativeCapture $nvx @('shim', 'node', '--use-env-proxy', '-e', $fetch)
if ($blocked.ExitCode -eq 0) {
    Write-Error "expected blocked egress to fail, got exit 0"
}

# Asserting only that blocked traffic fails is satisfied by a sandbox that denies
# everything, or one that cannot start a process at all. Allowlist the same host and
# require success, so the test tells enforcement apart from breakage.
Write-Host "Testing allowlisted egress via sandboxed node..."
@'
{
  "isolation": {
    "enabled": true,
    "level": "strict",
    "network": {
      "mode": "proxy",
      "default_allow": ["TARGET:443"],
      "prompt_unknown": false
    }
  }
}
'@.Replace("TARGET", $target) | Write-PolicyFile

$allowed = Invoke-NativeCapture $nvx @('shim', 'node', '--use-env-proxy', '-e', $fetch)
if ($allowed.ExitCode -ne 0) {
    Write-Error "an allowlisted host was blocked; the sandbox is denying everything rather than enforcing a policy (this phase needs outbound network access)"
}

Write-Host "Egress smoke passed: denied what it should, allowed what it should." -ForegroundColor Green
}
finally {
    # Set-Location changes the SESSION's location, so every exit path has to put it
    # back or the next script in the same CI step cannot be found. Nothing this
    # script made outlives it.
    Set-Location $startLocation
    Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
}
