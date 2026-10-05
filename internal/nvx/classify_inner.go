package nvx

import (
	"path"
	"strings"
)

// packageManagerBehind returns the package-manager command an invocation really
// runs, with the arguments that command receives, when it is spelled through
// another program. Anything else comes back unchanged.
//
// Two spellings reach a package manager without its own name being the
// command. `corepack pnpm add x` runs pnpm inside corepack's own node process,
// and `node "$npm_execpath" install x` runs npm's entry script directly. Both
// were classified by their outer name, corepack (not wrapped at all) and node
// (your own code), so the install inside ran uncontained with no pre-install
// checks.
func packageManagerBehind(cmd string, args []string) (string, []string) {
	// corepack can name corepack.js, which names a package manager.
	for range 3 {
		inner, innerArgs, ok := innerPackageManager(cmd, args)
		if !ok {
			break
		}
		cmd, args = inner, innerArgs
	}
	return cmd, args
}

func innerPackageManager(cmd string, args []string) (string, []string, bool) {
	switch strings.ToLower(cmd) {
	case "corepack":
		// corepack <name>[@<version>] [args...]
		for i, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			name, _, _ := strings.Cut(a, "@")
			return packageManagerNamed(name, args[i+1:])
		}
	case "node":
		if i := nodeScriptIndex(args); i >= 0 {
			return packageManagerEntryScript(args[i], args[i+1:])
		}
	case "bun":
		// bun runs a script file as `bun <file>` or `bun run <file>`.
		for i := 0; i < len(args); i++ {
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			if strings.EqualFold(args[i], "run") {
				continue
			}
			return packageManagerEntryScript(args[i], args[i+1:])
		}
	}
	return "", nil, false
}

// packageManagerNamed maps a package-manager binary name, as corepack accepts
// it, to the command nvx classifies. pnpx is pnpm's spelling of `pnpm dlx`.
func packageManagerNamed(name string, rest []string) (string, []string, bool) {
	tail := append([]string{}, rest...)
	switch strings.ToLower(name) {
	case "npm", "npx", "pnpm", "yarn", "corepack":
		return strings.ToLower(name), tail, true
	case "yarnpkg":
		return "yarn", tail, true
	case "pnpx":
		return "pnpm", append([]string{"dlx"}, tail...), true
	}
	return "", nil, false
}

// packageManagerEntryScript recognises a package manager's own entry script.
//
// The distinctive file names count wherever they are. The generic ones
// (corepack's dist/pnpm.js, yarn's bin/yarn.js) count only inside node_modules,
// so a project's own yarn.js is still the project's code.
func packageManagerEntryScript(script string, rest []string) (string, []string, bool) {
	p := "/" + strings.ToLower(strings.ReplaceAll(script, `\`, "/"))
	base := path.Base(p)
	switch base {
	case "npm-cli.js":
		return packageManagerNamed("npm", rest)
	case "npx-cli.js":
		return packageManagerNamed("npx", rest)
	case "pnpm.cjs":
		return packageManagerNamed("pnpm", rest)
	case "pnpx.cjs":
		return packageManagerNamed("pnpx", rest)
	}
	// Yarn 2 and later run from a checked-in release, .yarn/releases/yarn-4.5.0.cjs.
	if strings.Contains(p, "/.yarn/releases/") ||
		(strings.HasPrefix(base, "yarn-") && len(base) > 5 && base[5] >= '0' && base[5] <= '9') {
		return packageManagerNamed("yarn", rest)
	}
	if strings.Contains(p, "/node_modules/") {
		name := strings.TrimSuffix(strings.TrimSuffix(base, ".js"), ".cjs")
		if name+".js" == base || name+".cjs" == base {
			return packageManagerNamed(name, rest)
		}
	}
	return "", nil, false
}

// nodeScriptIndex returns the index of a package manager's entry script among
// node's arguments, or -1. Node runs the first positional as the script, so the
// scan stops there, reading past a token that may be a flag's value the way
// commandVerbIndex does.
func nodeScriptIndex(args []string) int {
	prevWasFlag := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				if _, _, ok := packageManagerEntryScript(args[i+1], nil); ok {
					return i + 1
				}
			}
			return -1
		}
		switch {
		case a == "-e", a == "--eval", a == "-p", a == "--print", a == "-pe", a == "-c", a == "--check",
			strings.HasPrefix(a, "--eval="), strings.HasPrefix(a, "--print="):
			// Inline code, or a syntax check that runs nothing.
			return -1
		case strings.HasPrefix(a, "-") && a != "-":
			if nodeFlagTakesValue(a) && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				prevWasFlag = false
				continue
			}
			prevWasFlag = !strings.Contains(a, "=")
			continue
		}
		if _, _, ok := packageManagerEntryScript(a, nil); ok {
			return i
		}
		if !prevWasFlag {
			return -1
		}
		prevWasFlag = false
	}
	return -1
}

// nodeFlagTakesValue lists node options that take their value as the next
// token. Not exhaustive, and it does not need to be: a missing entry only makes
// nodeScriptIndex look one token further.
func nodeFlagTakesValue(arg string) bool {
	switch arg {
	case "-r", "--require", "--import", "--loader", "--experimental-loader", "-C", "--conditions",
		"--input-type", "--title", "--env-file", "--env-file-if-exists", "--inspect-port",
		"--openssl-config", "--icu-data-dir", "--redirect-warnings", "--diagnostic-dir",
		"--report-dir", "--report-directory", "--report-filename", "--watch-path":
		return true
	}
	return false
}
