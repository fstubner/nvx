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
} finally {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Error "$failures installer checksum check(s) failed"
    exit 1
}
Write-Host "Installer checksum checks passed."
