# Regression tests for check-winget-sha.ps1.
#
# It runs only during a release, in the Winget job, so a check that passed
# everything would not surface until a binary had already reached winget-pkgs.
# The manifest below has the shape Komac 2.16.0 wrote for fstubner.netscli
# (read from microsoft/winget-pkgs on 2026-10-07), with this test's own digests.
#
# Run: pwsh scripts/release/check-winget-sha_test.ps1
$ErrorActionPreference = 'Stop'
$check = Join-Path $PSScriptRoot 'check-winget-sha.ps1'

$failures = 0
$dir = Join-Path ([IO.Path]::GetTempPath()) ("nvx-winget-sha-" + [guid]::NewGuid())
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

# Writes a manifest folder shaped as Komac writes it and returns its root.
function New-Manifests([string[]]$Lines) {
    $root = Join-Path $dir ([guid]::NewGuid())
    $leaf = Join-Path $root 'manifests\f\fstubner\nvx\0.7.1'
    New-Item -ItemType Directory -Path $leaf -Force | Out-Null
    Set-Content -Path (Join-Path $leaf 'fstubner.nvx.yaml') -Value 'ManifestType: version'
    Set-Content -Path (Join-Path $leaf 'fstubner.nvx.installer.yaml') -Value $Lines
    return $root
}

# One installer entry for each value given.
function Installer([string[]]$Digests) {
    $lines = @(
        'PackageIdentifier: fstubner.nvx',
        'PackageVersion: 0.7.1',
        'InstallerType: portable',
        'Commands:',
        '- nvx',
        'Installers:')
    $arch = @('x64', 'arm64')
    for ($i = 0; $i -lt $Digests.Count; $i++) {
        $lines += "- Architecture: $($arch[$i])"
        $lines += "  InstallerUrl: https://github.com/fstubner/nvx/releases/download/v0.7.1/nvx-$($arch[$i]).exe"
        $lines += "  InstallerSha256: $($Digests[$i])"
    }
    return $lines + @('ManifestType: installer', 'ManifestVersion: 1.12.0')
}

# What the check says when it throws, or $null when it passes.
function Refusal([string]$Root, [string]$Expected) {
    try { & $check -ManifestDirectory $Root -ExpectedSha256 $Expected 6>$null; return $null } catch { return "$_" }
}

try {
    Write-Host "A manifest that carries the verified digest passes:"
    Check "upper case in the manifest, lower case verified" ($null -eq (Refusal (New-Manifests (Installer $good.ToUpper())) $good))
    Check "lower case in the manifest, upper case verified" ($null -eq (Refusal (New-Manifests (Installer $good)) $good.ToUpper()))
    Check "a quoted value" ($null -eq (Refusal (New-Manifests (Installer "'$($good.ToUpper())'")) $good))

    Write-Host "A manifest that carries any other digest is refused:"
    $wrong = Refusal (New-Manifests (Installer $other.ToUpper())) $good
    Check "refused" ($null -ne $wrong)
    Check "names the digest it carries" ($wrong -match $other)
    Check "names the verified digest" ($wrong -match $good)
    Check "one wrong entry among two is refused" ($null -ne (Refusal (New-Manifests (Installer @($good.ToUpper(), $other.ToUpper()))) $good))
    Check "two right entries pass" ($null -eq (Refusal (New-Manifests (Installer @($good.ToUpper(), $good.ToUpper()))) $good))

    Write-Host "A manifest the check cannot read is refused:"
    Check "no digest in it" ($null -ne (Refusal (New-Manifests (Installer 'pending')) $good))
    Check "a digest of the wrong length" ($null -ne (Refusal (New-Manifests (Installer $good.Substring(0, 63))) $good))
    Check "a folder with no installer manifest" ($null -ne (Refusal (New-Item -ItemType Directory -Path (Join-Path $dir 'empty')).FullName $good))
    Check "a folder that does not exist" ($null -ne (Refusal (Join-Path $dir 'missing') $good))
    $both = New-Manifests (Installer $good)
    Copy-Item (Join-Path $both 'manifests\f\fstubner\nvx\0.7.1\fstubner.nvx.installer.yaml') (Join-Path $both 'second.installer.yaml')
    Check "two installer manifests" ($null -ne (Refusal $both $good))

    Write-Host "A verified digest that is not a SHA-256 is refused:"
    Check "a short one" ($null -ne (Refusal (New-Manifests (Installer $good)) 'abc'))
} finally {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Error "$failures check(s) failed"
    exit 1
}
Write-Host "check-winget-sha.ps1 checks passed."
