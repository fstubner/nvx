package nvx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	// Yarn 2 and later only.
	scriptsOffByYarnMode = "--mode=skip-build"
	scriptsOffByYarnrc   = "project .yarnrc.yml"
)

// ignoreScriptsSource says where this invocation turns lifecycle scripts off, or
// returns "" when the package manager will run them. Each package manager is
// asked only about the settings it reads.
//
// For npm and pnpm the precedence is npm's. The command line comes first,
// then npm_config_ignore_scripts, then the project's .npmrc. Only an explicit true
// counts, and an explicit false from a higher source beats a true from a lower
// one. A contained run is given none of the npm_config_* variables (see
// sensitiveEnvPrefixes), so the environment counts only for an uncontained run.
// Honouring it for a contained one would skip the check while npm ran the
// scripts.
//
// yarn reads neither the environment variable nor .npmrc, and the project's
// .npmrc used to count for it. Measured 2026-10-07 in a container, with a
// dependency whose postinstall writes a marker. Under a project .npmrc with
// ignore-scripts=true, yarn 1.22.22, 2.4.3 and 3.8.7 ran the postinstall. Under
// npm_config_ignore_scripts=true so did those three and 4.18.1. See
// yarnScriptsOff for what each yarn does read.
//
// bun reads only the flag here. It has no .npmrc setting for this, and nvx does
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
	switch pm {
	case "bun":
		return ""
	case "yarn":
		return yarnScriptsOff(args, projectDir)
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

// yarnScriptsOff says where a yarn install turns dependency scripts off, past
// the --ignore-scripts flag, or returns "".
//
// Measured 2026-10-07 in a container, with a dependency whose postinstall writes
// a marker. yarn 1.22.22 ran no scripts under --ignore-scripts. Yarn 2.4.3, 3.8.7
// and 4.18.1 refuse that flag and run nothing, so it counts for every yarn and
// ignoreScriptsSource reads it first. Yarn 3.8.7 and 4.18.1 ran none under
// --mode=skip-build, and 2.4.3 refuses --mode. Yarn 2 and later ran none under
// enableScripts: false in .yarnrc.yml. yarn 1 ran the scripts under both, so they
// count only where a later yarn is the one that runs (see yarnRunsBerry).
func yarnScriptsOff(args []string, projectDir string) string {
	if projectDir == "" || !yarnRunsBerry(projectDir) {
		return ""
	}
	if yarnModeSkipBuild(args) {
		return scriptsOffByYarnMode
	}
	// Measured the same way, a package named in dependenciesMeta with
	// built: true ran its postinstall under enableScripts: false, with 3.8.7
	// and 4.18.1. So did enableScripts: false when YARN_ENABLE_SCRIPTS=true was
	// set, because the variable wins. YARN_RC_FILENAME names another file.
	if os.Getenv("YARN_RC_FILENAME") != "" {
		return ""
	}
	if v := strings.TrimSpace(os.Getenv("YARN_ENABLE_SCRIPTS")); v != "" && v != "false" && v != "0" {
		return ""
	}
	if yarnrcSetting(projectDir, "enableScripts") != "false" || manifestBuildsADependency(projectDir) {
		return ""
	}
	return scriptsOffByYarnrc
}

// yarnRunsBerry reports whether the yarn that runs in projectDir is Yarn 2 or
// later.
//
// A yarnPath in .yarnrc.yml is the release every yarn hands the run to, yarn
// 1.22 included, and that release is the project's own code whatever it says.
// A packageManager field naming yarn 2 or later is what corepack runs, and
// measured 2026-10-07, yarn 1.22.22 refused to run in such a project, exit 1.
// Anything else may be yarn 1, which ignores .yarnrc.yml and --mode.
func yarnRunsBerry(projectDir string) bool {
	if yarnrcSetting(projectDir, "yarnPath") != "" {
		return true
	}
	var m struct {
		PackageManager string `json:"packageManager"`
	}
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return false
	}
	version, ok := strings.CutPrefix(strings.TrimSpace(m.PackageManager), "yarn@")
	if !ok {
		return false
	}
	major, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= 2
}

// yarnModeSkipBuild reports --mode=skip-build, or --mode skip-build, before
// any "--".
func yarnModeSkipBuild(args []string) bool {
	for i, a := range args {
		if a == "--" {
			return false
		}
		if a == "--mode=skip-build" || a == "--mode" && i+1 < len(args) && args[i+1] == "skip-build" {
			return true
		}
	}
	return false
}

// yarnrcSetting reads a top-level setting from the nearest .yarnrc.yml that
// sets it, from the working directory up to projectDir, which is the one yarn
// uses. Measured with 3.8.7 and 4.18.1, the nearer file wins. A file nvx cannot
// parse is read as not setting it.
func yarnrcSetting(projectDir, key string) string {
	for _, dir := range yarnrcDirs(projectDir) {
		data, err := os.ReadFile(filepath.Join(dir, ".yarnrc.yml"))
		if err != nil {
			continue
		}
		if docs, err := parseYAMLDocuments(data); err == nil && len(docs) > 0 {
			if v := docs[0].get(key); v != nil {
				return v.str()
			}
		}
	}
	return ""
}

// yarnrcDirs lists the working directory and each parent of it up to
// projectDir, nearest first, or only projectDir when the working directory is
// not inside it.
func yarnrcDirs(projectDir string) []string {
	root := filepath.Clean(projectDir)
	cwd, err := os.Getwd()
	if err != nil {
		return []string{root}
	}
	var dirs []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if rel, err := filepath.Rel(root, dir); err == nil && rel == "." {
			return dirs
		}
		if filepath.Dir(dir) == dir {
			return []string{root}
		}
	}
}

// manifestBuildsADependency reports a dependenciesMeta entry in package.json
// with built: true, which Yarn 2 and later build even under enableScripts:
// false.
func manifestBuildsADependency(projectDir string) bool {
	var m struct {
		DependenciesMeta map[string]struct {
			Built *bool `json:"built"`
		} `json:"dependenciesMeta"`
	}
	data, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err != nil || json.Unmarshal(data, &m) != nil {
		// Unreadable, so it may say anything.
		return true
	}
	for _, meta := range m.DependenciesMeta {
		if meta.Built != nil && *meta.Built {
			return true
		}
	}
	return false
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
