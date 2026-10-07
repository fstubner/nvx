package nvx

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `nvx --strict tsc` answered "Unknown command: tsc", and the way to contain a
// project's own program is `nvx --strict shim tsc`. Through the built binary,
// because the message is printed from the dispatcher and a test of the function
// that builds it would pass with the call deleted.
func TestAProjectBinTypedAfterNvxPointsAtTheShimForm(t *testing.T) {
	dir := tempDir(t)
	exe := filepath.Join(dir, "nvx"+exeSuffixForTest())
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}

	proj := tempDir(t)
	bin := filepath.Join(proj, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"package.json": `{"name":"p","version":"1.0.0"}`} {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tsc := "tsc"
	if runtime.GOOS == "windows" {
		tsc = "tsc.cmd"
	}
	if err := os.WriteFile(filepath.Join(bin, tsc), []byte("echo tsc\n"), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}

	run := func(args ...string) (string, int) {
		cmd := execCommandForTest(exe, args...)
		cmd.Dir = proj
		cmd.Env = append(os.Environ(), "NVX_HOME="+tempDir(t), "NVX_TRACE=")
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		code := 0
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return string(out), code
	}

	out, code := run("--strict", "tsc", "--noEmit")
	if !strings.Contains(out, "nvx --strict shim tsc") {
		t.Errorf("the message does not give the working form:\n%s", out)
	}
	if code != exitUsage {
		t.Errorf("exit %d, want %d for a command nvx does not have", code, exitUsage)
	}

	// Without a flag the form has none, and the flag it was given is not invented.
	out, _ = run("tsc")
	if !strings.Contains(out, "nvx shim tsc") || strings.Contains(out, "--strict") {
		t.Errorf("the form should carry exactly the flags that were typed:\n%s", out)
	}

	// A name the project has no program for gets no such hint, and nothing runs.
	out, code = run("--strict", "eslint")
	if strings.Contains(out, "node_modules/.bin") || code != exitUsage {
		t.Errorf("a program the project does not have was offered the shim form (exit %d):\n%s", code, out)
	}
}
