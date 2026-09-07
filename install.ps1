param(
    [switch]$InsecureSkipChecksum,
    [switch]$UseLocalBinary
)

# Installer script for nvx (Node Version X-platform)

$ErrorActionPreference = 'Stop'

# Define installation paths
$nvxHome = Join-Path $HOME ".nvx"
$binDir = Join-Path $nvxHome "bin"

Write-Host "Setting up nvx directories..."

# Create nvx directories if they do not exist
if (-not (Test-Path $binDir)) {
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
}

# 1. Update PATH environment variables for User
#
# Read raw, not through [Environment]::GetEnvironmentVariable: that one EXPANDS
# a REG_EXPAND_SZ value, so an entry written as %USERPROFILE%in comes back as
# the expanded path, and writing it back would bake today's expansion into the
# registry for ever. DoNotExpandEnvironmentNames keeps what the user actually
# wrote.
$envKey = 'HKCU:\Environment'
$envItem = Get-Item -Path $envKey
$userPath = $envItem.GetValue(
    'Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)

# And remember the type, because Windows ships this value as REG_EXPAND_SZ and
# that is what makes %VAR% entries resolve at all. The obvious writer,
# [Environment]::SetEnvironmentVariable, always writes REG_SZ -- so this
# installer used to convert the type on every machine it ran on, and every
# variable-spelled entry on it silently stopped resolving. nvx had the same bug
# in `doctor --fix`; this is the same fix.
$pathKind = 'ExpandString'
try {
    if ($envItem.GetValueKind('Path') -eq [Microsoft.Win32.RegistryValueKind]::String) {
        $pathKind = 'String'
    }
} catch {
    # No Path value yet on a fresh profile: ExpandString is what Windows would
    # have created.
}
$pathParts = $userPath -split ';'
$modified = $false

# Prepend bin directory (for nvx binary itself)
$hasBin = $false
foreach ($part in $pathParts) {
    $cleanPart = $part.Trim().TrimEnd('\')
    if ($cleanPart -eq $binDir.TrimEnd('\')) {
        $hasBin = $true
    }
}
if (-not $hasBin) {
    $userPath = "$binDir;$userPath"
    $modified = $true
}

if ($modified) {
    Write-Host "Adding nvx paths to your User environment variables..."
    Set-ItemProperty -Path $envKey -Name 'Path' -Value $userPath -Type $pathKind

    # Tell running programs the environment moved. [Environment]::SetEnvironment-
    # Variable did this as part of its own work; a plain registry write does not,
    # and without it Explorer keeps handing its stale copy to everything launched
    # from it until the next sign-in. Best effort: the value is already written,
    # and a machine that will not compile the P/Invoke should not fail the
    # install over a notification.
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

    # Update current session path
    $env:PATH = "$binDir;$env:PATH"
}

# 2. Check and configure PowerShell Execution Policy
$policy = Get-ExecutionPolicy -Scope CurrentUser
if ($policy -eq 'Restricted' -or $policy -eq 'Undefined') {
    Write-Host "Configuring PowerShell execution policy to RemoteSigned..."
    Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser -Force -ErrorAction SilentlyContinue
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
$interactive = ([Environment]::UserInteractive) -and (-not $env:CI) -and (-not $env:NVX_NONINTERACTIVE) -and ($Host.Name -ne 'Default Host')
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
