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
#
# The child then checks its own session. `iex` runs the script in the caller's
# session, and the installer used to leave $ErrorActionPreference = 'Stop', a
# TLS 1.2-only SecurityProtocol, and its variables and functions behind in the
# PowerShell window it was pasted into. Exit 3 means something leaked.
$childScript = @'
$before = [System.Net.ServicePointManager]::SecurityProtocol
Get-Content -Raw -LiteralPath '__INSTALLER__' | Invoke-Expression
$leaks = @()
if ($ErrorActionPreference -ne 'Continue') { $leaks += "ErrorActionPreference=$ErrorActionPreference" }
if ([System.Net.ServicePointManager]::SecurityProtocol -ne $before) { $leaks += 'SecurityProtocol' }
foreach ($f in 'Set-NvxUserPath', 'Install-NvxDownloadedBinary') {
    if (Get-Command $f -ErrorAction SilentlyContinue) { $leaks += "function $f" }
}
foreach ($v in 'nvxHome', 'binDir', 'nvxInstaller') {
    if (Get-Variable $v -ErrorAction SilentlyContinue) { $leaks += "variable $v" }
}
if ($leaks) {
    Write-Host ('LEAKED into the session: ' + ($leaks -join ', '))
    exit 3
}
'@.Replace('__INSTALLER__', $installer)
$encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($childScript))
$child = Start-Process -FilePath (Get-Process -Id $PID).Path -Wait -PassThru -NoNewWindow -ArgumentList @(
    '-NoProfile', '-OutputFormat', 'Text', '-EncodedCommand', $encoded)

if ($child.ExitCode -eq 3) {
    Write-Error "install.ps1 through Invoke-Expression left its own settings in the session it ran in (see LEAKED above)."
    exit 1
}
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
