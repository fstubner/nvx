# nvx uninstaller (Windows). Removes the nvx home, shims, and shell integration.
$ErrorActionPreference = 'Stop'

$nvxHome = if ($env:NVX_HOME) { $env:NVX_HOME } else { Join-Path $env:USERPROFILE ".nvx" }

Write-Host "This will remove nvx:"
Write-Host "  - directory: $nvxHome (installed runtimes, shims, policy, cache)"
Write-Host "  - the 'nvx env' line from your PowerShell profile"
$ans = Read-Host "Proceed? [y/N]"
if ($ans -notmatch '^(y|Y|yes|YES)$') { Write-Host "Aborted."; exit 0 }

# Strip the integration line from the PowerShell profile.
if (Test-Path $PROFILE) {
    $lines = Get-Content $PROFILE | Where-Object { $_ -notmatch 'nvx env' }
    Set-Content -Path $PROFILE -Value $lines
    Write-Host "Removed nvx integration from $PROFILE"
}

# Remove the nvx entries from the User PATH that install.ps1 added
# (~/.nvx/bin and ~/.nvx/current), so no dangling entries remain.
$binDir = Join-Path $nvxHome "bin"
$currentLink = Join-Path $nvxHome "current"
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath) {
    $kept = $userPath.Split(';') | Where-Object {
        $_ -and $_.TrimEnd('\') -ne $binDir.TrimEnd('\') -and $_.TrimEnd('\') -ne $currentLink.TrimEnd('\')
    }
    [Environment]::SetEnvironmentVariable("Path", ($kept -join ';'), "User")
    Write-Host "Removed nvx entries from User PATH"
}

# Note: install.ps1 may have set the PowerShell execution policy to RemoteSigned;
# it is intentionally left as-is (a reasonable baseline other tools may rely on).
Write-Host "Note: PowerShell execution policy (if changed to RemoteSigned during install) was left unchanged."

# Remove the nvx home.
if (Test-Path $nvxHome) {
    Remove-Item -Recurse -Force $nvxHome
    Write-Host "Removed $nvxHome"
}

# Remove the binary if discoverable and writable.
$cmd = Get-Command nvx -ErrorAction SilentlyContinue
if ($cmd) {
    try {
        Remove-Item -Force $cmd.Source
        Write-Host "Removed $($cmd.Source)"
    } catch {
        Write-Host "Note: remove the nvx binary manually: $($cmd.Source)"
    }
}

Write-Host "nvx uninstalled. Restart your shell to finish."
