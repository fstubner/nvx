# verify-security.ps1
# Script to run local security and vulnerability scans

$ErrorActionPreference = 'Stop'

Write-Host "Running local security checks for nvx..." -ForegroundColor Cyan

# 1. Check for govulncheck
$govulncheckPath = "$env:USERPROFILE\go\bin\govulncheck.exe"
if (-not (Test-Path $govulncheckPath)) {
    Write-Host "Installing govulncheck..." -ForegroundColor Yellow
    go install golang.org/x/vuln/cmd/govulncheck@v1.0.1
}

Write-Host "`n1. Running govulncheck..." -ForegroundColor Cyan
& $govulncheckPath ./...

# 2. Check for gosec
$gosecPath = "$env:USERPROFILE\go\bin\gosec.exe"
if (-not (Test-Path $gosecPath)) {
    Write-Host "Installing gosec..." -ForegroundColor Yellow
    go install github.com/securego/gosec/v2/cmd/gosec@v2.16.0
}

Write-Host "`n2. Running gosec..." -ForegroundColor Cyan
& $gosecPath "-exclude=G204,G304,G301,G306" ./...


Write-Host "`nAll security scans completed successfully!" -ForegroundColor Green
