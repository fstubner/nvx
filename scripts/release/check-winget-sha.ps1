# Fails unless every installer in the manifests Komac wrote carries the digest that
# was verified for nvx.exe.
#
#   check-winget-sha.ps1 -ManifestDirectory <dir Komac wrote to> -ExpectedSha256 <digest>
#
# publish.yml's Winget job verifies nvx.exe with verified_sha (scripts/release/lib.sh),
# which checks the sidecar and release.yml's build provenance and prints the digest.
# Komac then downloads nvx.exe for itself and writes the hash of what it got into
# InstallerSha256. Without this, a file replaced on the release page after
# verified_sha ran would reach winget-pkgs under its own hash, and winget clients
# check a download against that hash, so they would accept it.
#
# It throws and does not exit. The step that calls it carries on to the next command
# after a script's `exit 1`, and stops at a throw.
param(
    [Parameter(Mandatory)][string]$ManifestDirectory,
    [Parameter(Mandatory)][string]$ExpectedSha256
)
$ErrorActionPreference = 'Stop'

if ($ExpectedSha256 -notmatch '^[0-9a-fA-F]{64}$') {
    throw "The verified digest is not a SHA-256: '$ExpectedSha256'."
}

# Komac writes manifests/<letter>/<publisher>/<package>/<version>/ under the output
# directory, so look for the file instead of building the path.
$installers = @(Get-ChildItem -Path $ManifestDirectory -Recurse -Filter '*.installer.yaml' -File -ErrorAction SilentlyContinue)
if ($installers.Count -ne 1) {
    throw "Expected one installer manifest under $ManifestDirectory and found $($installers.Count)."
}

$pattern = '^\s*(?:-\s*)?InstallerSha256:\s*["'']?([0-9A-Fa-f]{64})["'']?\s*$'
$found = @(Select-String -Path $installers[0].FullName -Pattern $pattern | ForEach-Object { $_.Matches[0].Groups[1].Value })
if ($found.Count -eq 0) {
    throw "Found no InstallerSha256 in $($installers[0].Name)."
}
$wrong = @($found | Where-Object { $_ -ine $ExpectedSha256 })
if ($wrong.Count -gt 0) {
    throw "Komac's manifest carries $($wrong -join ', '), and the verified digest is $ExpectedSha256."
}
Write-Host "Komac's manifest carries the verified digest."
