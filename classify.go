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
//
// Only the commands nvx actually shims. `uvx` and `pyx` were listed here, and
// `uv`/`deno` had their own branches below, left behind when the Deno, Go and
// Python providers were removed. Neither name is in any provider's
// ShimCommands, so nvx never saw those invocations and the code could not run --
// it read as support for runtimes this build does not manage. The full
// implementation is preserved on feature/polyglot-runtimes; if it returns, it
// brings its own classification with it.
var executorCommands = map[string]bool{
	"npx": true, "bunx": true,
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
// subcommand: the non-flag tokens, up to the point where nothing further is
// nvx's to interpret.
//
// That point is a script-running verb -- `run <script>`: the script's name is
// not a subcommand and neither is anything after it; without this a project
// with a script called "update", "create" or "rebuild" had `npm run <that>`
// silently sandboxed, measured 2026-08-28 -- or a "--" that follows the
// command.
//
// A "--" BEFORE the command is a different thing. It ends the package
// manager's flag parsing only, and the first token after it is the command:
// measured, `npm -- view left-pad version` prints the version. The scan used
// to stop at every "--", so `npm -- exec x` and `npm -- create x` read as your
// own code. Everything after such a "--" is positional, dashes included, and
// all of it is returned, because hasAuditFix needs the second token too.
//
// "Follows the command" is judged the way findInstallVerbIndex judges it: a
// positional not immediately preceded by a flag is the command; one that is
// might be the flag's value, and a "--" after it is read the cautious way.
func subcommandCandidates(args []string) []string {
	var out []string
	commandSeen, prevWasFlag := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if isRunScriptVerb(a) {
			return out
		}
		if a == "--" {
			if commandSeen {
				return out
			}
			return append(out, args[i+1:]...)
		}
		if strings.HasPrefix(a, "-") {
			// A flag known to take a value carries it in the next token, which is
			// then neither a candidate nor the command. This is what nonFlagTokens
			// did, and dropping it made bare `npm init --registry X` read as an
			// initializer fetch because "X" followed "init".
			if flagTakesValue(a) && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				prevWasFlag = false
				continue
			}
			prevWasFlag = !strings.Contains(a, "=")
			continue
		}
		out = append(out, a)
		if !prevWasFlag {
			commandSeen = true
		}
		prevWasFlag = false
	}
	return out
}

// isRunScriptVerb reports the package-manager subcommands after which nothing
// further is nvx's to read.
//
// `run <script> [args]` (npm also spells it `run-script`; pnpm, yarn and bun
// use `run`), and `test`, `start`, `stop` and `restart`, which name no script
// but hand everything after "--" to one. Measured: `npm test -- install` ran
// the test script with argv ["install"]. The second group was absent while the
// scans stopped at every "--"; once they learned to look past it, `npm test --
// install` would have read as an install without this.
func isRunScriptVerb(arg string) bool {
	switch strings.ToLower(arg) {
	case "run", "run-script", "test", "start", "stop", "restart":
		return true
	}
	return false
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
// bun) can be your-code, install, or (for npx/bunx) an ad-hoc tool
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
	default:
		// node, python, and any other direct runtime invocation runs the
		// script you asked it to run — that is your code by definition.
		return classYourCode
	}
}
