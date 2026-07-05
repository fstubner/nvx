param(
    [string]$Version = "0.3.0",
    [string]$GoVersion = "1.26.4"
)

# build-release.ps1
# Script to download Go and cross-compile release binaries with checksums

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$scratchDir = Join-Path $env:TEMP "nvx-build"

$goTempDir = Join-Path $scratchDir "go_temp"
$goExe = Join-Path $goTempDir "go\bin\go.exe"
$distDir = Join-Path $PSScriptRoot "dist"

# 1. Download and extract Go if not present
if (-not (Test-Path $goExe)) {
    Write-Host "Go $GoVersion compiler not found. Downloading..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Path $scratchDir -Force | Out-Null
    New-Item -ItemType Directory -Path $goTempDir -Force | Out-Null
    $zipPath = Join-Path $scratchDir "go$GoVersion.zip"
    $checksumPath = "$zipPath.sha256"
    
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12
    $goUrl = "https://go.dev/dl/go$GoVersion.windows-amd64.zip"
    Invoke-WebRequest -Uri $goUrl -OutFile $zipPath -UseBasicParsing
    Invoke-WebRequest -Uri "$goUrl.sha256" -OutFile $checksumPath -UseBasicParsing
    $expectedSha = (Get-Content $checksumPath).Split(" ")[0].Trim().ToUpper()
    $actualSha = (Get-FileHash $zipPath -Algorithm SHA256).Hash.ToUpper()
    if ($expectedSha -ne $actualSha) {
        Remove-Item $zipPath, $checksumPath -Force -ErrorAction SilentlyContinue
        throw "Go toolchain checksum verification failed."
    }
    
    Write-Host "Extracting Go $GoVersion..." -ForegroundColor Cyan
    Expand-Archive -Path $zipPath -DestinationPath $goTempDir -Force
    Remove-Item $zipPath, $checksumPath -Force
}

# Verify Go version
$goVer = & $goExe version
Write-Host "Using Go compiler: $goVer" -ForegroundColor Green

# 2. Setup build distribution directory
if (Test-Path $distDir) {
    Remove-Item $distDir -Recurse -Force
}
New-Item -ItemType Directory -Path $distDir -Force | Out-Null

# 3. Define build matrix
$matrix = @(
    @{ os = "linux";  arch = "amd64"; ext = "";    name = "nvx-linux-amd64" },
    @{ os = "linux";  arch = "arm64"; ext = "";    name = "nvx-linux-arm64" },
    @{ os = "darwin"; arch = "amd64"; ext = "";    name = "nvx-darwin-amd64" },
    @{ os = "darwin"; arch = "arm64"; ext = "";    name = "nvx-darwin-arm64" },
    @{ os = "windows";arch = "amd64"; ext = ".exe"; name = "nvx" }
)

# 4. Compile targets
foreach ($target in $matrix) {
    $outName = $target.name + $target.ext
    $outPath = Join-Path $distDir $outName
    
    Write-Host "Building $outName (OS: $($target.os), Arch: $($target.arch))..." -ForegroundColor Cyan
    
    $env:GOOS = $target.os
    $env:GOARCH = $target.arch
    
    & $goExe build -ldflags="-s -w -X main.appVersion=$Version" -o $outPath .
    
    # Reset env variables
    $env:GOOS = $null
    $env:GOARCH = $null
    
    # 5. Compute SHA-256 checksum file
    Write-Host "Computing SHA-256 for $outName..." -ForegroundColor Yellow
    $hash = (Get-FileHash -Path $outPath -Algorithm SHA256).Hash.ToLower()
    $checksumContent = "$hash  $outName`n"
    $checksumPath = "$outPath.sha256"
    [System.IO.File]::WriteAllText($checksumPath, $checksumContent)
}

Write-Host "`nAll release binaries and checksum files successfully built under ./dist!" -ForegroundColor Green
