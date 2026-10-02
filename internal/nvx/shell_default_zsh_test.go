package nvx

import (
	"path/filepath"
	"strings"
	"testing"
)

// A zsh user (the macOS default) running `nvx use 20` without the integration
// was told to edit ~/.bashrc, and `nvx doctor --fix` wrote their integration
// there, where zsh never reads it. SHELL is the only signal off Windows, so this
// tests the function that reads it, which runs on every platform.
func TestSHELLNamingZshSelectsZsh(t *testing.T) {
	for shell, want := range map[string]string{
		"/bin/zsh":               "zsh",
		"/usr/local/bin/zsh":     "zsh",
		"/opt/homebrew/bin/zsh":  "zsh",
		"/usr/bin/fish":          "fish",
		"/bin/bash":              "bash",
		"/bin/sh":                "bash",
		"":                       "bash",
		"/usr/local/bin/zsh-foo": "bash",
	} {
		if got := posixShellFromEnv(shell); got != want {
			t.Errorf("SHELL=%q: got %q, want %q", shell, got, want)
		}
	}
}

// The shell name is only useful if everything keyed on it then does the zsh
// thing: the hint names ~/.zshrc, the profile doctor writes is ~/.zshrc, and the
// session variables are in the syntax zsh evaluates.
func TestZshUserGetsZshOutputEverywhereTheDefaultShellIsUsed(t *testing.T) {
	shell := posixShellFromEnv("/bin/zsh")

	if got := shellIntegrationHint(shell); !strings.Contains(got, ".zshrc") || !strings.Contains(got, "--shell=zsh") {
		t.Errorf("hint for a zsh user: %q", got)
	}
	if got := integrationLineFor(shell); got != `eval "$(nvx env --shell=zsh)"` {
		t.Errorf("integration line for a zsh user: %q", got)
	}

	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, want := profilePathFor(shell), filepath.Join(home, ".zshrc"); got != want {
		t.Errorf("doctor --fix profile for a zsh user = %q, want %q", got, want)
	}

	if got := shellEnvAssignment(shell, "NPM_CONFIG_PREFIX", "/a b"); got != "export NPM_CONFIG_PREFIX='/a b'\n" {
		t.Errorf("session variable for a zsh user: %q", got)
	}
	if got := shellPathFixLine("linux", shell, "/home/u/.nvx/bin"); got != `export PATH="/home/u/.nvx/bin:$PATH"` {
		t.Errorf("PATH fix for a zsh user: %q", got)
	}
	if got := evalHint(shell, "20"); got != `eval "$(nvx use 20)"` {
		t.Errorf("one-shot hint for a zsh user: %q", got)
	}
}
