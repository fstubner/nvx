# Covers install.ps1's execution-policy decision.
#
# The installer used to change the CurrentUser execution policy silently, with
# -Force and -ErrorAction SilentlyContinue behind a progress line: it altered the
# rule the shell uses to decide what code it will run without asking, and a
# failure to do it was indistinguishable from success. It now asks, and this
# covers the two decisions behind that prompt -- whether to offer at all, and
# what counts as a yes.
#
# Decision functions rather than the Set itself, on purpose: the write is one
# built-in call, while the part that has already shipped a defect class in this
# file is installer logic sitting inline where no test can reach it. That is what
# install.ps1 -LibraryOnly exists for, and what the sibling PATH test uses.
#
# Nothing here reads or writes a real execution policy.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $root 'install.ps1') -LibraryOnly

$failures = 0

function Check($label, $expected, $actual) {
    if ($expected -eq $actual) {
        Write-Host "  ok   $label"
    } else {
        Write-Host "  FAIL $label"
        Write-Host "       expected: $expected"
        Write-Host "       actual:   $actual"
        $script:failures++
    }
}

Write-Host "A policy that blocks profile loading is offered a change:"
Check "Restricted blocks it" $true (Test-NvxProfileBlockedByPolicy -Policy 'Restricted')
# No scope has set one, which is Restricted on Windows client editions.
Check "Undefined blocks it" $true (Test-NvxProfileBlockedByPolicy -Policy 'Undefined')
# An unsigned profile does not load under AllSigned either, and CurrentUser
# outranks LocalMachine, so the same change is the same fix.
Check "AllSigned blocks it" $true (Test-NvxProfileBlockedByPolicy -Policy 'AllSigned')

Write-Host "A policy that already allows it is left alone:"
# The regression this guards. The check read Get-ExecutionPolicy -Scope
# CurrentUser, which is Undefined whenever the policy is set at another scope --
# measured on a machine with CurrentUser Undefined and effective RemoteSigned.
# Reading the scope rather than the effective answer offers to change a setting
# that is already correct.
Check "RemoteSigned is not touched" $false (Test-NvxProfileBlockedByPolicy -Policy 'RemoteSigned')
Check "Unrestricted is not touched" $false (Test-NvxProfileBlockedByPolicy -Policy 'Unrestricted')
Check "Bypass is not touched" $false (Test-NvxProfileBlockedByPolicy -Policy 'Bypass')

Write-Host "Only an explicit yes consents:"
Check "y" $true (Test-NvxAffirmative 'y')
Check "yes" $true (Test-NvxAffirmative 'yes')
Check "Y, whatever the case" $true (Test-NvxAffirmative 'Y')
Check "yes with stray whitespace" $true (Test-NvxAffirmative '  yes  ')
# Pressing Return at a [y/N] prompt. This is what makes the default safe: an
# empty answer must never authorise a policy change.
Check "an empty answer is no" $false (Test-NvxAffirmative '')
Check "n is no" $false (Test-NvxAffirmative 'n')
# Not a prefix match: a sentence starting with "yes" is not a yes, and more to
# the point neither is anything else that merely contains it.
Check "'yes please' is no" $false (Test-NvxAffirmative 'yes please')
Check "'nope' is no" $false (Test-NvxAffirmative 'nope')

if ($failures -gt 0) {
    Write-Error "$failures installer execution-policy check(s) failed"
    exit 1
}
Write-Host "Installer execution-policy checks passed."
