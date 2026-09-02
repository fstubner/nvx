//go:build windows

package main

// The one PowerShell assertion that needs a real powershell.exe. The rest of the
// integration's text is checked in shell_integration_test.go, on every platform.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
