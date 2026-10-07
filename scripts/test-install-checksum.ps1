# Covers install.ps1's checksum gate, against files in a scratch directory.
#
# It shipped two defects at once: a mismatch left the unverified binary in
# ~/.nvx/bin, because the cleanup sat behind a Write-Error that throws under
# ErrorActionPreference Stop, and -InsecureSkipChecksum installed a binary whose
# checksum was present and wrong, because the catch read the mismatch as a
# missing file. Nothing here downloads anything.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $root 'install.ps1') -LibraryOnly

$failures = 0
$dir = Join-Path ([IO.Path]::GetTempPath()) ("nvx-install-checksum-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $dir | Out-Null
$dest = Join-Path $dir 'nvx.exe'
$download = "$dest.download"
$sums = "$dest.sha256"

function Check($label, $ok) {
    if ($ok) {
        Write-Host "  ok   $label"
    } else {
        Write-Host "  FAIL $label"
        $script:failures++
    }
}

# A previous, working install, which a failed upgrade must leave alone.
# $checksum is untyped on purpose: [string] would turn $null into ''.
function Reset([string]$payload, $checksum) {
    Remove-Item (Join-Path $dir '*') -Force -ErrorAction SilentlyContinue
    Set-Content -Path $dest -Value 'PREVIOUS' -NoNewline
    Set-Content -Path $download -Value $payload -NoNewline
    if ($checksum -eq '') {
        Set-Content -Path $sums -Value '' -NoNewline
    } elseif ($null -ne $checksum) {
        Set-Content -Path $sums -Value "$checksum  nvx.exe" -NoNewline
    }
}

function Run([switch]$AllowMissing) {
    try {
        Install-NvxDownloadedBinary -DownloadPath $download -ChecksumPath $sums `
            -Destination $dest -AllowMissingChecksum:$AllowMissing 3>$null 6>$null
        return $true
    } catch {
        return $false
    }
}

function Sha([string]$text) {
    $bytes = [Text.Encoding]::UTF8.GetBytes($text)
    $hash = [Security.Cryptography.SHA256]::Create().ComputeHash($bytes)
    return -join ($hash | ForEach-Object { $_.ToString('x2') })
}

$wrong = '0' * 64

# The payloads here are plain text, which has no Authenticode signature, so the
# signature gate is stood in for. It is covered against real files by
# test-install-signature.ps1. Every call is logged in $signatureChecks.
$signatureChecks = @()
function Assert-NvxSignedByPublisher {
    param([string]$Path)
    $script:signatureChecks += $Path
}

# A stand-in gh, so the provenance check never reaches the network or a real
# gh. It answers like gh 2.51 or newer unless $ghMode says otherwise:
# fail (attestation verify fails), old (no attestation command), nosigner (an
# attestation command without --signer-workflow, as in gh 2.49 and 2.50), noauth
# (not signed in). Every call is logged in $ghCalls.
$ghMode = 'pass'
$ghCalls = @()
function gh {
    $script:ghCalls += ($args -join ' ')
    $global:LASTEXITCODE = 0
    if ($args[0] -eq 'auth') {
        if ($script:ghMode -eq 'noauth') { $global:LASTEXITCODE = 1 }
        return
    }
    if ($script:ghMode -eq 'old') { $global:LASTEXITCODE = 1; return }
    if ($args -contains '--help') {
        'Usage: gh attestation verify [<file-path> | oci://<image-uri>] [--owner | --repo]'
        if ($script:ghMode -ne 'nosigner') { '      --signer-workflow string   Filter to workflows that match a path' }
        return
    }
    if ($script:ghMode -eq 'fail') {
        'stub gh: no attestation found'
        $global:LASTEXITCODE = 1
    }
}

# Like Run, but keeps what was written to the host in $ghOut, and sets $ghOk.
function RunOut {
    $script:ghCalls = @()
    $script:signatureChecks = @()
    try {
        $script:ghOut = (Install-NvxDownloadedBinary -DownloadPath $download -ChecksumPath $sums `
            -Destination $dest 6>&1 | Out-String)
        $script:ghOk = $true
    } catch {
        $script:ghOut = "$_"
        $script:ghOk = $false
    }
}

try {
    Write-Host "A matching checksum installs:"
    Reset 'GOOD' (Sha 'GOOD')
    Check "accepted" (Run)
    Check "destination is the new binary" ((Get-Content $dest -Raw) -eq 'GOOD')
    Check "no side files left" (-not (Test-Path $download) -and -not (Test-Path $sums))

    Write-Host "A mismatch is refused and changes nothing:"
    Reset 'TAMPERED' $wrong
    Check "refused" (-not (Run))
    Check "previous binary untouched" ((Get-Content $dest -Raw) -eq 'PREVIOUS')
    Check "download removed" (-not (Test-Path $download))

    Write-Host "A mismatch is refused even with the insecure skip:"
    Reset 'TAMPERED' $wrong
    Check "refused" (-not (Run -AllowMissing))
    Check "previous binary untouched" ((Get-Content $dest -Raw) -eq 'PREVIOUS')

    Write-Host "An empty checksum file is refused:"
    Reset 'GOOD' ''
    Check "refused" (-not (Run -AllowMissing))
    Check "previous binary untouched" ((Get-Content $dest -Raw) -eq 'PREVIOUS')

    Write-Host "A missing checksum file:"
    Reset 'UNVERIFIED' $null
    Check "refused by default" (-not (Run))
    Check "previous binary untouched" ((Get-Content $dest -Raw) -eq 'PREVIOUS')
    Reset 'UNVERIFIED' $null
    Check "accepted with the insecure skip" (Run -AllowMissing)
    Check "destination is the new binary" ((Get-Content $dest -Raw) -eq 'UNVERIFIED')

    Write-Host "Build provenance is checked with gh when gh can do it:"
    $ghMode = 'pass'
    Reset 'GOOD' (Sha 'GOOD')
    RunOut
    Check "accepted" $ghOk
    Check "gh checked the download against release.yml in fstubner/nvx" (@($ghCalls | Where-Object { $_ -eq "attestation verify $download --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml" }).Count -eq 1)
    Check "the signature gate was asked about the download" (@($signatureChecks | Where-Object { $_ -eq $download }).Count -eq 1)
    Check "says it was verified" ($ghOut -match 'Build provenance verified')
    Check "destination is the new binary" ((Get-Content $dest -Raw) -eq 'GOOD')

    Write-Host "A provenance failure is refused and changes nothing:"
    $ghMode = 'fail'
    Reset 'GOOD' (Sha 'GOOD')
    RunOut
    Check "refused" (-not $ghOk)
    Check "says provenance failed" ($ghOut -match 'Provenance verification failed')
    Check "shows gh's own message" ($ghOut -match 'no attestation found')
    Check "previous binary untouched" ((Get-Content $dest -Raw) -eq 'PREVIOUS')
    Check "download removed" (-not (Test-Path $download))

    Write-Host "Without gh the check is skipped, and says how to run it by hand:"
    $ghMode = 'pass'
    Reset 'GOOD' (Sha 'GOOD')
    # The real PATH cannot be used for this, because a machine running the
    # test may well have gh installed.
    $realPath = $env:PATH
    $emptyDir = Join-Path $dir 'empty'
    New-Item -ItemType Directory -Path $emptyDir | Out-Null
    $stub = (Get-Item function:gh).ScriptBlock
    Remove-Item function:gh
    $env:PATH = $emptyDir
    try { RunOut } finally { $env:PATH = $realPath; Set-Item function:gh $stub }
    Check "accepted" $ghOk
    Check "one skip line" (@($ghOut -split "`n" | Where-Object { $_ -match 'Provenance check skipped' }).Count -eq 1)
    Check "says nothing was shown about where the download came from" (($ghOut -replace '\s+', ' ') -match 'nothing has shown this download came from the release workflow')
    Check "gives the command, with the signer workflow" (($ghOut -replace '\s+', ' ') -match 'gh attestation verify .* --repo fstubner/nvx --signer-workflow fstubner/nvx/.github/workflows/release.yml')
    Check "destination is the new binary" ((Get-Content $dest -Raw) -eq 'GOOD')

    Write-Host "A gh that is too old, or not signed in, skips rather than fails the install:"
    $ghMode = 'old'
    Reset 'GOOD' (Sha 'GOOD')
    RunOut
    Check "old gh accepted" $ghOk
    Check "old gh says it skipped" ($ghOut -match 'Provenance check skipped')
    # gh 2.49 and 2.50 have the attestation command and not --signer-workflow.
    # Asking them for it would fail the install for a reason that is not a bad
    # download.
    $ghMode = 'nosigner'
    Reset 'GOOD' (Sha 'GOOD')
    RunOut
    Check "gh without --signer-workflow accepted" $ghOk
    Check "gh without --signer-workflow says it skipped" ($ghOut -match 'Provenance check skipped')
    Check "gh without --signer-workflow was not asked to verify" (@($ghCalls | Where-Object { $_ -like 'attestation verify*' -and $_ -notlike '*--help' }).Count -eq 0)
    $ghMode = 'noauth'
    Reset 'GOOD' (Sha 'GOOD')
    RunOut
    Check "signed-out gh accepted" $ghOk
    Check "signed-out gh says it skipped" ($ghOut -match 'Provenance check skipped')
    Check "signed-out gh was not asked to verify" (@($ghCalls | Where-Object { $_ -like 'attestation verify*' -and $_ -notlike '*--help' }).Count -eq 0)

    Write-Host "A checksum mismatch still fails before the signature or gh is asked:"
    $ghMode = 'pass'
    Reset 'TAMPERED' $wrong
    RunOut
    Check "refused" (-not $ghOk)
    Check "gh was not asked at all" ($ghCalls.Count -eq 0)
    Check "the signature gate was not asked" ($signatureChecks.Count -eq 0)
} finally {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Error "$failures installer checksum check(s) failed"
    exit 1
}
# The stub gh above leaves $LASTEXITCODE at 1 after the failing case, and a runner
# that calls this script with `pwsh -command` exits with that value.
$global:LASTEXITCODE = 0
Write-Host "Installer checksum checks passed."
