package nvx

import "testing"

// A shell nvx does not know is an error, not PowerShell.
//
// parseShellArg returned whatever followed --shell= and every emitter's default
// branch is PowerShell, so `nvx use 20 --shell=fish` printed PowerShell
// assignments for fish to evaluate. The shell integration passes --shell
// itself, so this was reachable mainly from a hand-written hook or a typo, and
// what it produced was a stream of syntax errors from a shell that had asked
// nvx what to do.
func TestAnUnknownShellIsRefusedRatherThanGuessed(t *testing.T) {
	for _, args := range [][]string{
		{"--shell=fish"},
		{"--shell", "cmd"},
		{"--shell=Power_Shell"},
	} {
		if got, err := parseShellArg(args); err == nil {
			t.Errorf("parseShellArg(%v) = %q with no error; PowerShell would be emitted for a shell that is not PowerShell", args, got)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--shell=bash"}, "bash"},
		{[]string{"--shell=ZSH"}, "zsh"},
		{[]string{"--shell", "pwsh"}, "pwsh"},
		{[]string{"powershell"}, "powershell"},
	} {
		got, err := parseShellArg(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("parseShellArg(%v) = %q, %v; want %q", tc.args, got, err, tc.want)
		}
	}
}
