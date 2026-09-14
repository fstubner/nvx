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

if ($LibraryOnly) { return }

Write-Host "Setting up nvx directories..."

# Create nvx directories if they do not exist
if (-not (Test-Path $binDir)) {
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
}


# 1. Update PATH environment variables for User
if (Set-NvxUserPath -BinDir $binDir) {
    Write-Host "Adding nvx paths to your User environment variables..."
    Send-NvxEnvironmentChange
}
# Update current session path
$env:PATH = "$binDir;$env:PATH"

# 2. PowerShell execution policy.
#
# Load-bearing rather than a nicety: under Restricted -- the default on Windows
# client editions -- PowerShell refuses to load $PROFILE at all, so the
# integration line written in step 3 never runs and nvx never sees a shell.
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

# 3. Add shell integration to PowerShell Profile
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

# 3. Handle Binary Setup
$localBinary = Join-Path $PSScriptRoot "nvx.exe"
if (($UseLocalBinary -or $env:NVX_USE_LOCAL_BINARY -eq "1") -and (Test-Path $localBinary)) {
    Write-Host "Copying compiled nvx.exe to bin directory..."
    Copy-Item -Path $localBinary -Destination (Join-Path $binDir "nvx.exe") -Force
} else {
    $downloadUrl = "https://github.com/fstubner/nvx/releases/latest/download/nvx.exe"

    $checksumUrl = "$downloadUrl.sha256"
    Write-Host "Downloading nvx.exe from $downloadUrl..."
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12
    try {
        $binPath = Join-Path $binDir "nvx.exe"
        Invoke-WebRequest -Uri $downloadUrl -OutFile $binPath -UseBasicParsing
        try {
            $checksumPath = Join-Path $binDir "nvx.exe.sha256"
            Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumPath -UseBasicParsing
            Write-Host "Verifying checksum..."
            $expectedSha = (Get-Content $checksumPath).Split(" ")[0].Trim().ToUpper()
            $actualSha = (Get-FileHash $binPath -Algorithm SHA256).Hash.ToUpper()
            if ($expectedSha -ne $actualSha) {
                Write-Error "Checksum verification failed!"
                Remove-Item $binPath -Force
                Remove-Item $checksumPath -Force
                exit 1
            }
            Write-Host "Checksum verified successfully."
        } catch {
            if ($InsecureSkipChecksum -or $env:NVX_INSECURE_SKIP_CHECKSUM -eq "1") {
                Write-Warning "Checksum file not available. Skipping verification because insecure skip was explicitly requested."
            } else {
                Write-Error "Checksum file not available. Refusing to install without verification."
                Remove-Item $binPath -Force -ErrorAction SilentlyContinue
                Remove-Item $checksumPath -Force -ErrorAction SilentlyContinue
                exit 1
            }
        }
    } catch {
        Write-Error "Failed to download nvx binary: $_"
        exit 1
    }
}



Write-Host ""
Write-Host "nvx has been successfully installed!"

# 4. Offer the one-time sandbox setup.
# Package managers (npm/npx/yarn/pnpm) can only run under the Windows sandbox
# after a one-time elevated grant. Offer it here (default: skip). It is entirely
# optional and re-runnable later with 'nvx setup'.
$nvxExe = Join-Path $binDir "nvx.exe"
if ($interactive -and (Test-Path $nvxExe)) {
    Write-Host ""
    Write-Host "Optional: enable the Windows sandbox for package managers (npm/npx/yarn/pnpm)."
    Write-Host "This needs a single Administrator approval (UAC). You can also do it later with 'nvx setup'."
    $answer = Read-Host "Enable the sandbox now? [y/N]"
    if ($answer -match '^(y|yes)$') {
        Write-Host "Requesting Administrator approval to run 'nvx setup'..."
        try {
            $p = Start-Process -FilePath $nvxExe -ArgumentList 'setup' -Verb RunAs -Wait -PassThru
            if ($p.ExitCode -eq 0) {
                Write-Host "Sandbox setup complete."
            } else {
                Write-Warning "Sandbox setup did not complete. Run 'nvx setup' from an Administrator terminal to try again."
            }
        } catch {
            Write-Warning "Elevation was declined or failed. Run 'nvx setup' from an Administrator terminal later."
        }
    } else {
        Write-Host "Skipped. Package-manager commands will run without OS isolation until you run 'nvx setup'."
        Write-Host "(Supply-chain checks still apply, and you can bypass per command with --no-sandbox.)"
    }
}

Write-Host ""
Write-Host "Please open a new PowerShell window to start using nvx."
