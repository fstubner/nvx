package nvx

import (
	"testing"
)

// `nvx setup` runs only the arguments it understands.
//
// The setup command scanned its arguments for `--undo` and `--all-drives`
// and ignored everything else, so `nvx setup --help` ran setup, and so did
// `nvx setup --undoo` -- forward, granting, when the person typing it meant
// to take the grant back. Setup is the one command that changes ACLs on the
// machine's drive roots and needs an Administrator terminal to do it, which
// is the worst place for "unrecognised means proceed".
func TestSetupRefusesArgumentsItDoesNotUnderstand(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		undo      bool
		allDrives bool
		help      bool
		wantErr   bool
	}{
		{"no arguments", nil, false, false, false, false},
		{"--undo", []string{"--undo"}, true, false, false, false},
		{"-u", []string{"-u"}, true, false, false, false},
		{"--all-drives", []string{"--all-drives"}, false, true, false, false},
		{"both", []string{"--undo", "--all-drives"}, true, true, false, false},
		{"--help asks for help, not setup", []string{"--help"}, false, false, true, false},
		{"-h asks for help, not setup", []string{"-h"}, false, false, true, false},
		{"help after a real flag still asks for help", []string{"--undo", "--help"}, true, false, true, false},
		{"a typo of --undo is an error, not a forward setup", []string{"--undoo"}, false, false, false, true},
		{"an unknown flag is an error", []string{"--yes"}, false, false, false, true},
		{"a stray word is an error", []string{"now"}, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSetupArgs(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseSetupArgs(%v) error = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if got.undo != tc.undo || got.allDrives != tc.allDrives || got.help != tc.help {
				t.Errorf("parseSetupArgs(%v) = %+v, want undo=%v allDrives=%v help=%v",
					tc.args, got, tc.undo, tc.allDrives, tc.help)
			}
		})
	}
}

// And the command itself does not reach the elevated path for help or for an
// error, while a well-formed invocation does.
//
// The elevated path is recorded, never run. A first version of this test
// called the real thing for the control case, reasoning that an unelevated
// test process would be stopped at the elevation check. GitHub's Windows
// runners are elevated, so on CI it ran `nvx setup --undo` against the
// runner's drive roots for several minutes.
func TestSetupCommandDoesNotAttemptSetupForHelpOrABadArgument(t *testing.T) {
	orig := runSetupImpl
	t.Cleanup(func() { runSetupImpl = orig })
	reached := 0
	var gotUndo, gotAll bool
	runSetupImpl = func(_ string, undo, allDrives bool) int {
		reached++
		gotUndo, gotAll = undo, allDrives
		return 0
	}

	if code := runSetupCommand([]string{"--help"}, tempDir(t)); code != 0 || reached != 0 {
		t.Errorf("`setup --help`: exit %d, elevated path reached %d times; want 0 and 0", code, reached)
	}
	if code := runSetupCommand([]string{"--undoo"}, tempDir(t)); code != 2 || reached != 0 {
		t.Errorf("`setup --undoo`: exit %d, elevated path reached %d times; want 2 and 0 -- a typo must not run setup in either direction", code, reached)
	}
	// The control: a well-formed invocation is what reaches it, with the
	// parsed flags.
	if code := runSetupCommand([]string{"--undo", "--all-drives"}, tempDir(t)); code != 0 || reached != 1 || !gotUndo || !gotAll {
		t.Errorf("`setup --undo --all-drives`: exit %d, reached %d, undo=%v allDrives=%v; want 0, 1, true, true", code, reached, gotUndo, gotAll)
	}
}
