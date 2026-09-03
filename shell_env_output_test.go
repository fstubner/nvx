package main

import (
	"strings"
	"testing"
)

// `nvx use` prints ~6,000 characters of PATH for a shell to evaluate. With the
// integration loaded that is right and the text never reaches the screen.
// Without it, the second command a newcomer is told to run fills the terminal,
// and the sentence explaining what to do is printed underneath the wall --
// already scrolled past on most terminals.
//
// Suppressed only when nothing will evaluate it AND stdout is a terminal. The
// second half is load-bearing: `eval "$(nvx use 22)"` is the escape hatch the
// warning itself recommends, and there stdout is a pipe.
func TestTheEnvironmentBlockIsOnlyPrintedForSomethingThatReadsIt(t *testing.T) {
	orig := stdoutIsTerminal
	t.Cleanup(func() { stdoutIsTerminal = orig })

	for _, tc := range []struct {
		name            string
		terminal        bool
		viaIntegration  bool
		integrationSeen bool
		want            bool
	}{
		{"a person at a terminal, no integration", true, false, false, false},
		{"piped into eval, no integration", false, false, false, true},
		{"invoked by the integration", true, true, false, true},
		{"integration loaded in this shell", true, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdoutIsTerminal = func() bool { return tc.terminal }
			if tc.integrationSeen {
				t.Setenv("NVX_SHELL_INTEGRATION", "1")
			} else {
				t.Setenv("NVX_SHELL_INTEGRATION", "")
			}
			if got := shouldPrintShellEnv(tc.viaIntegration); got != tc.want {
				t.Errorf("shouldPrintShellEnv = %v, want %v", got, tc.want)
			}
		})
	}
}

// The guidance names a command the reader can paste, in their own shell's
// syntax. The message this replaced said "see 'nvx env'" -- and `nvx env`
// prints the integration script, so following it literally means watching
// several hundred lines go past and being no better off.
func TestTheIntegrationHintIsACommandNotAPointer(t *testing.T) {
	for _, tc := range []struct{ shell, mustContain, mustNotContain string }{
		{"powershell", "Invoke-Expression", "~/.bashrc"},
		{"bash", `eval "$(nvx env --shell=bash)"`, "PROFILE"},
		{"zsh", `eval "$(nvx env --shell=zsh)"`, "bashrc"},
	} {
		got := shellIntegrationHint(tc.shell)
		if !strings.Contains(got, tc.mustContain) {
			t.Errorf("%s hint %q does not contain %q", tc.shell, got, tc.mustContain)
		}
		if strings.Contains(got, tc.mustNotContain) {
			t.Errorf("%s hint %q mentions %q, which belongs to another shell", tc.shell, got, tc.mustNotContain)
		}
	}

	// And the one-shot form actually names the version being switched to, so it
	// can be pasted rather than adapted.
	if got := evalHint("bash", "22"); !strings.Contains(got, "nvx use 22") {
		t.Errorf("bash eval hint %q does not name the version", got)
	}
	if got := evalHint("powershell", "22"); !strings.Contains(got, "Invoke-Expression") || !strings.Contains(got, "22") {
		t.Errorf("powershell eval hint %q is not a runnable line", got)
	}
}
