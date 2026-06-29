# Installer script for nvx (Node Version X-platform)


$ErrorActionPreference = 'Stop'

# Define installation paths
$nvxHome = Join-Path $HOME ".nvx"
$binDir = Join-Path $nvxHome "bin"
$currentLink = Join-Path $nvxHome "current"

Write-Host "Setting up nvx directories..."

# Create nvx directories if they do not exist
if (-not (Test-Path $binDir)) {
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
}

# 1. Update PATH environment variables for User
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
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

# Prepend current link (global default node runtime path)
$hasCurrent = $false
foreach ($part in $pathParts) {
    $cleanPart = $part.Trim().TrimEnd('\')
    if ($cleanPart -eq $currentLink.TrimEnd('\')) {
        $hasCurrent = $true
    }
}
if (-not $hasCurrent) {
    $userPath = "$currentLink;$userPath"
    $modified = $true
}

if ($modified) {
    Write-Host "Adding nvx paths to your User environment variables..."
    [Environment]::SetEnvironmentVariable("Path", $userPath, "User")
    # Update current session path
    $env:PATH = "$binDir;$currentLink;$env:PATH"
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
if (Test-Path $localBinary) {
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
            Write-Warning "Checksum file not available. Skipping verification."
        }
    } catch {
        Write-Error "Failed to download nvx binary: $_"
        exit 1
    }
}



Write-Host ""
Write-Host "nvx has been successfully installed!"
Write-Host "Please open a new PowerShell window to start using nvx."
