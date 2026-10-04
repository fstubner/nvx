package nvx

import (
	"fmt"
	"os"
)

// setupHelpText is what `nvx setup --help` and `nvx help setup` print.
const setupHelpText = `nvx setup [--undo]

(Windows, Administrator) Grant the sandbox stat access to the root of every
fixed volume. Optional: installs and npx do not need it; only a tool that
resolves a path all the way up to a drive root does, and nvx names this
command after such a failure. Also removes a loopback exemption an older
nvx left.

--undo, -u     Reverse what setup granted.
--all-drives   Accepted for older scripts. Setup covers every fixed volume.
`

type setupArgs struct {
	undo bool
	help bool
}

// parseSetupArgs reads setup's arguments and refuses any it does not know.
//
// It used to scan for --undo and --all-drives and ignore everything else, so
// `nvx setup --help` ran setup, and `nvx setup --undoo` ran it forward --
// granting -- when the person typing it meant to take the grant back. Setup
// is the one command that changes ACLs on the machine's drive roots, from an
// Administrator terminal, which is the worst place for "unrecognised means
// proceed". Measured on the installed build: `nvx setup --help` reached the
// elevation check.
func parseSetupArgs(args []string) (setupArgs, error) {
	var out setupArgs
	for _, a := range args {
		switch a {
		case "--undo", "-u":
			out.undo = true
		case "--all-drives":
			// What setup does anyway since 2026-10-04. Still accepted, so a
			// script written for an older nvx keeps working.
		case "--help", "-h":
			out.help = true
		default:
			return setupArgs{}, fmt.Errorf("nvx setup: unknown argument %q", a)
		}
	}
	return out, nil
}

// runSetupImpl is what a well-formed `nvx setup` invocation runs. A variable so
// a test can record whether the elevated path was reached without reaching
// it: on a CI runner that IS elevated, calling the real thing runs setup
// against the runner's drive roots.
var runSetupImpl = runWindowsSetup

// runSetupCommand is the `nvx setup` entry point: help and errors never reach
// the elevated path.
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
	return runSetupImpl(nvxHome, parsed.undo)
}
