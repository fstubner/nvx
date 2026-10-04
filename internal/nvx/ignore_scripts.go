package nvx

import (
	"path/filepath"
	"strings"
)

// Where a run turns install scripts off, which the install-script check reads.
//
// Measured 2026-10-04 against main: `npm ci --ignore-scripts` in a project
// depending on chromedriver stopped at the install-script prompt, and
// `npm install esbuild --ignore-scripts` aborted non-interactively with exit 77.
// npm runs none of those scripts under the flag, so the question protected
// nothing and blocked the install.
const (
	scriptsOffByFlag  = "command line"
	scriptsOffByEnv   = "environment"
	scriptsOffByNpmrc = "project .npmrc"
)

// ignoreScriptsSource says where this invocation turns lifecycle scripts off, or
// returns "" when the package manager will run them.
//
// Precedence follows npm: the command line, then npm_config_ignore_scripts, then
// the project's .npmrc. Only an explicit true counts, and an explicit false from
// a higher source beats a true from a lower one.
//
// A contained npm is given none of the npm_config_* variables (see
// sensitiveEnvPrefixes), so the environment counts only for an uncontained run.
// Honouring it for a contained one would skip the check while npm ran the scripts.
//
// bun reads only the flag here: it has no .npmrc setting for this, and nvx does
// not read its own config.
func ignoreScriptsSource(pm string, args []string, contained bool, projectDir string) string {
	pm = strings.ToLower(pm)
	switch pm {
	case "npm", "pnpm", "yarn", "bun":
	default:
		return ""
	}
	if v, ok := ignoreScriptsFlag(args); ok {
		if v {
			return scriptsOffByFlag
		}
		return ""
	}
	if pm == "bun" {
		return ""
	}
	if !contained {
		if v := envCaseInsensitive("npm_config_ignore_scripts"); v != "" {
			if strings.EqualFold(strings.TrimSpace(v), "true") {
				return scriptsOffByEnv
			}
			return ""
		}
	}
	if projectDir != "" {
		if v, ok := readNpmrc(filepath.Join(projectDir, ".npmrc"))["ignore-scripts"]; ok && strings.EqualFold(v, "true") {
			return scriptsOffByNpmrc
		}
	}
	return ""
}

// ignoreScriptsFlag reads --ignore-scripts from a command line. The last one
// wins, as it does for npm. Arguments after a bare `--` belong to a script or
// binary the package manager runs, not to the package manager.
func ignoreScriptsFlag(args []string) (value, found bool) {
	for _, a := range args {
		if a == "--" {
			break
		}
		switch strings.ToLower(a) {
		case "--ignore-scripts", "--ignore-scripts=true":
			value, found = true, true
		case "--ignore-scripts=false", "--no-ignore-scripts":
			value, found = false, true
		}
	}
	return value, found
}
