# The documented Windows install, run the documented way: the whole of
# install.ps1 through Invoke-Expression, as `irm https://nvx.run/install.ps1 | iex`
# does.
#
# Every other installer test dot-sources install.ps1 -LibraryOnly, which is a
# file run with a script directory. `iex` has none, and the script read
# $PSScriptRoot unconditionally, so every one-line install stopped with "Cannot
# bind argument to parameter 'Path' because it is an empty string" -- after it
# had already edited PATH and the profile. Nothing ran the script the way users
# run it, so nothing saw that.
#
# CI only: it installs nvx for the account it runs under, writing the User PATH
# and the PowerShell profile. It downloads the latest published release.

$ErrorActionPreference = 'Stop'

if (-not $env:CI) {
    Write-Error "This test installs nvx into the current account. Run it in CI, or set CI=1 if you mean it."
    exit 1
}

$installer = Join-Path $PSScriptRoot '..\install.ps1'
$bin = Join-Path $HOME '.nvx\bin\nvx.exe'

# In a child process, so the installer's own settings stay out of this one.
$child = Start-Process -FilePath (Get-Process -Id $PID).Path -Wait -PassThru -NoNewWindow -ArgumentList @(
    '-NoProfile', '-Command',
    "Get-Content -Raw -LiteralPath '$installer' | Invoke-Expression")

if ($child.ExitCode -ne 0) {
    Write-Error "install.ps1 through Invoke-Expression exited $($child.ExitCode)."
    exit 1
}
if (-not (Test-Path $bin)) {
    Write-Error "install.ps1 through Invoke-Expression finished without installing $bin."
    exit 1
}
$version = & $bin version
if ($LASTEXITCODE -ne 0 -or $version -notmatch '^nvx version ') {
    Write-Error "The installed nvx did not run: $version"
    exit 1
}
Write-Host "install.ps1 through Invoke-Expression installed: $version"
