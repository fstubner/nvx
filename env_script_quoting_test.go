package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The bash integration quotes the nvx path for bash, not for Go.
//
// envScript inserted the binary's path with %q, which is Go's quoting: a
// character outside ASCII becomes \u00eb, and $ and backticks are left for the
// shell to expand inside the double quotes. A user whose home directory has an
// accented letter in it -- not exotic -- got an integration whose every
// `nvx use` and every cd hook ran a path bash could not find. A single-quoted
// POSIX string is what the shell needs, and quotePOSIXShell already produces
// one for the PATH assignments this same code emits.
func TestTheBashIntegrationQuotesTheBinaryForBash(t *testing.T) {
	exe := `C:\Users\Fëlix $HOME\nvx.exe`
	// A POSIX-style shim dir: on Unix the prepend snippet keeps the string as
	// given, and a Windows one would put a literal backslash-U into the script
	// that has nothing to do with the quoting under test.
	script := envScript("bash", exe, "/home/fëlix/.nvx/bin")

	if strings.Contains(script, `\u00eb`) {
		t.Fatalf("the integration carries Go's escape for a non-ASCII byte, which bash does not decode:\n%s", excerpt(script, "u00"))
	}
	want := quotePOSIXShell("C:/Users/Fëlix $HOME/nvx.exe")
	if !strings.Contains(script, want) {
		t.Fatalf("the integration does not contain the single-quoted binary path %s:\n%s", want, excerpt(script, "nvx.exe"))
	}
}

// And through a real bash: a stand-in nvx whose path holds a space, a dollar
// sign and a non-ASCII letter is what the integration's `nvx` function ends up
// running.
func TestTheBashIntegrationRunsTheBinaryAtItsRealPath(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on this host")
	}
	dir := filepath.Join(tempDir(t), "Fëlix $HOME dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "nvx")
	// The stand-in records that it ran and with what. `use` makes the
	// integration evaluate stdout, so it prints an assignment.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf 'export NVX_TEST_RAN=%s\\n' \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	exeForScript := fake
	if runtime.GOOS == "windows" {
		// bash on Windows is Git Bash; it takes the path the script will spell.
		exeForScript = fake
	}
	script := envScript("bash", exeForScript, filepath.Join(dir, "bin"))

	probe := script + "\nnvx use 1 >/dev/null\necho \"ran=$NVX_TEST_RAN\"\n"
	out, err := exec.Command(bash, "-c", probe).CombinedOutput()
	if err != nil {
		t.Fatalf("bash could not evaluate the integration: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ran=use") {
		t.Fatalf("the integration's nvx function did not run the binary at its real path:\n%s", out)
	}
}

func excerpt(s, around string) string {
	i := strings.Index(s, around)
	if i < 0 {
		return s[:min(len(s), 400)]
	}
	lo, hi := i-120, i+120
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}
