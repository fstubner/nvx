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

# GitHub-hosted Windows runners cannot spawn AppContainer children (CreateProcess
# fails for all PE paths even with icacls grants). Same pattern as Linux netns skip.
if ($env:GITHUB_ACTIONS -eq 'true') {
    Write-Host "AppContainer unavailable on GitHub Actions Windows runners; skipping native sandbox smoke."
    exit 0
}

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
@{
    runtime = @{ default = "node"; versions = @{ node = $ver } }
} | ConvertTo-Json -Depth 4 | Set-Content -Path ".nvx-policy.json" -Encoding utf8

& $nvx init-shims | Out-Null

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

Write-Host "Windows sandbox smoke passed." -ForegroundColor Green
