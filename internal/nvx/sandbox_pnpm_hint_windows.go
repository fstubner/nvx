//go:build windows

package nvx

import "strings"

// pnpm fails in the Windows sandbox in two ways that do not mention the sandbox.
//
// pnpm 12 reads its --dir argument by asking Windows for the full path of a
// folder, which the sandbox refuses, and stops with "canonicalizing the `--dir`
// argument ... Access is denied. (os error 5)". Measured 2026-10-07, every pnpm
// 12 command did, including `pnpm --version`, and it is what `npm install -g
// pnpm` and corepack install without a pin.
//
// pnpm 9, 10 and 11 stop with a Rust panic, "Failed to get source volume info:
// ... Access is denied.", exit 127, when an install includes a package that has
// install scripts. Measured the same day with 9.15.9, 10.34.6 and 11.28.5 on
// bufferutil 4.1.0. Such a package is copied into node_modules with a native
// copy-on-write call whatever package-import-method says, and the call asks
// Windows about the drive's root folder, which the sandbox cannot open.
// `--ignore-scripts`, `package-import-method=copy` and `side-effects-cache=false`
// each left the panic as it was. A workspace is not the cause. The first report
// had one whose root was named ws at version 1.0.0, and pnpm 10 has a built-in
// extension for ws below 7.2.1 that adds bufferutil and utf-8-validate to it.
//
// Neither run says what to do, and nvx cannot read what the command printed, so
// the note is worded as two possible causes, as noteBunOffSystemDrive's is.

// pnpmFailedContained reports whether a command that exited with exitCode was a
// pnpm run, whether typed as pnpm, through corepack or as pnpm's entry script.
func pnpmFailedContained(command string, args []string, exitCode int) bool {
	if exitCode == 0 {
		return false
	}
	pm, _ := packageManagerBehind(command, args)
	return strings.EqualFold(pm, "pnpm")
}

// notePnpmLimits prints the explanation after pnpm's own output, once per launch.
func notePnpmLimits(command string, args []string, exitCode int) {
	if !pnpmFailedContained(command, args, exitCode) {
		return
	}
	LogWarn("pnpm stopped inside the Windows sandbox. If the error above is pnpm 12's \"Access is denied. (os error 5)\" as it reads its --dir argument, or a panic that names \"Failed to get source volume info\" (pnpm 9 to 11, for an install with a package that has install scripts), it is a limit of the sandbox and not of your project.")
	LogWarn("Run it with `nvx --no-sandbox pnpm ...`, which runs it uncontained. Known limitations lists the other ways round.")
}
