//go:build !windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Off Windows only $SHELL can say, and only fish needs a different syntax.
// (Windows reads the parent process instead. See shell_parent_windows_test.go.)
func TestFishIsDetectedFromShellOffWindows(t *testing.T) {
	for shell, want := range map[string]string{
		"/usr/bin/fish":          "fish",
		"/opt/homebrew/bin/fish": "fish",
		"/bin/zsh":               "bash",
		"":                       "bash",
	} {
		withEnv(t, "SHELL", shell)
		if got := defaultShell(); got != want {
			t.Errorf("SHELL=%q: defaultShell() = %q, want %q", shell, got, want)
		}
	}
}

// fish is not on every machine. When it is, it must at least parse the snippet:
// the text tests above cannot say whether fish accepts it.
func TestTheFishSnippetParsesUnderFish(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed here, so the snippet was not parsed by fish")
	}
	file := filepath.Join(tempDir(t), "nvx.fish")
	if err := os.WriteFile(file, []byte(envScript("fish", "/opt/nvx/nvx", "/home/u/.nvx/bin")), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(fish, "--no-execute", file).CombinedOutput(); err != nil {
		t.Errorf("fish rejected the snippet: %v\n%s", err, out)
	}
}
