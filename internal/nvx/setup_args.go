package nvx

import (
	"fmt"
	"os"
)

// setupHelpText is what `nvx setup --help` and `nvx help setup` print.
const setupHelpText = `nvx setup

(Windows) Remove what older nvx versions left on the machine: sandbox read and
list access on drive roots and Users folders, grants made to an older sandbox
identity, and the loopback exemption older versions registered. nvx no longer
adds any of these. Run it from an Administrator terminal to remove them. With
nothing to remove it says so and exits 0.

--undo, -u     Accepted, and does the same thing.
--all-drives   Accepted for older scripts. It changes nothing.
`

type setupArgs struct {
	help bool
}

// parseSetupArgs reads setup's arguments and refuses any it does not know.
//
// It used to scan for --undo and --all-drives and ignore everything else, so
// `nvx setup --help` ran setup, and `nvx setup --undoo` ran it forward --
// granting -- when the person typing it meant to take the grant back. Setup
// only removes things now, but it is still the one command that changes ACLs on
// the machine's drive roots, from an Administrator terminal, which is the worst
// place for "unrecognised means proceed". Measured on the build that granted:
// `nvx setup --help` reached the elevation check.
//
// --undo and --all-drives are still accepted so scripts and habits written for
// an older nvx keep working. Setup does the same thing with or without them.
func parseSetupArgs(args []string) (setupArgs, error) {
	var out setupArgs
	for _, a := range args {
		switch a {
		case "--undo", "-u", "--all-drives":
			// Setup removes what an older one left, which is what --undo asked for.
			// --all-drives widened a grant that no longer exists.
		case "--help", "-h":
			out.help = true
		default:
			return setupArgs{}, fmt.Errorf("nvx setup: unknown argument %q", a)
		}
	}
	return out, nil
}

// runSetupImpl is what a well-formed `nvx setup` invocation runs. A variable so
// a test can record whether the removal was reached without reaching it: on a
// CI runner that IS elevated, calling the real thing runs setup against the
// runner's drive roots.
var runSetupImpl = runWindowsSetup

// runSetupCommand is the `nvx setup` entry point: help and errors never reach
// the removal.
func runSetupCommand(args []string, nvxHome string) int {
	parsed, err := parseSetupArgs(args)
	if err != nil {
		LogError("%v", err)
		fmt.Fprint(os.Stderr, setupHelpText)
		return 2
	}
	if parsed.help {
		fmt.Print(setupHelpText)
		return 0
	}
	return runSetupImpl(nvxHome)
}
