package nvx

import (
	"testing"
)

// `nvx setup` runs only the arguments it understands.
//
// The setup command scanned its arguments for `--undo` and `--all-drives`
// and ignored everything else, so `nvx setup --help` ran setup, and so did
// `nvx setup --undoo` -- forward, granting, when the person typing it meant
// to take the grant back. Setup only removes entries now, but it is still the
// one command that changes ACLs on the machine's drive roots and needs an
// Administrator terminal to do it, which is the worst place for "unrecognised
// means proceed". --undo and --all-drives stay accepted, and change nothing.
func TestSetupRefusesArgumentsItDoesNotUnderstand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		help    bool
		wantErr bool
	}{
		{"no arguments", nil, false, false},
		{"--undo is still accepted", []string{"--undo"}, false, false},
		{"-u is still accepted", []string{"-u"}, false, false},
		{"--all-drives is still accepted", []string{"--all-drives"}, false, false},
		{"both", []string{"--undo", "--all-drives"}, false, false},
		{"--help asks for help, not setup", []string{"--help"}, true, false},
		{"-h asks for help, not setup", []string{"-h"}, true, false},
		{"help after a real flag still asks for help", []string{"--undo", "--help"}, true, false},
		{"a typo of --undo is an error", []string{"--undoo"}, false, true},
		{"an unknown flag is an error", []string{"--yes"}, false, true},
		{"a stray word is an error", []string{"now"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSetupArgs(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseSetupArgs(%v) error = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if got.help != tc.help {
				t.Errorf("parseSetupArgs(%v) = %+v, want help=%v", tc.args, got, tc.help)
			}
		})
	}
}

// And the command itself does not reach the removal for help or for an error,
// while a well-formed invocation does, whichever of the accepted flags it carries.
//
// The removal is recorded, never run. A first version of this test called the
// real thing for the control case, reasoning that an unelevated test process
// would be stopped at the elevation check. GitHub's Windows runners are
// elevated, so on CI it ran `nvx setup --undo` against the runner's drive roots
// for several minutes.
func TestSetupCommandDoesNotAttemptSetupForHelpOrABadArgument(t *testing.T) {
	orig := runSetupImpl
	t.Cleanup(func() { runSetupImpl = orig })
	reached := 0
	runSetupImpl = func(string) int {
		reached++
		return 0
	}

	if code := runSetupCommand([]string{"--help"}, tempDir(t)); code != 0 || reached != 0 {
		t.Errorf("`setup --help`: exit %d, removal reached %d times; want 0 and 0", code, reached)
	}
	if code := runSetupCommand([]string{"--undoo"}, tempDir(t)); code != 2 || reached != 0 {
		t.Errorf("`setup --undoo`: exit %d, removal reached %d times; want 2 and 0 -- a typo must not run setup", code, reached)
	}
	// The control: a well-formed invocation is what reaches it.
	for i, args := range [][]string{nil, {"--undo"}, {"--undo", "--all-drives"}} {
		if code := runSetupCommand(args, tempDir(t)); code != 0 || reached != i+1 {
			t.Errorf("`setup %v`: exit %d, reached %d; want 0 and %d", args, code, reached, i+1)
		}
	}
}
