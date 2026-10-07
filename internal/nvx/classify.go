package nvx

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
	// classUnknownCommand is an npm command nvx does not know to be safe. It is
	// contained, because the npm commands nvx did not know ran uncontained
	// with no checks. See npmCommandLine.
	classUnknownCommand
)

// String names the class for humans. Used in run traces, where "why was this not
// contained" is answered with the class and level that decided it.
func (c invocationClass) String() string {
	switch c {
	case classInstall:
		return "install"
	case classAdHocTool:
		return "ad-hoc tool"
	case classUnknownCommand:
		return "unrecognised npm command"
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
	"npm":  {"exec", "x", "create"},
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
//
// rb (rebuild) and udpate (npm's typo alias for update) were missing until
// 2026-09-26.
var refreshVerbs = []string{"update", "up", "upgrade", "udpate", "rebuild", "rb", "dedupe", "ddp"}

// installCommandVerbs are further subcommands, per package manager, that fetch
// dependencies or run their lifecycle scripts. Unlike the lists above they are
// matched only where the package manager reads its command (commandVerbIndex),
// because several also name harmless sub-subcommands such as `npm config
// edit`, `pnpm store prune` and `bun pm cache rm`.
//
// Each of these ran as your own code until 2026-10-01. Measured that day with a
// dependency whose postinstall writes a marker, from a project holding a
// lockfile and no node_modules. npm 11.19.0 uninstall, unlink and prune, pnpm
// 8.3.1 remove, prune and fetch, and bun 1.4.2 remove and patch all ran it.
// The removal verbs re-install whatever the lockfile lists that is missing.
// `npm edit` runs `npm rebuild` on the package once the editor exits. The
// rest come from each tool's help without a measurement, and are contained to
// be safe. `pnpm approve-builds` runs the builds it approves.
//
// The aliases added 2026-10-07 were read from each tool's source or help. They
// are pnpm's uni (remove) and dislink (unlink), in pnpm 8.15 to 12.9, yarn 1's
// upgradeInteractive, its command table's own key, and bun 1.4's r, uninstall
// and ci, the last an install. `pnpm edit` hands the command to npm, which
// rebuilds the package, and npm 12's `patch` reinstalls after commit and rm.
var installCommandVerbs = map[string][]string{
	"npm":  {"uninstall", "unlink", "remove", "rm", "r", "un", "prune", "edit", "patch"},
	"pnpm": {"remove", "rm", "uninstall", "un", "uni", "unlink", "dislink", "prune", "fetch", "deploy", "approve-builds", "patch-commit", "patch-remove", "self-update", "edit"},
	"yarn": {"remove", "unlink", "unplug", "upgrade-interactive", "upgradeInteractive", "patch", "patch-commit"},
	"bun":  {"remove", "rm", "r", "uninstall", "ci", "patch", "patch-commit"},
	// `corepack use` and `corepack up` switch the project's package manager and
	// then run its install.
	"corepack": {"use", "up"},
}

// installCommandPairs are install verbs spelled as two words. `bun pm trust` is
// how bun runs the lifecycle scripts it blocked during install, so it is the
// one command whose whole purpose is running dependency code. pnpm 11's
// `runtime set` (or `rt set`) installs a runtime and then the project.
var installCommandPairs = map[string][][2]string{
	"pnpm": {{"env", "use"}, {"env", "add"}, {"runtime", "set"}, {"rt", "set"}},
	"yarn": {{"workspaces", "focus"}, {"set", "version"}, {"policies", "set-version"}, {"plugin", "import"}},
	"bun":  {{"pm", "trust"}},
}

// linkVerbs install only when given something to link. Bare `npm link` runs the
// current package's own scripts and nothing else (measured). `npm link <dir>`
// ran that directory's dependency scripts, and `bun link <name>` ran an
// install of the project.
var linkVerbs = map[string][]string{
	"npm":  {"link", "ln"},
	"pnpm": {"link", "ln"},
	"yarn": {"link"},
	"bun":  {"link"},
}

// adHocCommandVerbs are executorVerbs matched only in the command position.
// `c` is bun's alias for create, and a single letter is too likely to be some
// other command's argument to look for it anywhere.
var adHocCommandVerbs = map[string][]string{
	"bun": {"c"},
}

// hasInstallCommandVerb reports whether args is one of tool's install verbs
// from installCommandVerbs, installCommandPairs or linkVerbs.
func hasInstallCommandVerb(tool string, args []string) bool {
	if commandVerbIndex(args, installCommandVerbs[tool]...) >= 0 {
		return true
	}
	if i := commandVerbIndex(args, linkVerbs[tool]...); i >= 0 && nextPositional(args, i) != "" {
		return true
	}
	for _, pair := range installCommandPairs[tool] {
		if hasCommandPair(args, pair[0], pair[1]) {
			return true
		}
	}
	return false
}

// hasCommandPair reports a two-word command such as `bun pm trust`.
func hasCommandPair(args []string, first, second string) bool {
	i := commandVerbIndex(args, first)
	return i >= 0 && strings.EqualFold(nextPositional(args, i), second)
}

// commandVerbIndex returns the index of the token in args that is one of verbs
// and sits where the package manager reads its command, or -1.
//
// That is the first positional. A flag right before it may have taken it as
// its value, and then the next positional may be the command too and is
// checked as well, so an unrecognised value-taking flag cannot hide a verb.
// The scan stops at the first positional that is certainly the command, and at
// a script-running verb.
func commandVerbIndex(args []string, verbs ...string) int {
	if len(verbs) == 0 {
		return -1
	}
	isVerb := func(tok string) bool {
		for _, v := range verbs {
			if strings.EqualFold(tok, v) {
				return true
			}
		}
		return false
	}
	prevWasFlag := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) && isVerb(args[i+1]) {
				return i + 1
			}
			return -1
		}
		if isRunScriptVerb(a) {
			return -1
		}
		if strings.HasPrefix(a, "-") {
			if flagTakesValue(a) && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				prevWasFlag = false
				continue
			}
			prevWasFlag = !strings.Contains(a, "=")
			continue
		}
		if isVerb(a) {
			return i
		}
		if !prevWasFlag {
			return -1
		}
		prevWasFlag = false
	}
	return -1
}

// nextPositional returns the first non-flag token after args[i], or "".
func nextPositional(args []string, i int) string {
	for j := i + 1; j < len(args); j++ {
		a := args[j]
		if a == "--" {
			if j+1 < len(args) {
				return args[j+1]
			}
			return ""
		}
		if strings.HasPrefix(a, "-") {
			if flagTakesValue(a) && !strings.Contains(a, "=") {
				j++
			}
			continue
		}
		return a
	}
	return ""
}

// fetchesRemoteSource reports an npm command that fetches a git or URL spec in
// order to read or pack it. npm prepares a git dependency before packing it,
// which means installing its dependencies and running its prepare script, so
// `npm pack github:user/repo` runs that repository's code. A registry name or a
// local directory does not reach here: a registry tarball carries no scripts to
// run, and packing your own directory runs your own.
//
// `npm diff` takes its specs from --diff and its positionals are path filters,
// so for diff only the --diff values are read.
func fetchesRemoteSource(args []string) bool {
	i := commandVerbIndex(args, "pack", "diff", "view", "info", "show", "v", "cache")
	if i < 0 {
		return false
	}
	isDiff := strings.EqualFold(args[i], "diff")
	rest := args[i+1:]
	for j := 0; j < len(rest); j++ {
		a := rest[j]
		switch {
		case a == "--diff" && j+1 < len(rest):
			j++
			a = rest[j]
		case strings.HasPrefix(a, "--diff="):
			a = strings.TrimPrefix(a, "--diff=")
		case strings.HasPrefix(a, "-") || isDiff:
			continue
		}
		if isRemoteSourceSpec(a) {
			return true
		}
	}
	return false
}

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

// isBareYarnInstall reports `yarn` with no subcommand, which is an install:
// yarn's default command. `yarn` and `yarn --frozen-lockfile` ran as your own
// code until 2026-09-26, with no sandbox and no pre-install checks. Asking only
// for its version or help installs nothing.
func isBareYarnInstall(cmd string, args []string) bool {
	if !strings.EqualFold(cmd, "yarn") || len(subcommandCandidates(args)) > 0 {
		return false
	}
	for _, a := range args {
		// subcommandCandidates stops at a script verb and returns nothing, which
		// would otherwise read `yarn run build` as bare yarn.
		if isRunScriptVerb(a) {
			return false
		}
		switch strings.ToLower(a) {
		case "--version", "-v", "--help", "-h":
			return false
		}
	}
	return true
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
	// `corepack pnpm add x` and `node .../npm-cli.js install x` are classified
	// as the package-manager command they run, and `npm exe` as `npm exec`.
	cmd, args = packageManagerBehind(cmd, args)
	lower := strings.ToLower(cmd)
	args, knownSafe := readCommand(lower, args)

	if executorCommands[lower] {
		return classAdHocTool
	}
	// The same fetch-and-run operation spelled as a subcommand (npm exec,
	// pnpm dlx, bun x, npm create). Checked before the install verbs because it
	// is the stronger classification and some spellings overlap.
	if hasExecutorVerb(lower, args) || commandVerbIndex(args, adHocCommandVerbs[lower]...) >= 0 {
		return classAdHocTool
	}
	if lower == "npm" && fetchesRemoteSource(args) {
		return classAdHocTool
	}

	switch lower {
	case "npm", "yarn", "pnpm":
		if hasInstallVerb(args, append(append([]string{}, ciVerbs...), refreshVerbs...)...) ||
			hasAuditFix(args) || isBareYarnInstall(lower, args) || hasInstallCommandVerb(lower, args) {
			return classInstall
		}
		if !knownSafe {
			return classUnknownCommand
		}
		return classYourCode
	case "bun":
		if hasInstallVerb(args, append([]string{"a"}, refreshVerbs...)...) || hasInstallCommandVerb(lower, args) {
			return classInstall
		}
		return classYourCode
	case "corepack":
		if hasInstallCommandVerb(lower, args) {
			return classInstall
		}
		return classYourCode
	default:
		// node, python, and any other direct runtime invocation runs the
		// script you asked it to run — that is your code by definition.
		return classYourCode
	}
}
