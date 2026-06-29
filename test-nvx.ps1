# test-nvx.ps1
# Script to verify nvx node installation and NPM isolation

$ErrorActionPreference = 'Stop'

Write-Host "1. Installing Node 18..." -ForegroundColor Cyan
& "$env:USERPROFILE\.nvx\bin\nvx.exe" install 18

# Verify that v18 folder exists
$versions = Get-ChildItem -Path "$env:USERPROFILE\.nvx\versions"
Write-Host "Installed versions in ~/.nvx/versions:"
$versions | Format-Table -Property Name

# Load the env wrapper
Write-Host "2. Evaluating nvx environment wrapper..." -ForegroundColor Cyan
& "$env:USERPROFILE\.nvx\bin\nvx.exe" env --shell=powershell | Out-String | Invoke-Expression


# Switch to the installed version
Write-Host "3. Activating Node 18..." -ForegroundColor Cyan
nvx use 18

# Verify Node execution path and version
Write-Host "4. Verifying active Node runtime..." -ForegroundColor Cyan
$nodePath = Get-Command node | Select-Object -ExpandProperty Source
Write-Host "Node path: $nodePath"
$nodeVer = node -v
Write-Host "Node version: $nodeVer"

# Verify NPM isolation variables
Write-Host "5. Checking NPM isolation..." -ForegroundColor Cyan
Write-Host "NPM_CONFIG_PREFIX: $env:NPM_CONFIG_PREFIX"

# Verify path includes npm_global
if ($env:PATH -like "*npm_global*") {
    Write-Host "PATH contains npm_global: Yes" -ForegroundColor Green
} else {
    Write-Host "PATH contains npm_global: No" -ForegroundColor Red
    exit 1
}

Write-Host "6. Installing a global npm package..." -ForegroundColor Cyan
# Install a very small global npm package: is-sorted
npm install -g is-sorted

# Verify that the package was installed to our isolated directory
$packagePath = Join-Path $env:NPM_CONFIG_PREFIX "node_modules\is-sorted"
if (Test-Path $packagePath) {
    Write-Host "Global package successfully isolated under nvx version folder!" -ForegroundColor Green
} else {
    Write-Host "Global package was not found in the isolated folder: $packagePath" -ForegroundColor Red
    exit 1
}

Write-Host "`nAll verifications passed successfully!" -ForegroundColor Green
