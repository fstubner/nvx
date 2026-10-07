# Regression tests for submit-winget.ps1.
#
# It runs only during a release, in the Winget job, so a script that submitted before
# it checked, or that submitted twice, would not surface until a pull request was
# open on microsoft/winget-pkgs. Komac is a stub script that writes the manifests a
# real run would and logs how it was called. GitHub is a function that answers the
# two lookups, which PowerShell prefers to the cmdlet of the same name.
#
# Run: pwsh scripts/release/submit-winget_test.ps1
$ErrorActionPreference = 'Stop'
$submit = Join-Path $PSScriptRoot 'submit-winget.ps1'

$failures = 0
$dir = Join-Path ([IO.Path]::GetTempPath()) ("nvx-submit-winget-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $dir | Out-Null

function Check($label, $ok) {
    if ($ok) {
        Write-Host "  ok   $label"
    } else {
        Write-Host "  FAIL $label"
        $script:failures++
    }
}

$good = 'a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90'
$other = 'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'
$url = 'https://github.com/fstubner/nvx/releases/download/v0.8.0/nvx.exe'

# The stand-in for komac.exe. The test chooses what it does with STUB_* variables.
# Its lines go to the output stream, as a program's do.
$stub = Join-Path $dir 'komac.ps1'
Set-Content -Path $stub -Value @'
Add-Content -Path $env:STUB_LOG -Value ($args -join ' ')
if ($args[0] -eq 'update') {
    # Without --dry-run the real one would open the pull request.
    if ($args -notcontains '--dry-run') { 'update without --dry-run'; exit 9 }
    $out = $args[[array]::IndexOf($args, '--output') + 1]
    $version = $args[[array]::IndexOf($args, '--version') + 1]
    $leaf = Join-Path $out "manifests\f\fstubner\nvx\$version"
    New-Item -ItemType Directory -Path $leaf -Force | Out-Null
    Set-Content -Path (Join-Path $leaf 'fstubner.nvx.yaml') -Value 'ManifestType: version'
    Set-Content -Path (Join-Path $leaf 'fstubner.nvx.locale.en-US.yaml') -Value 'ManifestType: defaultLocale'
    Set-Content -Path (Join-Path $leaf 'fstubner.nvx.installer.yaml') -Value @(
        'Installers:', '- Architecture: x64', "  InstallerSha256: $env:STUB_SHA", 'ManifestType: installer')
    Add-Content -Path $env:STUB_LOG -Value ('wrote ' + (Get-FileHash (Join-Path $leaf 'fstubner.nvx.installer.yaml')).Hash)
    exit [int]$env:STUB_UPDATE_EXIT
}
if ($args[0] -eq 'submit') {
    $installer = Get-ChildItem -Path $args[1] -Recurse -Filter '*.installer.yaml' | Select-Object -First 1
    Add-Content -Path $env:STUB_LOG -Value ('sent ' + (Get-FileHash $installer.FullName).Hash)
    if ($env:STUB_SUBMIT -eq 'fail') { 'GitHub said no'; exit 3 }
    if ($env:STUB_SUBMIT -eq 'nothing') { "No valid packages to submit were found in $($args[1])"; exit 0 }
    # A terminal that shows links gets the number and no separate url line.
    if ($env:STUB_SUBMIT -ne 'url-only') { 'Successfully created microsoft/winget-pkgs#4242' }
    if ($env:STUB_SUBMIT -ne 'number-only') { 'https://github.com/microsoft/winget-pkgs/pull/4242' }
    exit 0
}
exit 8
'@

# What GitHub answers, and what it was asked. Each case sets the answers. This is a
# table because the function below runs inside submit-winget.ps1, and $script: there
# is that script's scope.
$github = @{ Lookup = 200; Search = 200; PullRequests = @(); Requests = @() }
function Invoke-WebRequest {
    param([string]$Uri, $Headers, [switch]$SkipHttpErrorCheck)
    $github.Requests += [pscustomobject]@{ Uri = $Uri; Authorization = $Headers.Authorization }
    if ($Uri -like '*/contents/manifests/f/fstubner/nvx') {
        return [pscustomobject]@{ StatusCode = $github.Lookup; Content = '[]' }
    }
    if ($Uri -like '*/search/issues?q=*') {
        $body = @{ items = @($github.PullRequests) } | ConvertTo-Json -Depth 5
        return [pscustomobject]@{ StatusCode = $github.Search; Content = $body }
    }
    throw "unexpected request: $Uri"
}

# Runs the script once. Returns what it threw, or $null, and the lines komac logged.
function Submit([int]$Lookup = 200, [int]$Search = 200, $PullRequests = @(), [string]$Sha = $good,
                [string]$SubmitMode = 'pr', [int]$UpdateExit = 0, [switch]$StaleFile) {
    $github.Lookup = $Lookup
    $github.Search = $Search
    $github.PullRequests = $PullRequests
    $github.Requests = @()
    $env:STUB_SHA = $Sha
    $env:STUB_SUBMIT = $SubmitMode
    $env:STUB_UPDATE_EXIT = "$UpdateExit"
    $env:STUB_LOG = Join-Path $dir ([guid]::NewGuid().ToString() + '.log')
    New-Item -ItemType File -Path $env:STUB_LOG | Out-Null
    $work = Join-Path $dir ([guid]::NewGuid())
    if ($StaleFile) {
        New-Item -ItemType Directory -Path $work | Out-Null
        Set-Content -Path (Join-Path $work 'left-over.yaml') -Value 'x'
    }
    $thrown = $null
    try {
        & $submit -Komac $stub -PackageIdentifier 'fstubner.nvx' -PackageVersion '0.8.0' `
            -InstallerUrl $url -ExpectedSha256 $good -WorkDirectory $work -Token 'not-a-token' 6>$null
    } catch {
        $thrown = "$_"
    }
    [pscustomobject]@{ Thrown = $thrown; Calls = @(Get-Content $env:STUB_LOG); Work = $work; Requests = @($github.Requests) }
}

function Pr($title, $state = 'open') {
    [pscustomobject]@{ title = $title; state = $state; html_url = 'https://github.com/microsoft/winget-pkgs/pull/1' }
}

try {
    Write-Host "A package that is there, with no pull request yet:"
    $r = Submit
    $verbs = @($r.Calls | Where-Object { $_ -match '^(update|submit) ' } | ForEach-Object { ($_ -split ' ')[0] })
    Check "passes" ($null -eq $r.Thrown)
    Check "runs komac update, then komac submit, and nothing else" (($verbs -join ',') -eq 'update,submit')
    $update = $r.Calls | Where-Object { $_ -like 'update *' }
    Check "update is a dry run that writes into the work folder" (($update -like '*--dry-run*') -and ($update -like "*--output $($r.Work)"))
    Check "update is given the installer url" ($update -like "*--urls $url *")
    Check "submit is given the work folder" ($r.Calls -contains "submit $($r.Work) --yes")
    $wrote = $r.Calls | Where-Object { $_ -like 'wrote *' }
    $sent = $r.Calls | Where-Object { $_ -like 'sent *' }
    Check "the installer manifest submit read is the one update wrote" ($wrote -and ($wrote -replace '^wrote ', '') -eq ($sent -replace '^sent ', ''))
    $asked = $r.Requests | Where-Object { $_.Uri -like '*/contents/*' }
    Check "looks for the package under manifests/f/fstubner/nvx in winget-pkgs" ($asked.Uri -eq 'https://api.github.com/repos/microsoft/winget-pkgs/contents/manifests/f/fstubner/nvx')
    $searched = $r.Requests | Where-Object { $_.Uri -like '*/search/issues?q=*' }
    $query = [uri]::UnescapeDataString((($searched.Uri -split '\?q=')[1] -split '&')[0])
    Check "searches winget-pkgs pull requests by title for the package and version" (($query -match 'repo:microsoft/winget-pkgs') -and ($query -match '(^|\s)is:pr(\s|$)') -and ($query -match 'in:title') -and ($query -match 'fstubner\.nvx') -and ($query -match '0\.8\.0'))
    Check "sends the token on both requests" (@($r.Requests | Where-Object { $_.Authorization -eq 'Bearer not-a-token' }).Count -eq 2)

    Write-Host "A manifest that carries another digest is never submitted:"
    $r = Submit -Sha $other
    Check "refused" ($null -ne $r.Thrown)
    Check "names the digest it carries" ($r.Thrown -match $other)
    Check "komac submit is not run" (-not ($r.Calls | Where-Object { $_ -like 'submit *' }))

    Write-Host "A package that is not in winget-pkgs yet:"
    $r = Submit -Lookup 404
    Check "stops with the reason" ($r.Thrown -match 'not in microsoft/winget-pkgs yet')
    Check "says to submit by hand" ($r.Thrown -match 'by hand')
    Check "runs nothing" ($r.Calls.Count -eq 0)
    $r = Submit -Lookup 500
    Check "a lookup that failed is not read as the package missing" (($null -ne $r.Thrown) -and ($r.Thrown -notmatch 'not in microsoft/winget-pkgs yet') -and ($r.Thrown -match 'HTTP 500'))
    Check "runs nothing when the lookup failed" ($r.Calls.Count -eq 0)

    Write-Host "A pull request for this version that exists already:"
    foreach ($state in 'open', 'closed', 'merged') {
        $r = Submit -PullRequests @(Pr 'New version: fstubner.nvx version 0.8.0' $state)
        Check "$state, so nothing is sent" (($null -eq $r.Thrown) -and ($r.Calls.Count -eq 0))
    }

    Write-Host "A pull request that is not for this version or this package:"
    foreach ($title in 'New version: fstubner.nvx version 0.8.01', 'New version: fstubner.nvx version 0.8.0-beta',
                       'New version: fstubner.nvx.beta version 0.8.0', 'New version: fstubner.nvx version 0.7.0') {
        $r = Submit -PullRequests @(Pr $title)
        Check "'$title' does not block" (($null -eq $r.Thrown) -and ($r.Calls | Where-Object { $_ -like 'submit *' }))
    }
    $r = Submit -Search 403
    Check "a search that failed stops the job" (($null -ne $r.Thrown) -and ($r.Thrown -match 'HTTP 403') -and ($r.Calls.Count -eq 0))

    Write-Host "Komac failing, or sending nothing:"
    $r = Submit -UpdateExit 4
    Check "a dry run that fails stops the job before submit" (($r.Thrown -match 'komac update --dry-run exited 4') -and -not ($r.Calls | Where-Object { $_ -like 'submit *' }))
    $r = Submit -SubmitMode fail
    Check "a submit that fails stops the job" ($r.Thrown -match 'komac submit exited 3')
    $r = Submit -SubmitMode nothing
    Check "a submit that exits 0 without a pull request stops the job" ($r.Thrown -match 'nothing was submitted')

    Write-Host "Komac naming the pull request:"
    $r = Submit -SubmitMode number-only
    Check "by number alone is enough" ($null -eq $r.Thrown)
    $r = Submit -SubmitMode url-only
    Check "by url alone is enough" ($null -eq $r.Thrown)

    Write-Host "A work folder that is not empty:"
    $r = Submit -StaleFile
    Check "is refused before komac runs" (($r.Thrown -match 'not empty') -and ($r.Calls.Count -eq 0))
} finally {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Error "$failures check(s) failed"
    exit 1
}
Write-Host "submit-winget.ps1 checks passed."
