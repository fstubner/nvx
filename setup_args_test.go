package main

import (
	"runtime"
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
// error. On Windows, unelevated, reaching it is an exit of 1 with "must run
// from an elevated terminal"; help exits 0 and an error exits 2, and neither
// is that.
func TestSetupCommandDoesNotAttemptSetupForHelpOrABadArgument(t *testing.T) {
	if code := runSetupCommand([]string{"--help"}, tempDir(t)); code != 0 {
		t.Errorf("`setup --help` exited %d, want 0: it attempted setup instead of printing help", code)
	}
	if code := runSetupCommand([]string{"--undoo"}, tempDir(t)); code != 2 {
		t.Errorf("`setup --undoo` exited %d, want 2: a typo must not run setup in either direction", code)
	}
	if runtime.GOOS == "windows" {
		// The control: a well-formed invocation does reach the elevation check,
		// which an unelevated test process fails. This is what distinguishes
		// "printed help" from "ran and happened to exit 0".
		if code := runSetupCommand([]string{"--undo"}, tempDir(t)); code != 1 {
			t.Errorf("`setup --undo` unelevated exited %d, want 1 (the elevation check)", code)
		}
	}
}
