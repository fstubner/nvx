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

# A DEPENDENCY's lifecycle script, which is the thing the sandbox exists to
# contain and the one shape nothing exercised.
#
# A contained process cannot create a named pipe, and libuv builds piped child
# stdio out of named pipes, so npm's default of piping script output made every
# install of a script-bearing dependency hang forever -- inside libuv, before the
# child existed. The suite launched contained children from the parent and never
# had a contained process spawn one, so it stayed green through the whole failure.
#
# It must be a dependency, not this package's own script: a root postinstall runs
# fine either way, and an earlier version of this check used one and passed
# happily against a build with the fix reverted.
Write-Host "Testing a contained dependency lifecycle script..."
$lifecycle = Join-Path $proj "lifecycle"
Remove-Item $lifecycle -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path (Join-Path $lifecycle "dep") | Out-Null
Set-Location $lifecycle

$utf8 = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText((Join-Path $lifecycle "package.json"),
    '{"name":"nvx-smoke-host","version":"1.0.0"}', $utf8)
[System.IO.File]::WriteAllText((Join-Path $lifecycle "dep\package.json"),
    '{"name":"nvx-smoke-dep","version":"1.0.0","scripts":{"postinstall":"node postinstall.js"}}', $utf8)
# The postinstall CAPTURES A SUBPROCESS'S OUTPUT, which is the case this fixture
# could not previously reach: it only wrote a file, so it passed while
# `npm install esbuild` hung forever on exactly this. A contained process cannot
# create a named pipe, and that is how Windows implements piped child stdio, so
# without the stdio preload the line below blocks inside libuv and the install
# never returns.
[System.IO.File]::WriteAllText((Join-Path $lifecycle "dep\postinstall.js"),
    'const cp=require("child_process");' +
    'const out=cp.execFileSync(process.execPath,["-e","process.stdout.write(\"CAPTURED\")"],{encoding:"utf8"});' +
    'if(out.trim()!=="CAPTURED"){throw new Error("capture returned "+JSON.stringify(out))}' +
    'require("fs").writeFileSync("POSTINSTALL_RAN.txt",out)', $utf8)

& npm pack ./dep 2>&1 | Out-Null
$tgz = Join-Path $lifecycle "nvx-smoke-dep-1.0.0.tgz"
if (-not (Test-Path $tgz)) {
    Set-Location $startLocation
    Write-Error "could not pack the fixture dependency"
}

# NVX_YES only clears the pre-install verification prompt, which does not
# recognise a local tarball path as a package. It has no bearing on stdio.
$env:NVX_YES = "true"
$job = Start-Job -ScriptBlock {
    param($nvxPath, $dir, $pkg)
    Set-Location $dir
    $env:NVX_YES = "true"
    & $nvxPath shim npm install $pkg 2>&1 | Out-Null
} -ArgumentList $nvx, $lifecycle, $tgz
if (-not (Wait-Job $job -Timeout 240)) {
    Stop-Job $job -ErrorAction SilentlyContinue
    Remove-Job $job -Force -ErrorAction SilentlyContinue
    Set-Location $startLocation
    Write-Error "a contained npm install of a dependency with a postinstall did not finish within 240s"
}
Remove-Job $job -Force -ErrorAction SilentlyContinue

$ran = Join-Path $lifecycle (Join-Path "node_modules" (Join-Path "nvx-smoke-dep" "POSTINSTALL_RAN.txt"))
if (-not (Test-Path $ran)) {
    Set-Location $startLocation
    Write-Error "the dependency postinstall never ran inside the sandbox"
}
# And it captured its child's output rather than merely surviving. Asserting the
# content, not just the file, is the difference between this fixture and the one
# that let "npm installs are unaffected" ship while esbuild hung.
$captured = (Get-Content $ran -Raw).Trim()
if ($captured -ne "CAPTURED") {
    Set-Location $startLocation
    Write-Error "the postinstall ran but captured '$captured' instead of 'CAPTURED': subprocess output capture is broken inside the sandbox"
}

Set-Location $startLocation
Write-Host "Windows sandbox smoke passed." -ForegroundColor Green
