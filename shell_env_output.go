package main

import "os"

// stdoutIsTerminal reports whether this command's stdout is a terminal rather
// than a pipe or a file.
//
// A variable so a test can answer for it: the two branches it selects between
// have opposite failure modes, and only one of them can be produced from a test
// harness, whose stdout is always a pipe.
var stdoutIsTerminal = func() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		// Cannot tell. Say "not a terminal", which keeps the output that a shell
		// might be about to evaluate -- losing it would break `eval "$(nvx use)"`,
		// and printing it needlessly only costs noise.
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// shouldPrintShellEnv reports whether `nvx use` should emit the export block.
//
// It exists because the block is 6,000 characters of PATH and a person is not
// the intended reader. `nvx use` prints an environment for a shell to evaluate;
// with the integration loaded that is exactly right, and the text never reaches
// the screen. Without it, a newcomer running the second command they are told to
// run gets a wall of text with the explanation printed underneath it, already
// scrolled past on most terminals.
//
// Two conditions, and both are needed:
//
//   - Nothing is going to evaluate it. The integration exports
//     NVX_SHELL_INTEGRATION, and a --shell argument means nvx was invoked BY the
//     integration; either answers "yes, something will".
//   - Stdout is a terminal. `eval "$(nvx use 22)"` is the documented escape
//     hatch for anyone who has not loaded the integration, and there stdout is a
//     pipe. Suppressing the block on that would break the one workaround the
//     warning itself recommends.
func shouldPrintShellEnv(viaIntegration bool) bool {
	if viaIntegration || os.Getenv("NVX_SHELL_INTEGRATION") != "" {
		return true
	}
	return !stdoutIsTerminal()
}

// shellIntegrationHint is the one line that loads the integration permanently,
// in the syntax of the shell being used.
//
// Named per shell rather than pointing at `nvx env` and leaving the reader to
// work it out. The message this replaces said "see 'nvx env'", which prints an
// integration script -- so the obvious next move is to run it, watch several
// hundred lines go past, and be no better off.
func shellIntegrationHint(shell string) string {
	switch shell {
	case "powershell":
		return `Add to $PROFILE:   nvx env --shell=powershell | Out-String | Invoke-Expression`
	case "zsh":
		return `Add to ~/.zshrc:   eval "$(nvx env --shell=zsh)"`
	default:
		return `Add to ~/.bashrc:  eval "$(nvx env --shell=bash)"`
	}
}

// evalHint is the one-shot equivalent, for someone who wants this shell
// switched now and will decide about the profile later.
func evalHint(shell, version string) string {
	if shell == "powershell" {
		return "nvx use " + version + " | Out-String | Invoke-Expression"
	}
	return `eval "$(nvx use ` + version + `)"`
}
