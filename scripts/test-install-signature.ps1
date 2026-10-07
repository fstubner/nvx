# Covers install.ps1's Authenticode gate, against real files and real signatures.
#
# The checksum and the attestation check both leave a replaced release able to
# pass: the checksum sits on the same page as the file, and the attestation
# check is skipped on a machine without a signed-in gh. The signature on nvx.exe
# is the check that needs nothing installed, so what it refuses has to be
# shown. Nothing here downloads anything and nothing signs anything: the
# fixtures are copies of binaries already on the machine.
#
#   unsigned.exe     cmd.exe with one byte changed. Windows catalogs the real
#                    cmd.exe, so the changed copy has no signature left.
#   tampered.exe     this shell's own executable with one byte changed. Under
#                    pwsh it is signed inside the file, so the hash no longer
#                    matches. Under Windows PowerShell it is catalogued, so the
#                    copy is simply unsigned.
#   text.exe         not an executable at all.
#   othersigned.exe  an unchanged copy of cmd.exe: a valid signature, from
#                    Microsoft.
#
# The real publisher cannot be shown accepting a signature here, because nobody
# but the release workflow can sign as it. The accepting half is a control
# instead: the same unchanged cmd.exe is accepted when it is asked for under
# Microsoft's name. scripts/test-install-iex.ps1 installs the real latest
# release, which covers the real publisher.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $root 'install.ps1') -LibraryOnly

$failures = 0
$dir = Join-Path ([IO.Path]::GetTempPath()) ("nvx-install-signature-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $dir | Out-Null

function Check($label, $ok) {
    if ($ok) {
        Write-Host "  ok   $label"
    } else {
        Write-Host "  FAIL $label"
        $script:failures++
    }
}

# What the function says when it throws, or $null when it does not.
function Refusal([scriptblock]$Action) {
    try { & $Action 6>$null; return $null } catch { return "$_" }
}

function New-TestCertificate([string]$Subject) {
    $key = [System.Security.Cryptography.RSA]::Create(2048)
    $request = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new(
        $Subject, $key, [System.Security.Cryptography.HashAlgorithmName]::SHA256,
        [System.Security.Cryptography.RSASignaturePadding]::Pkcs1)
    return $request.CreateSelfSigned([DateTimeOffset]::UtcNow.AddDays(-1), [DateTimeOffset]::UtcNow.AddDays(1))
}

function Copy-WithChangedByte([string]$From, [string]$To) {
    $bytes = [IO.File]::ReadAllBytes($From)
    $middle = [int]($bytes.Length / 2)
    $bytes[$middle] = $bytes[$middle] -bxor 0xFF
    [IO.File]::WriteAllBytes($To, $bytes)
}

try {
    $pubCn = 'Open Source Developer Felix Stubner'
    $pubO = 'Open Source Developer'
    function Accepts([string]$Subject) {
        Test-NvxSignerName -Certificate (New-TestCertificate $Subject) -CommonName $pubCn -Organization $pubO
    }

    Write-Host "The signer's name is matched part by part:"
    # The subject Windows reports for the 0.7.0 nvx.exe.
    Check "the 0.7.0 subject is accepted" (Accepts 'CN=Open Source Developer Felix Stubner, O=Open Source Developer, L=Cork, S=Munster, C=IE')
    Check "a renewal that moved is accepted" (Accepts 'CN=Open Source Developer Felix Stubner, O=Open Source Developer, L=Dublin, S=Leinster, C=IE')
    Check "another order is accepted" (Accepts 'O=Open Source Developer, CN=Open Source Developer Felix Stubner')
    Check "another person in the same programme is refused" (-not (Accepts 'CN=Open Source Developer Someone Else, O=Open Source Developer'))
    Check "the same name under another organization is refused" (-not (Accepts 'CN=Open Source Developer Felix Stubner, O=Someone Else Ltd'))
    Check "a missing organization is refused" (-not (Accepts 'CN=Open Source Developer Felix Stubner'))
    Check "a missing common name is refused" (-not (Accepts 'O=Open Source Developer'))
    Check "a longer common name is refused" (-not (Accepts 'CN=Open Source Developer Felix Stubner Jr, O=Open Source Developer'))
    Check "another letter case is refused" (-not (Accepts 'CN=open source developer felix stubner, O=Open Source Developer'))
    # The two parts written inside one quoted value, which a search of the
    # one-line subject for ", CN=..." and ", O=..." finds.
    Check "both parts inside a quoted organization are refused" (-not (Accepts 'CN=Evil, O="x, CN=Open Source Developer Felix Stubner, O=Open Source Developer, y"'))
    # Newlines inside a quoted value make plain-looking lines of their own.
    $nl = "`n"
    Check "both parts on lines of a quoted organization are refused" (-not (Accepts ('CN=Evil, O="x' + $nl + 'CN=Open Source Developer Felix Stubner' + $nl + 'O=Open Source Developer' + $nl + 'y"')))
    Check "both parts joined into one name part are refused" (-not (Accepts 'CN=Open Source Developer Felix Stubner+O=Open Source Developer'))

    $cmd = Join-Path $env:SystemRoot 'System32\cmd.exe'
    $shellExe = (Get-Process -Id $PID).Path
    Copy-Item $cmd (Join-Path $dir 'othersigned.exe')
    Copy-WithChangedByte $cmd (Join-Path $dir 'unsigned.exe')
    Copy-WithChangedByte $shellExe (Join-Path $dir 'tampered.exe')
    Set-Content -Path (Join-Path $dir 'text.exe') -Value 'not an executable' -NoNewline
    $fixtures = @('othersigned.exe', 'unsigned.exe', 'tampered.exe', 'text.exe') | ForEach-Object { Join-Path $dir $_ }
    foreach ($f in $fixtures) {
        $s = Get-AuthenticodeSignature -FilePath $f
        Write-Host ("  fixture {0}: status {1}" -f (Split-Path $f -Leaf), $s.Status)
    }

    Write-Host "A file that is not signed by the publisher is refused:"
    $unsigned = Refusal { Assert-NvxSignedByPublisher -Path (Join-Path $dir 'unsigned.exe') }
    Check "unsigned: refused" ($null -ne $unsigned)
    Check "unsigned: says there is no valid signature" ($unsigned -match 'no valid Authenticode signature \(status NotSigned\)')
    $tampered = Refusal { Assert-NvxSignedByPublisher -Path (Join-Path $dir 'tampered.exe') }
    Check "changed after signing: refused" ($null -ne $tampered)
    Check "changed after signing: says there is no valid signature" ($tampered -match 'no valid Authenticode signature')
    $text = Refusal { Assert-NvxSignedByPublisher -Path (Join-Path $dir 'text.exe') }
    Check "not an executable: refused" ($null -ne $text)
    $other = Refusal { Assert-NvxSignedByPublisher -Path (Join-Path $dir 'othersigned.exe') }
    Check "valid, from another publisher: refused" ($null -ne $other)
    Check "valid, from another publisher: names who signed it" ($other -match 'signed by .*Microsoft')

    Write-Host "A valid signature from the named publisher is accepted:"
    $control = Refusal { Assert-NvxSignedByPublisher -Path (Join-Path $dir 'othersigned.exe') -CommonName 'Microsoft Windows' -Organization 'Microsoft Corporation' }
    Check "accepted under the name its signature carries" ($null -eq $control)
    if ($control) { Write-Host "       $control" }
    # The right name does not rescue a file whose signature no longer holds. Under
    # pwsh the changed copy still names Microsoft Corporation as its signer.
    $changedUnderItsName = Refusal { Assert-NvxSignedByPublisher -Path (Join-Path $dir 'tampered.exe') -CommonName 'Microsoft Corporation' -Organization 'Microsoft Corporation' }
    Check "changed after signing: refused even under the name it carries" ($null -ne $changedUnderItsName)

    # Through the install step, with the checksum right, so the signature is the
    # only thing standing in the way. gh is stood in for: it is covered by
    # test-install-checksum.ps1, and a real one would reach the network.
    $installed = Join-Path $dir 'nvx.exe'
    $download = "$installed.download"
    $sums = "$installed.sha256"
    $provenanceChecks = 0
    function Test-NvxProvenance { param([string]$Path, [string]$InstalledPath) $script:provenanceChecks++ }

    function Install-Fixture([string]$Fixture, [switch]$NoChecksumFile) {
        Remove-Item (Join-Path $dir 'nvx.*') -Force -ErrorAction SilentlyContinue
        Set-Content -Path $installed -Value 'PREVIOUS' -NoNewline
        Copy-Item (Join-Path $dir $Fixture) $download
        if (-not $NoChecksumFile) {
            Set-Content -Path $sums -Value ((Get-FileHash $download -Algorithm SHA256).Hash + '  nvx.exe') -NoNewline
        }
        $script:provenanceChecks = 0
        try {
            Install-NvxDownloadedBinary -DownloadPath $download -ChecksumPath $sums -Destination $installed `
                -AllowMissingChecksum:$NoChecksumFile 3>$null 6>$null
            return $true
        } catch {
            return $false
        }
    }

    Write-Host "The install step refuses it and changes nothing:"
    foreach ($case in @(@('unsigned.exe', 'unsigned'), @('tampered.exe', 'changed after signing'), @('othersigned.exe', 'signed by another publisher'))) {
        Check "$($case[1]): refused" (-not (Install-Fixture $case[0]))
        Check "$($case[1]): previous binary untouched" ((Get-Content $installed -Raw) -eq 'PREVIOUS')
        Check "$($case[1]): download removed" (-not (Test-Path $download))
        Check "$($case[1]): gh was not asked" ($provenanceChecks -eq 0)
    }
    Check "unsigned with no checksum file and the insecure skip: still refused" (-not (Install-Fixture 'unsigned.exe' -NoChecksumFile))
    Check "unsigned with no checksum file and the insecure skip: previous binary untouched" ((Get-Content $installed -Raw) -eq 'PREVIOUS')

    Write-Host "The install step installs a file the gate accepts:"
    # The real gate, asked for Microsoft's name instead of the publisher's.
    $realGate = (Get-Item function:Assert-NvxSignedByPublisher).ScriptBlock
    function Assert-NvxSignedByPublisher {
        param([string]$Path)
        & $realGate -Path $Path -CommonName 'Microsoft Windows' -Organization 'Microsoft Corporation'
    }
    Check "accepted" (Install-Fixture 'othersigned.exe')
    Check "destination is the new file" ((Get-FileHash $installed -Algorithm SHA256).Hash -eq (Get-FileHash $cmd -Algorithm SHA256).Hash)
    Check "gh was asked once, after the signature" ($provenanceChecks -eq 1)
    Check "an unsigned file is still refused under that name" (-not (Install-Fixture 'unsigned.exe'))
} finally {
    Remove-Item $dir -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures -gt 0) {
    Write-Error "$failures installer signature check(s) failed"
    exit 1
}
Write-Host "Installer signature checks passed."
