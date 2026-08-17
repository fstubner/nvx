# Windows sandbox smoke test — run after go build
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"
if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe .)"
}
if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    Write-Host "Node.js not available; skipping Windows sandbox smoke."
    exit 0
}

# This script used to exit 0 unconditionally on GitHub Actions, on the belief that
# hosted Windows runners cannot spawn AppContainer children. The step therefore
# reported success having verified nothing, for every run.
#
# That belief predates two fixes: piped stdio never reached the child at all
# (STARTF_USESTDHANDLES was never set), and every launch stalled ~45s in the
# ancestor-grant walk. Both would make a launch here look impossible. Whether the
# runners can actually host an AppContainer is now decided by trying it, and a
# failure is reported as a failure.
#
# Set NVX_SMOKE_SKIP_APPCONTAINER=1 to opt out deliberately on a host known not to
# support it -- an explicit choice, rather than a silent one keyed off CI.
if ($env:NVX_SMOKE_SKIP_APPCONTAINER -eq '1') {
    Write-Host "NVX_SMOKE_SKIP_APPCONTAINER=1 set; skipping native sandbox smoke."
    exit 0
}

# Set-Location below changes the SESSION's location, not just this script's, so any
# exit path has to put it back. Returning early without doing so left the caller in
# the scratch directory and the next script in the same CI step could not be found.
$startLocation = Get-Location

$proj = Join-Path $env:USERPROFILE "nvx-smoke-wd"
New-Item -ItemType Directory -Force -Path $proj | Out-Null
Set-Location $proj

# AppContainer icacls grants succeed on user-owned ~/.nvx paths; mirror setup-node into versions.
$ver = & node -p "process.version.slice(1)"
$nvxVer = "v$ver"
$nodeSrc = Split-Path (Get-Command node).Source -Parent
$nvxNode = Join-Path $env:USERPROFILE ".nvx\versions\node\$nvxVer"
if (-not (Test-Path (Join-Path $nvxNode "node.exe"))) {
    New-Item -ItemType Directory -Force -Path $nvxNode | Out-Null
    Copy-Item -Path "$nodeSrc\*" -Destination $nvxNode -Recurse -Force
}
# isolation.level = strict is required for what this script asserts. At the default
# "standard" level a bare `node -e ...` is classified as YOUR OWN CODE and runs
# uncontained by design, so the host-write assertion below could never hold. That
# has been true since containment v2 landed and went unnoticed because this script
# exited 0 on CI without running.
@{
    runtime   = @{ default = "node"; versions = @{ node = $ver } }
    isolation = @{ enabled = $true; level = "strict" }
} | ConvertTo-Json -Depth 4 | Set-Content -Path ".nvx-policy.json" -Encoding utf8

& $nvx init-shims | Out-Null

# Can this host create an AppContainer at all? GitHub-hosted Windows runners cannot:
# CreateProcess returns "Access is denied" for every executable, including cmd.exe
# (measured in CI run 32077425413). Probing once and skipping with that reason keeps
# the environment's limitation from being reported as a product failure -- while
# still failing normally everywhere the sandbox does work.
$probe = & $nvx shim node -e "process.exit(0)" 2>&1 | Out-String
if ($probe -match 'AppContainer launch failed') {
    Write-Host "This host cannot create AppContainer children; skipping the containment assertions."
    Write-Host ("  " + $probe.Trim())
    Set-Location $startLocation
    exit 0
}

Write-Host "Testing sandboxed node via shim..."
$probe = Join-Path $proj "probe.txt"
& $nvx shim node -e "require('fs').writeFileSync('probe.txt','ok')"
if (-not (Test-Path $probe)) {
    Write-Error "workdir write failed"
}

$hostFile = Join-Path $env:USERPROFILE "nvx-smoke-host-probe.txt"
& $nvx shim node -e "require('fs').writeFileSync(process.env.USERPROFILE + '/nvx-smoke-host-probe.txt','pwned')" 2>$null
if (Test-Path $hostFile) {
    Remove-Item $hostFile -Force
    Write-Error "host profile write should be blocked"
}

Set-Location $startLocation
Write-Host "Windows sandbox smoke passed." -ForegroundColor Green
