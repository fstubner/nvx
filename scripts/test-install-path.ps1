# Covers install.ps1's User PATH update, against a scratch registry key.
#
# That one write has already shipped a defect: it read the PATH through an API
# that expands %VAR%, and wrote it back through one that always stores REG_SZ,
# so installing nvx replaced every variable-spelled entry with that day's
# expansion and made sure none would ever be expanded again. It had no test
# because the logic sat inline in a script that installs software when you run
# it. install.ps1 -LibraryOnly exists so this file can reach it.
#
# HKCU\Software\nvx-install-path-test, never HKCU\Environment.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $root 'install.ps1') -LibraryOnly

$key = 'HKCU:\Software\nvx-install-path-test'
$failures = 0

function Check($label, $expected, $actual) {
    if ($expected -ceq $actual) {
        Write-Host "  ok   $label"
    } else {
        Write-Host "  FAIL $label"
        Write-Host "       expected: $expected"
        Write-Host "       actual:   $actual"
        $script:failures++
    }
}

function Reset($value, $kind) {
    if (Test-Path $key) { Remove-Item -Path $key -Recurse -Force }
    New-Item -Path $key -Force | Out-Null
    Set-ItemProperty -Path $key -Name 'Path' -Value $value -Type $kind
}

function Raw {
    (Get-Item $key).GetValue(
        'Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
}

Write-Host "An expandable PATH keeps its type and its variables:"
Reset '%USERPROFILE%\bin;C:\Windows' 'ExpandString'
$changed = Set-NvxUserPath -BinDir 'C:\Users\test\.nvx\bin' -KeyPath $key
Check "reports that it changed something" $true $changed
Check "prepends the bin directory" 'C:\Users\test\.nvx\bin;%USERPROFILE%\bin;C:\Windows' (Raw)
Check "keeps the value expandable" 'ExpandString' ((Get-Item $key).GetValueKind('Path').ToString())

Write-Host "A plain PATH is not converted the other way:"
Reset 'C:\literal;C:\Windows' 'String'
$null = Set-NvxUserPath -BinDir 'C:\Users\test\.nvx\bin' -KeyPath $key
Check "leaves the value plain" 'String' ((Get-Item $key).GetValueKind('Path').ToString())

Write-Host "Running it twice does not add the directory twice:"
Reset '%USERPROFILE%\bin' 'ExpandString'
$null = Set-NvxUserPath -BinDir 'C:\Users\test\.nvx\bin' -KeyPath $key
$second = Set-NvxUserPath -BinDir 'C:\Users\test\.nvx\bin' -KeyPath $key
Check "reports no change the second time" $false $second
Check "and the value is unchanged" 'C:\Users\test\.nvx\bin;%USERPROFILE%\bin' (Raw)

Write-Host "A trailing backslash is still recognised as the same directory:"
Reset 'C:\Users\test\.nvx\bin\;C:\Windows' 'ExpandString'
$again = Set-NvxUserPath -BinDir 'C:\Users\test\.nvx\bin' -KeyPath $key
Check "reports no change" $false $again

Remove-Item -Path $key -Recurse -Force
if ($failures -gt 0) {
    Write-Error "$failures installer PATH check(s) failed"
    exit 1
}
Write-Host "Installer PATH checks passed."
