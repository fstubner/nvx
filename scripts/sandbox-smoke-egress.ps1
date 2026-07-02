# Egress block smoke — sandboxed fetch to non-allowlisted host must fail
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"
if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe .)"
}
if ($env:GITHUB_ACTIONS -eq 'true') {
    Write-Host "AppContainer unavailable on GitHub Actions Windows runners; skipping egress smoke."
    exit 0
}

$proj = Join-Path $env:USERPROFILE "nvx-egress-smoke"
New-Item -ItemType Directory -Force -Path $proj | Out-Null
Set-Location $proj

$ver = & node -p "process.version.slice(1)"
$nvxVer = "v$ver"
$nodeSrc = Split-Path (Get-Command node).Source -Parent
$nvxNode = Join-Path $env:USERPROFILE ".nvx\versions\node\$nvxVer"
if (-not (Test-Path (Join-Path $nvxNode "node.exe"))) {
    New-Item -ItemType Directory -Force -Path $nvxNode | Out-Null
    Copy-Item -Path "$nodeSrc\*" -Destination $nvxNode -Recurse -Force
}

@'
{
  "runtime": {
    "default": "node",
    "versions": { "node": "PLACEHOLDER" }
  },
  "isolation": {
    "enabled": true,
    "network": {
      "mode": "proxy",
      "default_allow": [],
      "prompt_unknown": false
    }
  }
}
'@.Replace("PLACEHOLDER", $ver) | Set-Content -Path ".nvx-policy.json" -Encoding utf8

& $nvx init-shims | Out-Null

Write-Host "Testing blocked egress via sandboxed node..."
$out = & $nvx shim node -e "require('https').get('https://example.com',r=>process.exit(0)).on('error',()=>process.exit(1))" 2>&1
$code = $LASTEXITCODE
if ($code -eq 0) {
    Write-Error "expected blocked egress to fail, got exit 0"
}

Write-Host "Egress block smoke passed." -ForegroundColor Green
