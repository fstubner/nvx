//go:build windows

package main

// The PowerShell integration has to survive two things the bash one already did:
// a console codepage that is not UTF-8, and being loaded twice.
//
// Both failed silently, on the platform this project was started for, and both
// leave nvx installed and inert rather than erroring.

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var psBase64Path = regexp.MustCompile(`FromBase64String\('([A-Za-z0-9+/=]*)'\)`)

// powershellDecodedPaths returns the paths a PowerShell snippet decodes at
// runtime, so a test can assert the VALUE the shell ends up with rather than how
// it happens to be spelled on the wire.
func powershellDecodedPaths(script string) []string {
	var out []string
	for _, m := range psBase64Path.FindAllStringSubmatch(script, -1) {
		if raw, err := base64.StdEncoding.DecodeString(m[1]); err == nil {
			out = append(out, string(raw))
		}
	}
	return out
}

func decodedPathsContain(script, want string) bool {
	for _, p := range powershellDecodedPaths(script) {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

// A shim directory under a home with an accent in it must reach the shell intact.
//
// PowerShell decodes a native command's stdout with [Console]::OutputEncoding,
// a legacy OEM codepage by default, while nvx writes UTF-8. Measured 2026-09-02
// on Windows PowerShell 5.1 and pwsh 7 (both ibm850): the U+00E4 in the emitted
// path arrived as U+251C U+00F1, Test-Path on the result was False, and `node`
// was then not found -- with no error at any point. Any account named Müller,
// José or Łukasz got an nvx that never intercepted anything.
//
// So the emitted script must be pure ASCII: bytes no codepage can disagree about.
func TestPowerShellIntegrationSurvivesANonASCIIPath(t *testing.T) {
	const home = `C:\Users\Müller\.nvx\bin`
	const exe = `C:\Users\Müller\.nvx\bin\nvx.exe`
	script := envScript("powershell", exe, home)

	for i := 0; i < len(script); i++ {
		if script[i] > 127 {
			t.Fatalf("the PowerShell script carries a non-ASCII byte at offset %d, so a console "+
				"codepage that is not UTF-8 will corrupt it and the shim dir will name a "+
				"directory that does not exist. Script:\n%s", i, script)
		}
	}

	// Pure ASCII is only half of it: the script must still yield the real paths.
	if !decodedPathsContain(script, `Müller`) {
		t.Errorf("the script is ASCII but no longer carries the real path; it decodes to %v",
			powershellDecodedPaths(script))
	}
	if !decodedPathsContain(script, `nvx.exe`) {
		t.Errorf("the nvx binary path is not recoverable from the script: %v",
			powershellDecodedPaths(script))
	}
}

// Loading the integration twice must leave a working prompt.
//
// The wrapper captured $old_prompt and resolved that name at CALL time, so a
// second load made it call itself -- an empty prompt, then CallDepthOverflow.
// `. $PROFILE` after upgrading nvx is enough. The prompt is what drives
// auto-switching, so this takes .nvmrc with it.
//
// Driven through a real PowerShell rather than by matching the emitted text,
// because what matters is whether the shell survives it.
func TestPowerShellIntegrationCanBeLoadedTwice(t *testing.T) {
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skipf("no powershell.exe on PATH: %v", err)
	}

	dir := tempDir(t)
	exe := filepath.Join(dir, "nvx.exe")
	script := envScript("powershell", exe, filepath.Join(dir, "bin"))

	scriptPath := filepath.Join(dir, "nvxenv.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	// __nvx_last_pwd is set to the current directory AFTER loading, so the hook
	// short-circuits and never shells out -- there is no real nvx.exe here. The
	// recursion this test is about lives in the prompt WRAPPER, not in the hook,
	// so it still fires: the wrapper calls what it captured, and on a second load
	// that is itself.
	driver := `
$ErrorActionPreference = 'Stop'
function prompt { "PS> " }
. ` + "'" + scriptPath + "'" + `
. ` + "'" + scriptPath + "'" + `
$global:__nvx_last_pwd = $pwd
$p = prompt
if ([string]::IsNullOrEmpty($p)) { Write-Output 'EMPTY_PROMPT'; exit 1 }
Write-Output "PROMPT_OK:$p"
`
	driverPath := filepath.Join(dir, "drive.ps1")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", driverPath).CombinedOutput()
	got := strings.TrimSpace(string(out))
	if err != nil || !strings.Contains(got, "PROMPT_OK") {
		t.Errorf("loading the integration twice left the prompt broken (err=%v).\n"+
			"That happens on a plain `. $PROFILE` after upgrading nvx, and it takes .nvmrc "+
			"auto-switching down with it.\nOutput:\n%s", err, got)
	}
}
