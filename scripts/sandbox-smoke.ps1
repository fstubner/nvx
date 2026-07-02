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

$proj = Join-Path $env:TEMP "nvx-smoke-wd"
New-Item -ItemType Directory -Force -Path $proj | Out-Null
Set-Location $proj

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
