//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Run in a real cmd.exe, because the claim is about what cmd does with the text:
// that `FOR /f ... DO %i` over `nvx env --shell=cmd` puts the shim directory
// first on that window's PATH, and that the parent nvx finds really is cmd.exe.
func TestCmdForLoopPutsTheShimsFirstOnPath(t *testing.T) {
	dir := tempDir(t)
	exe := filepath.Join(dir, "nvx.exe")
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}
	home := filepath.Join(dir, "home 50% (parens) & ampersand")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(home, "bin")

	run := func(command string) string {
		t.Helper()
		cmd := exec.Command("cmd.exe")
		// CmdLine, not Args: Go would re-quote the string, and the quotes inside
		// it are the text under test.
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /c "` + command + `"`}
		cmd.Env = append(os.Environ(),
			"NVX_HOME="+home, "NVX_TRACE=", "NVX_SHELL_INTEGRATION=",
			"MSYSTEM=MINGW64", // inherited from Git Bash, as it is for a cmd opened there
			"PATH="+dir+";"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd /c %s: %v\n%s", command, err, out)
		}
		return string(out)
	}

	out := run(`FOR /f "tokens=*" %i IN ('nvx env --shell=cmd') DO @%i & where node`)
	t.Logf("shim dir %s\n%s", shimDir, out)
	first := strings.SplitN(strings.TrimSpace(out), "\r\n", 2)[0]
	if !strings.EqualFold(filepath.Dir(first), shimDir) {
		t.Errorf("after the for loop the first `node` on PATH is %q, want one under %q\n%s", first, shimDir, out)
	}

	// Run from cmd with MSYSTEM inherited and no --shell, nvx must see cmd.exe as
	// its parent and not print the POSIX syntax that variable used to select.
	out = run(`nvx env`)
	if !strings.HasPrefix(out, `set "PATH=`) || strings.Contains(out, "export ") {
		t.Errorf("`nvx env` from cmd.exe printed:\n%s", out)
	}
}
