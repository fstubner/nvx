# Egress smoke — a sandboxed fetch must be blocked unless the host is allowlisted,
# and must succeed when it is. Both halves matter: a sandbox that denies everything
# passes the first on its own.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"
if (-not (Test-Path $nvx)) {
    Write-Error "Build nvx.exe first (go build -o nvx.exe ./cmd/nvx)"
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
# JSON parser once rejected it ("invalid character 'ï'"). Every policy written
# here was then unparseable, so `nvx shim` exited non-zero before reaching the
# network, which is indistinguishable from "egress was blocked" and satisfied the
# first assertion below for entirely the wrong reason. nvx strips a leading BOM
# now (withoutUTF8BOM in policy.go). The bytes are still written explicitly, so
# the file is the same under 5.1 and pwsh and this check does not lean on that.
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

# Node 24 specifically, and installed rather than discovered: Node core reads
# HTTP_PROXY only when NODE_USE_ENV_PROXY=1, which nvx puts in the contained
# environment, and the https module this script calls reads it from 24.5.0 (and
# from 22.21.0). On anything older the allowlisted half could never pass -- the
# request would go direct and the sandbox would correctly refuse it. This used to
# probe the version on PATH and skip when it was too old, which meant the half that
# tells enforcement from breakage was silently not run on most machines. Pinning
# the runtime removes the skip rather than reporting it.
#
# The node command lines below carry no --use-env-proxy. They used to, which sent
# the request to the proxy whether or not nvx had told node to. Without the flag, a
# pass here means nvx delivered the setting.
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

# Can this host create an AppContainer at all? GitHub-hosted Windows runners could
# not until 2026-09-21 (see the sibling smoke script). Probe once and skip with that
# reason, so a host that refuses is not reported as a product failure.
$probe = (Invoke-NativeCapture $nvx @('shim', 'node', '-e', 'process.exit(0)')).Output
if ($probe -match 'AppContainer launch failed') {
    # Only the two shapes a HOST refusal takes, the same narrowed test as
    # sandbox-enforcement-windows.ps1, which explains it. Any other launch
    # failure is a regression and fails here.
    if ($probe -match 'Access is denied' -or $probe -match 'The system cannot find the file specified') {
        Write-Host "This host cannot create AppContainer children; skipping the egress assertions."
        Write-Host ("  " + $probe.Trim())
        exit 0
    }
    Write-Host ("  " + $probe.Trim())
    Write-Error "the sandbox could not launch, and not in a way this host is known to refuse"
}

Write-Host "Testing blocked egress via sandboxed node..."
$blocked = Invoke-NativeCapture $nvx @('shim', 'node', '-e', $fetch)
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

$allowed = Invoke-NativeCapture $nvx @('shim', 'node', '-e', $fetch)
if ($allowed.ExitCode -ne 0) {
    Write-Error "an allowlisted host was blocked; the sandbox is denying everything rather than enforcing a policy (this phase needs outbound network access)"
}

$utf8n = New-Object System.Text.UTF8Encoding $false

# A server and a client in one sandbox reach each other, and the policy names
# nothing for that. They talk over 127.0.0.1 and Node's fetch and http follow the
# proxy variables, so unless NO_PROXY lists loopback each request goes to nvx's
# proxy, which dials that port on this machine instead and refuses it. The port
# here is not one nvx opened, so only the loopback listing can make this pass.
Write-Host "Testing a server and a client in one sandbox..."
[System.IO.File]::WriteAllText((Join-Path $proj "pair.js"), @'
const http = require('http');
const srv = http.createServer((q, r) => r.end('INSIDE_OK'));
srv.listen(0, '127.0.0.1', async () => {
  const url = 'http://127.0.0.1:' + srv.address().port + '/';
  const out = [];
  try { const r = await fetch(url); out.push('fetch=' + r.status + ':' + (await r.text())); } catch (e) { out.push('fetch=failed'); }
  await new Promise(done => http.get(url, r => { let b = ''; r.on('data', d => (b += d)); r.on('end', () => { out.push('get=' + r.statusCode + ':' + b); done(); }); })
    .on('error', () => { out.push('get=failed'); done(); }));
  console.log('PAIR ' + out.join(' '));
  srv.close();
});
'@, $utf8n)
$pair = Invoke-NativeCapture $nvx @('shim', 'node', 'pair.js')
if ($pair.Output -notmatch 'PAIR fetch=200:INSIDE_OK get=200:INSIDE_OK') {
    Write-Host $pair.Output
    Write-Error "a server and a client in one sandbox could not reach each other: the request went to nvx's proxy"
}

# An allow_hosts entry for a loopback host sends requests to it through the proxy
# instead, so a service on this machine stays reachable for a client that tunnels.
# fetch tunnels with CONNECT, which the proxy serves. The proxy is the only thing
# that writes egress_allow, so the record shows the request went through it.
# Without the entry the same request is refused by the AppContainer, and must not
# reach the service. Both are run, the one without the entry first, because a
# sandbox that shared this machine's loopback would answer 200 to both.
Write-Host "Testing an allow_hosts entry for a service on this machine..."
$hostNode = (Get-ChildItem (Join-Path $env:NVX_HOME "versions\node\*\node.exe") | Select-Object -First 1).FullName
$portFile = Join-Path $probeRoot "service-port.txt"
[System.IO.File]::WriteAllText((Join-Path $probeRoot "service.js"), @'
const fs = require('fs'), http = require('http');
const s = http.createServer((q, r) => r.end('SERVICE_OK'));
s.listen(0, '127.0.0.1', () => fs.writeFileSync(process.argv[2], String(s.address().port)));
'@, $utf8n)
[System.IO.File]::WriteAllText((Join-Path $proj "host-fetch.js"), @'
fetch('http://127.0.0.1:' + process.argv[2] + '/')
  .then(async r => console.log('HOSTFETCH ' + r.status + ' ' + (await r.text())))
  .catch(e => console.log('HOSTFETCH failed ' + ((e.cause && (e.cause.code || e.cause.message)) || e.message)));
'@, $utf8n)
$service = Start-Process -FilePath $hostNode -ArgumentList @("`"$(Join-Path $probeRoot 'service.js')`"", "`"$portFile`"") -PassThru -WindowStyle Hidden
try {
    for ($i = 0; $i -lt 100 -and -not (Test-Path $portFile); $i++) { Start-Sleep -Milliseconds 100 }
    if (-not (Test-Path $portFile)) {
        Write-Error "the stand-in host service never reported its port"
    }
    $servicePort = (Get-Content $portFile -Raw).Trim()

    $noEntry = Invoke-NativeCapture $nvx @('shim', 'node', 'host-fetch.js', $servicePort)
    if ($noEntry.Output -match 'HOSTFETCH 200 SERVICE_OK') {
        Write-Error "a contained fetch reached 127.0.0.1:$servicePort on this machine with no allow_hosts entry"
    }
    if ($noEntry.Output -notmatch 'HOSTFETCH failed') {
        Write-Host $noEntry.Output
        Write-Error "the unreachable-without-an-entry check did not run: the probe neither connected nor reported a failure"
    }

    @'
{
  "isolation": {
    "enabled": true,
    "level": "strict",
    "network": {
      "mode": "proxy",
      "default_allow": ["TARGET:443"],
      "allow_hosts": ["127.0.0.1:SERVICE"],
      "prompt_unknown": false
    }
  }
}
'@.Replace("TARGET", $target).Replace("SERVICE", $servicePort) | Write-PolicyFile

    # An allow_hosts entry widens the policy, which NVX_YES does not approve.
    $env:NVX_TRUST_YES = "true"
    try {
        $withEntry = Invoke-NativeCapture $nvx @('shim', 'node', 'host-fetch.js', $servicePort)
    } finally {
        Remove-Item Env:NVX_TRUST_YES -ErrorAction SilentlyContinue
    }
    if ($withEntry.Output -notmatch 'HOSTFETCH 200 SERVICE_OK') {
        Write-Host $withEntry.Output
        Write-Error "an allow_hosts entry for 127.0.0.1:$servicePort did not reach the service through the proxy"
    }
    $recorded = Select-String -Path (Join-Path $env:NVX_HOME "audit.log") -SimpleMatch -Pattern "`"event`":`"egress_allow`",`"host`":`"127.0.0.1:$servicePort`"" |
        Where-Object { $_.Line -match '"rule":"allow_hosts"' }
    if (-not $recorded) {
        Write-Error "the request reached the service, but the proxy wrote no egress_allow for it: it did not go through the proxy"
    }
} finally {
    Stop-Process -Id $service.Id -Force -ErrorAction SilentlyContinue
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
