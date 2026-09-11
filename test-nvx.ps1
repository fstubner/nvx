# test-nvx.ps1
# Verifies that the INSTALLED nvx activates a runtime and isolates global npm.
#
# Against a throwaway NVX_HOME, which is the third time this correction has been
# made in this repository and the reason it is spelled out here.
#
# scripts/sandbox-smoke.ps1 and scripts/sandbox-smoke-egress.ps1 both carry the
# same note: they used to run against the developer's REAL ~/.nvx, where
# `init-shims` overwrote the installed shims with the build under test, an
# attempted overwrite of nvx.exe succeeded whenever nvx was not running, and a
# Node distribution was mirrored into the real versions directory -- on a machine
# with nvx installed, `node` IS the nvx shim, so resolving it through PATH copied
# ~/.nvx/bin into ~/.nvx/versions/node/v and that bogus entry then showed up in
# `nvx list`. Both were fixed. This script was missed.
#
# What it did until now: installed Node 18 into the real store and ran
# `npm install -g is-sorted` there, leaving both behind. Harmless-looking, and
# still a script that silently changes the machine of anyone who runs it to check
# something.
#
# The binary under test is deliberately the installed one -- this verifies an
# installation, not a build tree -- and only the HOME it works in is redirected.
# NVX_HOME is what nvx resolves everything else from, so pointing it at a scratch
# directory moves the versions, the shims and the npm prefix together.
$ErrorActionPreference = 'Stop'

$nvx = Join-Path $env:USERPROFILE ".nvx\bin\nvx.exe"
if (-not (Test-Path $nvx)) {
    Write-Host "FAIL: no installed nvx at $nvx." -ForegroundColor Red
    Write-Host "      This script checks an installation; run install.ps1 first."
    exit 1
}

# Everything created here lives under one root the finally block removes.
$probeRoot = Join-Path $env:USERPROFILE ".nvx-verify-probe"
Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
$env:NVX_HOME = Join-Path $probeRoot "nvxhome"
New-Item -ItemType Directory -Force -Path $env:NVX_HOME | Out-Null

try {
    Write-Host "1. Installing Node 18 into a throwaway NVX_HOME..." -ForegroundColor Cyan
    Write-Host "   NVX_HOME = $env:NVX_HOME"
    & $nvx -y install 18
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL: could not install Node 18 (exit $LASTEXITCODE)." -ForegroundColor Red
        exit 1
    }

    $versions = Get-ChildItem -Path (Join-Path $env:NVX_HOME "versions") -ErrorAction SilentlyContinue
    Write-Host "Installed versions in the throwaway home:"
    $versions | Format-Table -Property Name

    # The shims have to exist in the scratch home, or the PATH the wrapper emits
    # leads to a bin directory with nothing in it.
    & $nvx init-shims | Out-Null

    Write-Host "2. Evaluating nvx environment wrapper..." -ForegroundColor Cyan
    & $nvx env --shell=powershell | Out-String | Invoke-Expression

    # Through the wrapper function step 2 just defined, not the binary directly.
    #
    # `nvx use` emits the shell's own syntax for the caller to evaluate, and with
    # no --shell it emits bash. Calling the exe here and piping it into
    # Invoke-Expression therefore died with "the term 'export' is not recognized"
    # -- measured, after this line was written that way. The wrapper is also how a
    # person actually uses it, which is what this script is here to verify.
    Write-Host "3. Activating Node 18..." -ForegroundColor Cyan
    nvx use 18

    Write-Host "4. Verifying active Node runtime..." -ForegroundColor Cyan
    $nodePath = (Get-Command node -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source)
    if (-not $nodePath) {
        Write-Host "FAIL: node is not resolvable after 'nvx use'." -ForegroundColor Red
        exit 1
    }
    Write-Host "Node path: $nodePath"
    $nodeVer = node -v
    Write-Host "Node version: $nodeVer"

    # It must be the throwaway home's runtime, not one already on the machine.
    # Without this the whole script can pass against the developer's existing Node
    # while asserting nothing about what nvx just installed.
    if (-not $nodePath.StartsWith($probeRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        Write-Host "FAIL: node resolved to $nodePath, which is outside the throwaway home." -ForegroundColor Red
        Write-Host "      This run would be measuring a runtime nvx did not install."
        exit 1
    }

    Write-Host "5. Checking NPM isolation..." -ForegroundColor Cyan
    Write-Host "NPM_CONFIG_PREFIX: $env:NPM_CONFIG_PREFIX"
    if (-not $env:NPM_CONFIG_PREFIX) {
        Write-Host "FAIL: NPM_CONFIG_PREFIX is not set, so global installs are not isolated." -ForegroundColor Red
        exit 1
    }
    if ($env:PATH -like "*npm_global*") {
        Write-Host "PATH contains npm_global: Yes" -ForegroundColor Green
    } else {
        Write-Host "PATH contains npm_global: No" -ForegroundColor Red
        exit 1
    }

    # --no-sandbox, because nvx refuses a global install inside the sandbox: it
    # needs write access to a location every future nvx invocation trusts, and a
    # contained install must not be able to plant something that later runs
    # un-contained. nvx's own refusal prescribes this flag.
    #
    # Running it uncontained does not weaken what this step checks. The assertion
    # is that a global install lands in the version's isolated npm_global prefix
    # rather than in a machine-wide one -- that is NPM_CONFIG_PREFIX doing its
    # job, which is a different guarantee from containment and the only one this
    # script is about.
    #
    # Until now this line ran contained and therefore failed for everyone, on any
    # home, since the global-install guard landed. The throwaway-home fix above is
    # what made that visible rather than something that scrolled past after the
    # real store had already been written to.
    Write-Host "6. Installing a global npm package (uncontained: see below)..." -ForegroundColor Cyan
    & $nvx -y --no-sandbox shim npm install -g is-sorted
    if ($LASTEXITCODE -ne 0) {
        Write-Host "FAIL: the global install failed (exit $LASTEXITCODE)." -ForegroundColor Red
        exit 1
    }

    $packagePath = Join-Path $env:NPM_CONFIG_PREFIX "node_modules\is-sorted"
    if (Test-Path $packagePath) {
        Write-Host "Global package successfully isolated under the nvx version folder!" -ForegroundColor Green
    } else {
        Write-Host "Global package was not found in the isolated folder: $packagePath" -ForegroundColor Red
        exit 1
    }

    Write-Host "`nAll verifications passed successfully!" -ForegroundColor Green
}
finally {
    # Nothing this script made outlives it. The PATH and NVX_HOME changes above are
    # this process's own and die with it; the directory would not.
    Remove-Item $probeRoot -Recurse -Force -ErrorAction SilentlyContinue
}
