param(
    [switch]$InsecureSkipChecksum,
    [switch]$UseLocalBinary,
    # Define the functions and stop, so scripts/test-install-path.ps1 can
    # exercise the PATH logic below without installing anything. The PATH write
    # is the one line in this installer that has already shipped a defect --
    # it converted every %VAR% entry on the machine into today's expansion of
    # it -- and it had no test because there was no way to reach it.
    [switch]$LibraryOnly
)

# Installer script for nvx (Node Version X-platform)

# The body is a script block, run with & so it has a scope of its own. The
# documented install is `irm ... | iex`, and Invoke-Expression runs a script
# in the caller's session. Without the block, $ErrorActionPreference = 'Stop'
# and every variable and function below stayed in the PowerShell window the
# line was pasted into. -LibraryOnly dot-sources the block instead, because
# scripts/test-install-*.ps1 need its functions in their own scope.
$nvxInstaller = {

$ErrorActionPreference = 'Stop'

# Define installation paths
$nvxHome = Join-Path $HOME ".nvx"
$binDir = Join-Path $nvxHome "bin"

# Puts $BinDir at the front of the User PATH, preserving both what is stored
# and how it is stored.
#
# Read raw, not through [Environment]::GetEnvironmentVariable: that one EXPANDS
# a REG_EXPAND_SZ value, so an entry written as %USERPROFILE%\bin comes back as
# the expanded path and writing it back would bake today's expansion into the
# registry for ever. And write with the type it already had, because Windows
# ships this value as REG_EXPAND_SZ and [Environment]::SetEnvironmentVariable
# always writes REG_SZ -- this installer used to convert it on every machine it
# ran on, after which no %VAR% entry resolved again. nvx had the same bug in
# `doctor --fix`.
#
# Returns $true when it changed something.
function Set-NvxUserPath {
    param(
        [Parameter(Mandatory)][string]$BinDir,
        [string]$KeyPath = 'HKCU:\Environment'
    )

    $item = Get-Item -Path $KeyPath
    $userPath = $item.GetValue(
        'Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)

    $kind = 'ExpandString'
    try {
        if ($item.GetValueKind('Path') -eq [Microsoft.Win32.RegistryValueKind]::String) {
            $kind = 'String'
        }
    } catch {
        # No Path value yet on a fresh profile: ExpandString is what Windows
        # would have created.
    }

    foreach ($part in ($userPath -split ';')) {
        if ($part.Trim().TrimEnd('\') -eq $BinDir.TrimEnd('\')) {
            return $false
        }
    }

    Set-ItemProperty -Path $KeyPath -Name 'Path' -Value "$BinDir;$userPath" -Type $kind
    return $true
}

# Tells running programs the environment moved. [Environment]::SetEnvironment-
# Variable did this as part of its own work; a plain registry write does not,
# and without it Explorer keeps handing its stale copy to everything launched
# from it until the next sign-in. Best effort: the value is already written by
# the time this runs, and a machine that will not compile the P/Invoke should
# not fail an install over a notification.
function Send-NvxEnvironmentChange {
    try {
        if (-not ('Nvx.Native' -as [type])) {
            Add-Type -Namespace 'Nvx' -Name 'Native' -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam,
    string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
        }
        $result = [UIntPtr]::Zero
        # HWND_BROADCAST, WM_SETTINGCHANGE, SMTO_ABORTIFHUNG, 5s.
        [void][Nvx.Native]::SendMessageTimeout(
            [IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
    } catch {
        Write-Host "  (could not notify running programs; new terminals will still pick this up)"
    }
}

# Does this execution policy stop PowerShell from loading a profile?
#
# Takes the EFFECTIVE policy, not the CurrentUser one. The check used to read
# `Get-ExecutionPolicy -Scope CurrentUser`, which is Undefined on a machine whose
# policy is set at any other scope -- measured here on 2026-09-11: CurrentUser
# Undefined, effective RemoteSigned, scripts running perfectly well. Reading the
# scope rather than the answer meant offering to change a setting that was
# already fine.
#
# AllSigned is included because an unsigned profile does not load under it
# either, and CurrentUser outranks LocalMachine in policy precedence, so the same
# change is the same fix. Undefined at the effective level means no scope has set
# one, which is Restricted on Windows client editions.
function Test-NvxProfileBlockedByPolicy {
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Policy)
    return $Policy -in @('Restricted', 'AllSigned', 'Undefined')
}

# Only an explicit yes is a yes. Empty -- someone pressing Return at a [y/N]
# prompt -- is no, which is what makes the prompt's default safe.
function Test-NvxAffirmative {
    param([AllowEmptyString()][string]$Answer)
    return $Answer -match '^\s*(y|yes)\s*$'
}

# Does the signer's subject name the expected publisher? Both the common name
# and the organization must match exactly, and each must appear once.
#
# Decode with UseNewLines puts one name part on each line and keeps the quotes
# round a value that holds a comma. A part smuggled inside such a value
# therefore cannot equal a plain "CN=..." or "O=..." line, which a search
# through the one-line Subject string would have found. A part joined to
# another with a + shares their line, and that does not match either.
# A value holding a line break splits into lines of its own, some of which look
# like name parts, so any subject with a line break in it is refused first.
function Test-NvxSignerName {
    param(
        [Parameter(Mandatory)]$Certificate,
        [Parameter(Mandatory)][string]$CommonName,
        [Parameter(Mandatory)][string]$Organization
    )
    if ($Certificate.Subject -match '[\r\n]') { return $false }
    $flag = [System.Security.Cryptography.X509Certificates.X500DistinguishedNameFlags]::UseNewLines
    $parts = @($Certificate.SubjectName.Decode($flag) -split '\r?\n')
    return (@($parts -ceq "CN=$CommonName").Count -eq 1) -and (@($parts -ceq "O=$Organization").Count -eq 1)
}

# Throws unless the file carries a valid Authenticode signature from the nvx
# publisher. The release workflow signs nvx.exe with a Certum certificate. The
# checksum comes from the same release page as the file, and the attestation
# check needs gh, which many machines lack. This check needs nothing installed,
# and a replaced release cannot pass it without the publisher's key.
#
# The publisher is pinned by the certificate's CN and O and not by thumbprint,
# so a renewed certificate for the same publisher keeps working. Read from the
# 0.7.0 nvx.exe on 2026-10-07: "CN=Open Source Developer Felix Stubner, O=Open
# Source Developer, L=Cork, S=Munster, C=IE", issued by Certum Code Signing 2021
# CA. This script is served from main and checks the LATEST release, so if
# signing ever moves to another name, change the two values below in a commit
# that is live before that release is published. Until then the new release is
# refused.
#
# An unsigned file is refused. Every release from 0.7.0 on is signed, because
# release.yml cannot publish without its signing job, and the installer fetches
# only the latest release. Skipping an unsigned one instead would let a replaced
# release pass by dropping the signature. The parameters exist so a test can ask
# the same question about a file signed by someone else. The install never
# passes them.
function Assert-NvxSignedByPublisher {
    param(
        [Parameter(Mandatory)][string]$Path,
        [string]$CommonName = 'Open Source Developer Felix Stubner',
        [string]$Organization = 'Open Source Developer'
    )
    $signature = Get-AuthenticodeSignature -FilePath $Path
    if ($signature.Status -ne 'Valid') {
        throw ("nvx.exe has no valid Authenticode signature (status $($signature.Status)). " +
            "Releases from 0.7.0 on are signed, so this is not a file the release workflow built. " +
            "If the status is NotTrusted, this machine may not trust Certum's certificates.")
    }
    $subject = $signature.SignerCertificate.Subject
    if (-not (Test-NvxSignerName -Certificate $signature.SignerCertificate -CommonName $CommonName -Organization $Organization)) {
        throw "nvx.exe is signed by '$subject', which is not the nvx publisher (CN=$CommonName, O=$Organization)."
    }
    Write-Host "Authenticode signature verified: $subject"
}

# Second check on a downloaded binary, when gh can make it. The .sha256 file
# comes from the same release as the binary, so it cannot tell a replaced
# release from a real one. The build attestation is signed by release.yml and
# checked against GitHub. Only an attestation made by release.yml counts, as in
# scripts/release/lib.sh: without --signer-workflow, one from any workflow in
# this repository would pass.
#
# gh has to be signed in and new enough. The attestation command arrived in gh
# 2.49 and --signer-workflow in 2.51, so the help text is asked for the flag. A
# gh that cannot make the check skips it. A gh that makes it and reports a
# failure, or cannot reach GitHub, throws.
function Test-NvxProvenance {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$InstalledPath
    )
    $signerWorkflow = 'fstubner/nvx/.github/workflows/release.yml'
    $hint = "gh attestation verify $InstalledPath --repo fstubner/nvx --signer-workflow $signerWorkflow"
    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
        Write-Host "Provenance check skipped: gh is not installed, so nothing has shown this download came from the release workflow."
        Write-Host "  The checksum only shows it matches its release page. To check by hand, install gh 2.51 or newer,"
        Write-Host "  sign in with 'gh auth login', then run: $hint"
        return
    }
    # gh writes progress to stderr, which Stop turns into a terminating error.
    $ErrorActionPreference = 'Continue'
    $help = & gh attestation verify --help 2>&1 | Out-String
    $canVerify = ($LASTEXITCODE -eq 0) -and ($help -match '--signer-workflow')
    if ($canVerify) {
        & gh auth status *> $null
        $canVerify = ($LASTEXITCODE -eq 0)
    }
    if (-not $canVerify) {
        Write-Host "Provenance check skipped: gh is older than 2.51 or not signed in, so nothing has shown this download came from the release workflow."
        Write-Host "  The checksum only shows it matches its release page. To check by hand, update gh,"
        Write-Host "  sign in with 'gh auth login', then run: $hint"
        return
    }
    Write-Host "Verifying build provenance..."
    $output = & gh attestation verify $Path --repo fstubner/nvx --signer-workflow $signerWorkflow 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) {
        throw "Provenance verification failed: $($output.Trim())"
    }
    Write-Host "Build provenance verified."
}

# Writes the shims into the bin directory. They are what put nvx in front of npm,
# node and the rest. Nothing else wrote them until a PowerShell profile first ran
# `nvx env`, so a first terminal that never loads one (cmd, Git Bash, a declined
# execution-policy change, an agent started from a GUI app) had nvx on PATH and
# nothing to intercept with.
#
# Run from the Windows directory, because `init-shims` also writes shims for the
# project it is run in, and an install should not do that for whichever folder
# the installer happened to be started from.
function Initialize-NvxShims {
    param([Parameter(Mandatory)][string]$NvxExe)
    # nvx writes its progress to stderr, which Stop turns into a terminating error.
    $ErrorActionPreference = 'Continue'
    Push-Location $env:SystemRoot
    try {
        & $NvxExe init-shims
        if ($LASTEXITCODE -ne 0) {
            throw "nvx init-shims exited $LASTEXITCODE."
        }
    } finally {
        Pop-Location
    }
}

# Verifies a downloaded nvx.exe against its published SHA-256, its Authenticode
# signature and, when gh can, its build provenance, and moves it into place, or
# throws and leaves Destination as it was.
#
# The download goes to a side path so a failed check never leaves an unverified
# binary where the shims will run it. It used to be written straight to
# Destination, and the cleanup after a mismatch sat behind Write-Error, which
# throws under ErrorActionPreference Stop, so the file stayed. The catch around
# it then read the mismatch as a missing checksum file, and with
# -InsecureSkipChecksum went on to install it.
#
# AllowMissingChecksum covers a release with no checksum file and nothing else.
# A checksum that is present and does not match always fails.
function Install-NvxDownloadedBinary {
    param(
        [Parameter(Mandatory)][string]$DownloadPath,
        [Parameter(Mandatory)][string]$ChecksumPath,
        [Parameter(Mandatory)][string]$Destination,
        [switch]$AllowMissingChecksum
    )
    try {
        if (Test-Path $ChecksumPath) {
            Write-Host "Verifying checksum..."
            $expected = ((Get-Content $ChecksumPath -Raw).Trim() -split '\s+')[0].ToUpper()
            $actual = (Get-FileHash $DownloadPath -Algorithm SHA256).Hash.ToUpper()
            if ($expected -ne $actual) {
                throw "Checksum verification failed: expected $expected, got $actual."
            }
            Write-Host "Checksum verified successfully."
        } elseif ($AllowMissingChecksum) {
            Write-Warning "Checksum file not available. Skipping verification because insecure skip was explicitly requested."
        } else {
            throw "Checksum file not available. Refusing to install without verification."
        }
        # Not skippable. -AllowMissingChecksum is about the checksum file only.
        Assert-NvxSignedByPublisher -Path $DownloadPath
        Test-NvxProvenance -Path $DownloadPath -InstalledPath $Destination
        Move-Item -Path $DownloadPath -Destination $Destination -Force
    } finally {
        Remove-Item $DownloadPath, $ChecksumPath -Force -ErrorAction SilentlyContinue
    }
}

if ($LibraryOnly) { return }

Write-Host "Setting up nvx directories..."

# Create nvx directories if they do not exist
if (-not (Test-Path $binDir)) {
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
}


# 1. Download and verify the binary, before anything else is changed.
#
# First so a failed download leaves no trace: it used to run after the PATH and
# profile edits, so a network error left a profile that ran `nvx env` in every
# new PowerShell window and failed there.
#
# $PSScriptRoot is empty under `irm ... | iex`, the documented install, and
# Join-Path refuses an empty path. The line ran unconditionally, so every
# one-line install stopped here with "Cannot bind argument to parameter 'Path'
# because it is an empty string", after editing PATH and the profile. Only a
# local-binary install, run from a file, has a script directory to look in.
$useLocalBinary = $UseLocalBinary -or $env:NVX_USE_LOCAL_BINARY -eq "1"
$localBinary = if ($PSScriptRoot) { Join-Path $PSScriptRoot "nvx.exe" } else { $null }
if ($useLocalBinary -and $localBinary -and (Test-Path $localBinary)) {
    Write-Host "Copying compiled nvx.exe to bin directory..."
    Copy-Item -Path $localBinary -Destination (Join-Path $binDir "nvx.exe") -Force
} else {
    $downloadUrl = "https://github.com/fstubner/nvx/releases/latest/download/nvx.exe"

    $checksumUrl = "$downloadUrl.sha256"
    Write-Host "Downloading nvx.exe from $downloadUrl..."
    # Process-wide, so a scope does not contain it. Put back in the finally
    # below, or the session that ran `iex` is left TLS 1.2 only.
    $previousProtocol = [System.Net.ServicePointManager]::SecurityProtocol
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12
    $binPath = Join-Path $binDir "nvx.exe"
    $downloadPath = "$binPath.download"
    $checksumPath = "$binPath.sha256"
    try {
        Invoke-WebRequest -Uri $downloadUrl -OutFile $downloadPath -UseBasicParsing
        try {
            Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumPath -UseBasicParsing
        } catch {
            # Absent from here on; Install-NvxDownloadedBinary decides whether
            # that is allowed.
            Remove-Item $checksumPath -Force -ErrorAction SilentlyContinue
        }
        Install-NvxDownloadedBinary -DownloadPath $downloadPath -ChecksumPath $checksumPath `
            -Destination $binPath `
            -AllowMissingChecksum:($InsecureSkipChecksum -or $env:NVX_INSECURE_SKIP_CHECKSUM -eq "1")
    } catch {
        Remove-Item $downloadPath, $checksumPath -Force -ErrorAction SilentlyContinue
        # throw, not exit. The documented install is `irm ... | iex`, and exit
        # inside Invoke-Expression ends the PowerShell session the user typed
        # it into: the window closed before the error could be read. Measured
        # 2026-09-25: after `iex` of a script ending in `exit 1` the next
        # statement never ran; after one ending in throw it did. Run with
        # -File, an uncaught throw still exits 1.
        throw "nvx was not installed: $_"
    } finally {
        [System.Net.ServicePointManager]::SecurityProtocol = $previousProtocol
    }
}

# 2. Create the shims, before PATH changes.
#
# So they exist whenever PATH takes effect, and so a failure leaves PATH and the
# profile as they were.
try {
    Initialize-NvxShims -NvxExe (Join-Path $binDir "nvx.exe")
} catch {
    throw "Creating the nvx shims failed, so PATH and your profile were not changed. nvx.exe is in $binDir. $_"
}

# 3. Update PATH environment variables for User
if (Set-NvxUserPath -BinDir $binDir) {
    Write-Host "Adding nvx paths to your User environment variables..."
    Send-NvxEnvironmentChange
}
# Update current session path
$env:PATH = "$binDir;$env:PATH"

# 4. PowerShell execution policy.
#
# Load-bearing rather than a nicety: under Restricted -- the default on Windows
# client editions -- PowerShell refuses to load $PROFILE at all, so the
# integration line written in step 5 never runs and nvx never sees a shell.
# RemoteSigned is the narrowest policy that allows it, and the CurrentUser scope
# leaves the machine policy alone.
#
# It used to be changed silently: `-Force -ErrorAction SilentlyContinue`, behind
# a progress line. An installer altering the rule its shell uses to decide what
# code it will execute is not a progress line, and SilentlyContinue meant a
# failure to do it read exactly like success -- the integration would then be
# dead with nothing saying why. Ask, report what happened, and when the answer is
# no, say what that costs and how to do it later.
$interactive = ([Environment]::UserInteractive) -and (-not $env:CI) -and (-not $env:NVX_NONINTERACTIVE) -and ($Host.Name -ne 'Default Host')
$policy = Get-ExecutionPolicy
if (Test-NvxProfileBlockedByPolicy -Policy $policy) {
    Write-Host ""
    Write-Host "nvx's shell integration lives in your PowerShell profile, and this machine's"
    Write-Host "execution policy ($policy) stops PowerShell from loading any profile."
    Write-Host "RemoteSigned, for your user account only, lets local scripts run; anything"
    Write-Host "downloaded still needs a signature. No other account is affected."

    $consent = $false
    if ($interactive) {
        $consent = Test-NvxAffirmative (Read-Host "Set the CurrentUser execution policy to RemoteSigned? [y/N]")
    } else {
        Write-Host "This is not an interactive session, so it is left unchanged."
    }

    if ($consent) {
        try {
            Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser -Force
            Write-Host "Execution policy set to RemoteSigned for the current user."
        } catch {
            Write-Warning "Could not set the execution policy: $_"
            $consent = $false
        }
    }
    if (-not $consent) {
        Write-Host "  nvx will still install, but 'nvx use' cannot change your shell until you run:"
        Write-Host "    Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser"
    }
}

# 5. Add shell integration to PowerShell Profile
if (-not (Test-Path $PROFILE)) {
    Write-Host "Creating PowerShell profile..."
    $profileDir = Split-Path $PROFILE
    if (-not (Test-Path $profileDir)) {
        New-Item -ItemType Directory -Path $profileDir -Force | Out-Null
    }
    New-Item -ItemType File -Path $PROFILE -Force | Out-Null
}

$profileContent = Get-Content $PROFILE -ErrorAction SilentlyContinue
$integrationLine = 'nvx env --shell=powershell | Out-String | Invoke-Expression'

$alreadyIntegrated = $false
if ($profileContent) {
    foreach ($line in $profileContent) {
        if ($line -ne $null -and $line.Trim() -eq $integrationLine) {
            $alreadyIntegrated = $true
            break
        }
    }
}

if (-not $alreadyIntegrated) {
    Write-Host "Adding shell integration to your PowerShell profile..."
    Add-Content -Path $PROFILE -Value "`n# nvx (Node Version X-platform) shell integration`n$integrationLine"

}

Write-Host ""
Write-Host "nvx has been successfully installed!"

Write-Host ""
Write-Host "Please open a new PowerShell window to start using nvx."
}

if ($LibraryOnly) {
    . $nvxInstaller
    Remove-Variable nvxInstaller
    return
}
try {
    & $nvxInstaller
} finally {
    Remove-Variable nvxInstaller -ErrorAction SilentlyContinue
}
