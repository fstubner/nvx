# Submits one version of a package to microsoft/winget-pkgs with Komac, so that the
# files that were checked are the files that are sent.
#
#   submit-winget.ps1 -Komac <komac.exe> -PackageIdentifier fstubner.nvx `
#       -PackageVersion 0.8.0 -InstallerUrl <url of nvx.exe> `
#       -ExpectedSha256 <digest> -WorkDirectory <empty folder>
#
# publish.yml's Winget job verifies nvx.exe with verified_sha (scripts/release/lib.sh),
# which checks the sidecar and release.yml's build provenance and prints the digest.
# This script then:
#
#   1. Looks for the package in winget-pkgs. `komac update` adds a version to a package
#      that is already there. `komac new` asks questions that have no flags, so it
#      cannot run without a terminal. A package that is missing stops the job here,
#      and the first version is submitted by hand (packaging/winget/README.md).
#   2. Looks for a pull request for this version in any state, as `komac update
#      --submit` does in CI, and stops quietly when there is one. `komac submit`
#      does not look, and a second run would open a second pull request.
#   3. Runs `komac update --dry-run --output`. That downloads nvx.exe once and writes
#      the manifests it would send. check-winget-sha.ps1 then fails unless they carry
#      the verified digest.
#   4. Runs `komac submit` on that same folder. It reads the files and sends them
#      and downloads nothing. A file replaced on the release page after step 3 cannot
#      reach the pull request, and nothing is sent unless step 3 passed.
#   5. Requires Komac to name the pull request it opened. `komac submit` exits 0 and
#      sends nothing when the folder has no complete set of manifests it can read.
#
# Komac 2.16.0 reads the manifests into its own types and writes them back out when
# it submits. It hoists the fields the installers share and sorts them, and it does
# not change an installer's InstallerSha256.
#
# It throws and does not exit, for the reason check-winget-sha.ps1 gives.
param(
    [Parameter(Mandatory)][string]$Komac,
    [Parameter(Mandatory)][string]$PackageIdentifier,
    [Parameter(Mandatory)][string]$PackageVersion,
    [Parameter(Mandatory)][string]$InstallerUrl,
    [Parameter(Mandatory)][string]$ExpectedSha256,
    [Parameter(Mandatory)][string]$WorkDirectory,
    [string]$Token = $env:GITHUB_TOKEN
)
$ErrorActionPreference = 'Stop'

$headers = @{ Accept = 'application/vnd.github+json'; 'X-GitHub-Api-Version' = '2022-11-28' }
if ($Token) { $headers.Authorization = "Bearer $Token" }

# One GET against the GitHub API. A 404 or a 5xx comes back as a status and does not
# throw, so the caller can tell "not there" from "could not ask".
function Get-GitHub([string]$Path) {
    $response = Invoke-WebRequest -Uri "https://api.github.com$Path" -Headers $headers -SkipHttpErrorCheck
    [pscustomobject]@{ Status = [int]$response.StatusCode; Body = [string]$response.Content }
}

# Runs Komac and keeps what it printed, which is also shown as it arrives. Komac
# writes its progress to stderr, and Windows PowerShell treats that as an error under
# 'Stop', so the preference is relaxed here and only here.
function Invoke-Komac([string[]]$Arguments) {
    $ErrorActionPreference = 'Continue'
    $lines = @(& $Komac @Arguments 2>&1 | ForEach-Object { Write-Host $_; "$_" })
    [pscustomobject]@{ ExitCode = $LASTEXITCODE; Output = $lines -join "`n" }
}

# 1. Is the package in winget-pkgs? Komac looks in the same place.
$packagePath = 'manifests/' + $PackageIdentifier.Substring(0, 1).ToLowerInvariant() + '/' + ($PackageIdentifier -replace '\.', '/')
$lookup = Get-GitHub "/repos/microsoft/winget-pkgs/contents/$packagePath"
if ($lookup.Status -eq 404) {
    throw "$PackageIdentifier is not in microsoft/winget-pkgs yet. ``komac update`` only adds a version to a package that is there, and ``komac new`` asks questions that have no flags, so neither can make the first submission here. Submit the first version by hand, as packaging/winget/README.md describes under 'The first submission'. When that pull request has merged, run this job again."
}
if ($lookup.Status -ne 200) {
    throw "Could not look for $PackageIdentifier in microsoft/winget-pkgs. GitHub answered HTTP $($lookup.Status)."
}

# 2. Is there a pull request for this version already? Komac matches the title on whole
# words, so 0.8.0 does not match 0.8.01.
$query = [uri]::EscapeDataString("repo:microsoft/winget-pkgs is:pr in:title $PackageIdentifier $PackageVersion")
$search = Get-GitHub "/search/issues?q=$query&per_page=100"
if ($search.Status -ne 200) {
    throw "Could not look for a pull request for $PackageIdentifier $PackageVersion. GitHub answered HTTP $($search.Status)."
}
$identifierWord = '(^|\s)' + [regex]::Escape($PackageIdentifier) + '(\s|$)'
$versionWord = '(^|\s)' + [regex]::Escape($PackageVersion) + '(\s|$)'
$existing = @(($search.Body | ConvertFrom-Json).items | Where-Object { $_.title -match $identifierWord -and $_.title -match $versionWord }) | Select-Object -First 1
if ($existing) {
    Write-Host "There is already a pull request for $PackageIdentifier $PackageVersion ($($existing.state)), $($existing.html_url). Nothing to submit."
    return
}

# 3. Write the manifests and check them. The folder has to start empty, or files left
# in it would be sent with the new ones.
if ((Test-Path $WorkDirectory) -and (Get-ChildItem -Path $WorkDirectory -Force | Select-Object -First 1)) {
    throw "$WorkDirectory is not empty."
}
$written = Invoke-Komac @('update', $PackageIdentifier, '--version', $PackageVersion, '--urls', $InstallerUrl, '--dry-run', '--output', $WorkDirectory)
if ($written.ExitCode -ne 0) {
    throw "komac update --dry-run exited $($written.ExitCode)."
}
& (Join-Path $PSScriptRoot 'check-winget-sha.ps1') -ManifestDirectory $WorkDirectory -ExpectedSha256 $ExpectedSha256

# 4 and 5. Send those files, and require a pull request to come of it.
$sent = Invoke-Komac @('submit', $WorkDirectory, '--yes')
if ($sent.ExitCode -ne 0) {
    throw "komac submit exited $($sent.ExitCode)."
}
# Komac prints "Successfully created microsoft/winget-pkgs#123" and, when stdout is
# not a terminal that shows links, the pull request's URL on the next line.
if ($sent.Output -notmatch '(microsoft/winget-pkgs#|https://github\.com/microsoft/winget-pkgs/pull/)\d+') {
    throw "komac submit exited 0 and named no pull request, so nothing was submitted."
}
Write-Host "Submitted $PackageIdentifier $PackageVersion, $($Matches[0])."
