# Egress block smoke — sandboxed fetch to non-allowlisted host must fail
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"
if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe .)"
}

$proj = Join-Path $env:USERPROFILE "nvx-egress-smoke"
New-Item -ItemType Directory -Force -Path $proj | Out-Null
Set-Location $proj

@'
{
  "isolation": {
    "enabled": true,
    "network": {
      "mode": "proxy",
      "default_allow": [],
      "prompt_unknown": false
    }
  }
}
'@ | Set-Content -Path ".nvx-policy.json" -Encoding utf8

& $nvx init-shims | Out-Null

Write-Host "Testing blocked egress via sandboxed node..."
$out = & $nvx shim node -e "require('https').get('https://example.com',r=>process.exit(0)).on('error',()=>process.exit(1))" 2>&1
$code = $LASTEXITCODE
if ($code -eq 0) {
    Write-Error "expected blocked egress to fail, got exit 0"
}

Write-Host "Egress block smoke passed." -ForegroundColor Green
