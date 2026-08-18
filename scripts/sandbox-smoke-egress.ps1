# Egress smoke — a sandboxed fetch must be blocked unless the host is allowlisted,
# and must succeed when it is. Both halves matter: a sandbox that denies everything
# passes the first on its own.
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

$ver = & node -p "process.version.slice(1)"
if ([int]($ver.Split('.')[0]) -lt 24) {
    # Node core ignores HTTP_PROXY without --use-env-proxy (Node 24+). On an older
    # node the allowlisted half can never pass, because the request would go direct
    # -- which the sandbox correctly refuses. Skip rather than report that as a
    # product failure.
    Write-Host "node $ver has no --use-env-proxy (needs 24+); skipping the egress assertions."
    Set-Location $startLocation
    exit 0
}

$proj = Join-Path $env:USERPROFILE "nvx-egress-smoke"
New-Item -ItemType Directory -Force -Path $proj | Out-Null
Set-Location $proj

$nvxVer = "v$ver"
$nodeSrc = Split-Path (Get-Command node).Source -Parent
$nvxNode = Join-Path $env:USERPROFILE ".nvx\versions\node\$nvxVer"
if (-not (Test-Path (Join-Path $nvxNode "node.exe"))) {
    New-Item -ItemType Directory -Force -Path $nvxNode | Out-Null
    Copy-Item -Path "$nodeSrc\*" -Destination $nvxNode -Recurse -Force
}

# isolation.level is "strict" in both policies below because `node` is your own
# code, and at the default "standard" level nvx deliberately does not contain it --
# containment covers installs and ad-hoc tool runners. Without strict, `nvx shim
# node` logs "Running directly (not sandboxed)" and reaches the network freely, so
# this script was measuring an unsandboxed process and calling the result egress
# enforcement.
@'
{
  "runtime": {
    "default": "node",
    "versions": { "node": "PLACEHOLDER" }
  },
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
'@.Replace("PLACEHOLDER", $ver) | Write-PolicyFile

& $nvx init-shims | Out-Null

# A project policy that widens the allowlist needs approval, and without this the
# run blocks on an interactive prompt that never arrives in CI. The policy is
# written by this script, so approving it is the intent.
$env:NVX_YES = "true"

# Can this host create an AppContainer at all? GitHub-hosted Windows runners cannot
# (see the sibling smoke script). Probe once and skip with that reason, so the
# environment's limitation is not reported as a product failure.
$probe = & $nvx shim node -e "process.exit(0)" 2>&1 | Out-String
if ($probe -match 'AppContainer launch failed') {
    Write-Host "This host cannot create AppContainer children; skipping the egress assertions."
    Write-Host ("  " + $probe.Trim())
    Set-Location $startLocation
    exit 0
}

Write-Host "Testing blocked egress via sandboxed node..."
& $nvx shim node --use-env-proxy -e $fetch 2>&1 | Out-Null
if ($LASTEXITCODE -eq 0) {
    Set-Location $startLocation
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
    "level": "strict",
    "network": {
      "mode": "proxy",
      "default_allow": ["TARGET:443"],
      "prompt_unknown": false
    }
  }
}
'@.Replace("PLACEHOLDER", $ver).Replace("TARGET", $target) | Write-PolicyFile

& $nvx shim node --use-env-proxy -e $fetch 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Set-Location $startLocation
    Write-Error "an allowlisted host was blocked; the sandbox is denying everything rather than enforcing a policy (this phase needs outbound network access)"
}

Set-Location $startLocation
Write-Host "Egress smoke passed: denied what it should, allowed what it should." -ForegroundColor Green
