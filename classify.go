package main

import "strings"

// invocationClass is the three-way split the containment model bases its
// contain/don't-contain decision on: code you wrote and are running, a
// package-manager install (untrusted code arriving on disk), or an ad-hoc
// third-party tool invocation (untrusted code you didn't install yourself).
type invocationClass int

const (
	classYourCode invocationClass = iota
	classInstall
	classAdHocTool
)

// String names the class for humans. Used in run traces, where "why was this not
// contained" is answered with the class and level that decided it.
func (c invocationClass) String() string {
	switch c {
	case classInstall:
		return "install"
	case classAdHocTool:
		return "ad-hoc tool"
	default:
		return "your code"
	}
}

// executorCommands are ad-hoc tool runners: they fetch and execute a package
// that was not explicitly installed into the project, so every invocation is
// untrusted-code-by-default regardless of subcommand.
var executorCommands = map[string]bool{
	"npx": true, "bunx": true, "uvx": true, "pyx": true,
}

// classifyInvocation determines which containment class a wrapped command
// invocation falls into. It is subcommand-aware: the same command name (npm,
// bun, uv) can be your-code, install, or (for npx/bunx/uvx/pyx) an ad-hoc tool
// runner, depending on whether an install-style verb appears anywhere in its
// arguments (see hasInstallVerb) — not just whether the first non-flag
// argument happens to be one, since a preceding value-taking flag this
// classifier doesn't recognize would otherwise let an install slip through
// uncontained.
func classifyInvocation(cmd string, args []string) invocationClass {
	lower := strings.ToLower(cmd)

	if executorCommands[lower] {
		return classAdHocTool
	}

	switch lower {
	case "npm", "yarn", "pnpm":
		if hasInstallVerb(args, "ci") {
			return classInstall
		}
		return classYourCode
	case "bun":
		if hasInstallVerb(args, "a") {
			return classInstall
		}
		return classYourCode
	case "uv":
		if hasInstallVerb(args, "pip") {
			return classInstall
		}
		return classYourCode
	case "deno":
		if hasInstallVerb(args) {
			return classInstall
		}
		return classYourCode
	default:
		// node, python, and any other direct runtime invocation runs the
		// script you asked it to run — that is your code by definition.
		return classYourCode
	}
}
