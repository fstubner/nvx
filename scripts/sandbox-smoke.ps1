# Windows sandbox smoke test — run after go build
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$nvx = Join-Path $root "nvx.exe"

# Capturing a native command's stderr is incompatible with
# $ErrorActionPreference = 'Stop' in Windows PowerShell: `2>&1` turns every
# stderr line into an ErrorRecord and 'Stop' makes the first one terminate the
# script. nvx writes its progress lines to stderr, so this script exited 1 at the
# AppContainer probe below under powershell 5.1 -- the default shell on Windows --
# while passing under pwsh 7, which is what ci.yml pins. CONTRIBUTING does not
# name a shell, so a maintainer running the documented gate got a red result that
# was not a product failure. Its sibling sandbox-enforcement-windows.ps1 had the
# same defect.
#
# Relaxed per call rather than globally, so the explicit checks below keep their
# teeth: $ErrorActionPreference is scoped dynamically, so setting it inside this
# function covers the call and nothing else.
function Invoke-NativeCapture {
    param([Parameter(Mandatory)][string]$Exe, [string[]]$Arguments = @())
    $ErrorActionPreference = 'Continue'
    $out = & $Exe @Arguments 2>&1 | Out-String
    return [pscustomobject]@{ Output = $out; ExitCode = $LASTEXITCODE }
}
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

# Everything this script creates lives under one root that the finally block
# removes, and it runs against a throwaway NVX_HOME.
#
# It used to work in $env:USERPROFILE\nvx-smoke-wd -- left behind on every run --
# against the developer's REAL ~/.nvx: it ran `init-shims` there, overwriting the
# installed shims with the build under test, and tried to overwrite the installed
# nvx.exe (it only failed because the file was in use). Worse, it mirrored a Node
# distribution into the real versions directory by resolving `node` through PATH.
# On any machine where nvx is installed, `node` IS the nvx shim, so
# `(Get-Command node).Source` gave the shim directory and
# `node -p process.version` gave nothing -- the script copied ~/.nvx/bin, nvx.exe
# included, into ~/.nvx/versions/node/v. That bogus version then showed up in
# `nvx list`. Measured on the maintainer's machine, which is the only place this
# is run by hand.
#
# The runtime now comes from nvx itself into the throwaway home, the way
# sandbox-enforcement-windows.ps1 already did it. Nothing is read from PATH.
$probeRoot = Join-Path $env:USERPROFILE ".nvx-smoke-probe"
Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
$proj = Join-Path $probeRoot "wd"
New-Item -ItemType Directory -Force -Path $proj | Out-Null

$env:NVX_HOME = Join-Path $probeRoot "nvxhome"
New-Item -ItemType Directory -Force -Path $env:NVX_HOME | Out-Null

try {
Set-Location $proj

# Both checked: $LASTEXITCODE reflects only the last command, so a failed install
# followed by a successful default would read as success.
Write-Host "Installing an nvx-managed runtime..."
$installCode = (Invoke-NativeCapture $nvx @('-y', 'install', '22')).ExitCode
$defaultCode = (Invoke-NativeCapture $nvx @('-y', 'default', '22')).ExitCode
if ($installCode -ne 0 -or $defaultCode -ne 0) {
    Write-Host "FAIL: could not install an nvx-managed runtime (install=$installCode default=$defaultCode)." -ForegroundColor Red
    Write-Host "      Without one the sandbox has nothing it is permitted to execute, so this"
    Write-Host "      script would assert nothing."
    exit 1
}

# isolation.level = strict is required for what this script asserts. At the default
# "standard" level a bare `node -e ...` is classified as YOUR OWN CODE and runs
# uncontained by design, so the host-write assertion below could never hold. That
# has been true since containment v2 landed and went unnoticed because this script
# exited 0 on CI without running.
@{
    isolation = @{ enabled = $true; level = "strict" }
} | ConvertTo-Json -Depth 4 | Set-Content -Path ".nvx-policy.json" -Encoding utf8

& $nvx init-shims | Out-Null

# Can this host create an AppContainer at all? GitHub-hosted Windows runners cannot:
# CreateProcess returns "Access is denied" for every executable, including cmd.exe
# (measured in CI run 32077425413). Probing once and skipping with that reason keeps
# the environment's limitation from being reported as a product failure -- while
# still failing normally everywhere the sandbox does work.
$probe = (Invoke-NativeCapture $nvx @('shim', 'node', '-e', 'process.exit(0)')).Output
if ($probe -match 'AppContainer launch failed') {
    Write-Host "This host cannot create AppContainer children; skipping the containment assertions."
    Write-Host ("  " + $probe.Trim())
    exit 0
}

Write-Host "Testing sandboxed node via shim..."
$probe = Join-Path $proj "probe.txt"
& $nvx shim node -e "require('fs').writeFileSync('probe.txt','ok')"
if (-not (Test-Path $probe)) {
    Write-Error "workdir write failed"
}

$hostFile = Join-Path $env:USERPROFILE "nvx-smoke-host-probe.txt"
# Through the helper: this write is expected to fail, so it is the one call
# guaranteed to put something on stderr, and `2>$null` is caught by 'Stop' just
# as `2>&1` is.
$null = Invoke-NativeCapture $nvx @('shim', 'node', '-e',
    "require('fs').writeFileSync(process.env.USERPROFILE + '/nvx-smoke-host-probe.txt','pwned')")
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

# nvx's own npm, from the throwaway home, rather than whatever `npm` on PATH
# resolves to -- which on a machine with nvx installed is the installed shim.
$null = Invoke-NativeCapture $nvx @('shim', 'npm', 'pack', './dep')
$tgz = Join-Path $lifecycle "nvx-smoke-dep-1.0.0.tgz"
if (-not (Test-Path $tgz)) {
    Write-Error "could not pack the fixture dependency"
}

# NVX_YES only clears the pre-install verification prompt, which does not
# recognise a local tarball path as a package. It has no bearing on stdio.
$env:NVX_YES = "true"
# A background job is a fresh PowerShell session and inherits none of this one's
# environment, so NVX_HOME has to be passed in. Without it the job ran against
# the developer's real ~/.nvx while the rest of the script used the throwaway.
$job = Start-Job -ScriptBlock {
    param($nvxPath, $dir, $pkg, $nvxHome)
    Set-Location $dir
    $env:NVX_YES = "true"
    $env:NVX_HOME = $nvxHome
    & $nvxPath shim npm install $pkg 2>&1 | Out-Null
} -ArgumentList $nvx, $lifecycle, $tgz, $env:NVX_HOME
if (-not (Wait-Job $job -Timeout 240)) {
    Stop-Job $job -ErrorAction SilentlyContinue
    Remove-Job $job -Force -ErrorAction SilentlyContinue
    Write-Error "a contained npm install of a dependency with a postinstall did not finish within 240s"
}
Remove-Job $job -Force -ErrorAction SilentlyContinue

$ran = Join-Path $lifecycle (Join-Path "node_modules" (Join-Path "nvx-smoke-dep" "POSTINSTALL_RAN.txt"))
if (-not (Test-Path $ran)) {
    Write-Error "the dependency postinstall never ran inside the sandbox"
}
# And it captured its child's output rather than merely surviving. Asserting the
# content, not just the file, is the difference between this fixture and the one
# that let "npm installs are unaffected" ship while esbuild hung.
$captured = (Get-Content $ran -Raw).Trim()
if ($captured -ne "CAPTURED") {
    Write-Error "the postinstall ran but captured '$captured' instead of 'CAPTURED': subprocess output capture is broken inside the sandbox"
}

Write-Host "Windows sandbox smoke passed." -ForegroundColor Green
}
finally {
    # Set-Location changes the SESSION's location, so every exit path has to put
    # it back or the next script in the same CI step cannot be found. And nothing
    # this script made outlives it: the throwaway home and the working tree both
    # go, which is what stopped it leaving nvx-smoke-wd in the user's profile.
    Set-Location $startLocation
    Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
}
