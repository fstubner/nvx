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

// executorCommands are ad-hoc tool runners: they fetch and execute a package
// that was not explicitly installed into the project, so every invocation is
// untrusted-code-by-default regardless of subcommand.
var executorCommands = map[string]bool{
	"npx": true, "bunx": true, "uvx": true, "pyx": true,
}

// classifyInvocation determines which containment class a wrapped command
// invocation falls into. It is subcommand-aware: the same command name (npm,
// bun, uv) can be your-code, install, or (for npx/bunx/uvx/pyx) an ad-hoc tool
// runner, depending on its first non-flag argument.
func classifyInvocation(cmd string, args []string) invocationClass {
	lower := strings.ToLower(cmd)

	if executorCommands[lower] {
		return classAdHocTool
	}

	sub := firstNonFlagArg(args)

	switch lower {
	case "npm", "yarn", "pnpm":
		if sub == "ci" || installAliases[sub] {
			return classInstall
		}
		return classYourCode
	case "bun":
		if sub == "install" || sub == "add" || sub == "a" || installAliases[sub] {
			return classInstall
		}
		return classYourCode
	case "uv":
		if sub == "add" || sub == "pip" || installAliases[sub] {
			return classInstall
		}
		return classYourCode
	case "deno":
		if sub == "add" || sub == "install" {
			return classInstall
		}
		return classYourCode
	default:
		// node, python, and any other direct runtime invocation runs the
		// script you asked it to run — that is your code by definition.
		return classYourCode
	}
}
