package nvx

import (
	"strings"
	"testing"
)

// A zsh terminal opened inside a project switches version without a cd.
//
// The zsh hook is chpwd, which fires only when the directory changes, so a new
// terminal that starts in a project kept the default version until the first
// cd. bash switches on its first prompt. The snippet therefore has to run the
// hook once itself, inside the zsh branch only.
//
// This pins the snippet text. Whether zsh then runs it was not run in this
// test, which has no zsh.
func TestTheZshSnippetRunsTheHookOnceAtLoad(t *testing.T) {
	script := envScript("zsh", "/opt/nvx", "/home/u/.nvx/bin")

	start := strings.Index(script, `if [[ -n "$ZSH_VERSION" ]]; then`)
	end := strings.Index(script, "elif [[ -n \"$BASH_VERSION\" ]]")
	if start < 0 || end < start {
		t.Fatalf("could not find the zsh branch in:\n%s", script)
	}
	branch := script[start:end]

	register := strings.Index(branch, "add-zsh-hook chpwd nvx_chpwd_hook")
	if register < 0 {
		t.Fatalf("the zsh branch no longer registers the chpwd hook:\n%s", branch)
	}
	called := false
	for _, line := range strings.Split(branch[register:], "\n")[1:] {
		if strings.TrimSpace(line) == "nvx_chpwd_hook" {
			called = true
		}
	}
	if !called {
		t.Errorf("the zsh branch registers the hook but never runs it once, so a terminal opened in a project is not switched until a cd:\n%s", branch)
	}

	// The bash branch keeps its prompt hook alone.
	bashBranch := script[end:]
	if strings.Contains(bashBranch, "nvx_chpwd_hook") {
		t.Errorf("the bash branch runs the zsh hook:\n%s", bashBranch)
	}
}
