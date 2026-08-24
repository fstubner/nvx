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

// executorVerbs are the same operation as npx, spelled as a subcommand.
//
// `npm exec pkg`, `pnpm dlx pkg` and `bun x pkg` fetch a package that was never
// installed into the project and run it. Only the `npx` spelling was recognised,
// so the identical operation under any other name ran uncontained, unscanned and
// untyposquat-checked -- measured 2026-08-24: `nvx npm exec cowsay hi` fetched
// and executed cowsay with no containment, while `nvx npx cowsay hi` was
// contained.
//
// `create` and `init <initializer>` belong here for the same reason: both fetch
// a create-* package from the registry and execute it. `npm create vite` is how
// a large share of projects begin.
var executorVerbs = map[string][]string{
	"npm":  {"exec", "create"},
	"pnpm": {"dlx", "create"},
	"yarn": {"dlx", "create"},
	"bun":  {"x", "create"},
}

// refreshVerbs re-fetch dependencies or re-run the install scripts of ones
// already present. They execute package-authored code exactly as an install
// does, and were classified as your-own-code.
//
// `npm rebuild` re-runs every dependency's install scripts. `npm update` fetches
// new versions and runs theirs. `npm audit fix` -- the command a developer runs
// *because* of a security advisory -- installs new versions to do it.
var refreshVerbs = []string{"update", "up", "upgrade", "rebuild", "dedupe", "ddp"}

// subcommandCandidates returns the tokens that could be this invocation's
// subcommand: nonFlagTokens, truncated at the "--" passthrough separator.
//
// The truncation is what stops `npm run build -- create` reading as an
// initializer fetch. Everything after "--" belongs to the script being run, and
// a word there is not nvx's to interpret -- the same rule parseShimOptions
// follows for flags.
func subcommandCandidates(args []string) []string {
	for i, a := range args {
		if a == "--" {
			args = args[:i]
			break
		}
	}
	return nonFlagTokens(args)
}

// hasExecutorVerb reports whether this invocation fetches and runs a package
// that is not part of the project.
func hasExecutorVerb(cmd string, args []string) bool {
	verbs, ok := executorVerbs[cmd]
	if !ok {
		return false
	}
	tokens := subcommandCandidates(args)
	for i, tok := range tokens {
		lower := strings.ToLower(tok)
		for _, v := range verbs {
			if lower == v {
				return true
			}
		}
		// `init` only fetches when it is given an initializer: bare `npm init`
		// writes a package.json and runs nothing, so containing it would be noise.
		if lower == "init" && i+1 < len(tokens) {
			return true
		}
	}
	return false
}

// hasAuditFix reports the two-token `audit fix`, which installs. Plain `npm
// audit` only reads, so the verb alone must not count.
func hasAuditFix(args []string) bool {
	tokens := subcommandCandidates(args)
	for i := 0; i+1 < len(tokens); i++ {
		if strings.EqualFold(tokens[i], "audit") && strings.EqualFold(tokens[i+1], "fix") {
			return true
		}
	}
	return false
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
	// The same fetch-and-run operation spelled as a subcommand (npm exec,
	// pnpm dlx, bun x, npm create). Checked before the install verbs because it
	// is the stronger classification and some spellings overlap.
	if hasExecutorVerb(lower, args) {
		return classAdHocTool
	}

	switch lower {
	case "npm", "yarn", "pnpm":
		if hasInstallVerb(args, append([]string{"ci"}, refreshVerbs...)...) || hasAuditFix(args) {
			return classInstall
		}
		return classYourCode
	case "bun":
		if hasInstallVerb(args, append([]string{"a"}, refreshVerbs...)...) {
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
