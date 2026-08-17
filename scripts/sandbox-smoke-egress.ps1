# Egress block smoke — sandboxed fetch to non-allowlisted host must fail
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"
if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe .)"
}
if ($env:NVX_SMOKE_SKIP_APPCONTAINER -eq '1') {
    Write-Host "NVX_SMOKE_SKIP_APPCONTAINER=1 set; skipping egress smoke."
    exit 0
}

# Windows only restricts egress once an elevated `nvx setup` has registered the
# loopback exemption. Without it the sandbox is granted internetClient and connects
# directly, so a reachable host is the CORRECT behaviour and asserting a block would
# test a guarantee the platform does not make (see docs/enforcement-matrix.md note 3).
#
# This script used to skip on CI and assert the block everywhere else, which is why
# the contradiction went unnoticed: the assertion only ran on machines that happened
# to have completed setup.
$setupMarker = Join-Path $env:USERPROFILE ".nvx\windows-setup.json"
$loopbackExempt = $false
if (Test-Path $setupMarker) {
    try {
        $loopbackExempt = [bool](Get-Content $setupMarker -Raw | ConvertFrom-Json).loopback_exempt
    } catch {
        $loopbackExempt = $false
    }
}
if (-not $loopbackExempt) {
    Write-Host "Loopback exemption absent (no elevated 'nvx setup'), so egress is not allowlisted on this host."
    Write-Host "Skipping the egress assertion: with internetClient granted and no proxy, reaching a host is expected."
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
$fetch = "require('https').get('https://example.com',r=>process.exit(0)).on('error',()=>process.exit(1))"

& $nvx shim node -e $fetch 2>&1 | Out-Null
if ($LASTEXITCODE -eq 0) {
    Write-Error "expected blocked egress to fail, got exit 0"
}

# Asserting only that blocked traffic fails is satisfied by a sandbox that denies
# everything, or one that cannot start a process at all. Allowlist the same host and
# require success, so the test tells enforcement apart from breakage.
Write-Host "Testing allowlisted egress via sandboxed node..."
@'
{
  "runtime": { "default": "node", "versions": { "node": "PLACEHOLDER" } },
  "isolation": {
    "enabled": true,
    "network": {
      "mode": "proxy",
      "default_allow": ["example.com:443"],
      "prompt_unknown": false
    }
  }
}
'@.Replace("PLACEHOLDER", $ver) | Set-Content -Path ".nvx-policy.json" -Encoding utf8

& $nvx shim node -e $fetch 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Error "an allowlisted host was blocked; the sandbox is denying everything rather than enforcing a policy (this phase needs outbound network access)"
}

Write-Host "Egress smoke passed: denied what it should, allowed what it should." -ForegroundColor Green
