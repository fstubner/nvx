package nvx

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// A tool that is already installed in the project is run by npx, not fetched.
//
// `npx vitest` in a project that has vitest installed starts
// node_modules/.bin/vitest and makes no request. Every npx was classified as an
// ad-hoc tool anyway, so the project's own test runner, bundler and ORM CLI ran
// contained and asked the install-script question, while the same tools through
// `npm run` ran as your own code.
//
// runsProjectBin names the invocations that only run an installed tool. They
// are classified as `npm run` is, and the pre-install checks skip them, because
// nothing is fetched. It has to agree with the package manager, since a wrong
// answer runs a fetched package uncontained. Measured 2026-10-07 against a local
// registry that logs every request, with npm 8.12.1, 9.6.3, 10.2.4, 10.9.8 and
// 11.19.0 and bun 1.4.2, all on Windows:
//
//   - `npx <name>` and `npm exec <name>`, also with -y, --yes, --no and
//     --no-install, ran the installed bin and made no request. npm looks for
//     the file <name> itself, so with only <name>.cmd in .bin it fetched.
//   - From npm 9.6.3 to 11.19.0, a name that the project's own package.json
//     lists under "bin" installed the project into npm's cache instead of
//     running .bin. npm 8.12.1 ran .bin.
//   - `name@latest` fetched. `name@1` ran the installed copy because it
//     satisfied the range, which is npm's judgment, so any spec stays ad hoc,
//     as do -p, --package and -c.
//   - bunx ran <name>.exe, <name>.cmd or <name>.bat from .bin, then any program
//     on PATH, and fetched only when both missed. A version in the name went to
//     a separate install in the temp folder.
//   - `npm exec <name> --package=<spec>` read the flag as its own and fetched
//     <spec> with every npm version above. `npx`, bunx and bun x pass what
//     follows the name to the tool.
//   - A `package` setting in .npmrc, or npm_config_package in the environment,
//     made `npx <name>` and `npm exec <name>` fetch it (npm 11.19.0).
//
// An unknown flag could take the next word as its value and move the name, so
// any flag outside the lists below makes the line an ad-hoc tool run.

// execRunner is the package manager whose rules read an npx-style command line.
type execRunner struct {
	bun bool
	// switches are the flags that take no value.
	switches map[string]bool
	// flagsAfter is set for `npm exec`, which reads every flag as its own up to a
	// "--", wherever it stands. The others pass what follows the name to the tool.
	flagsAfter bool
}

var (
	npmRunner = execRunner{switches: map[string]bool{
		"-y": true, "--yes": true, "--no": true, "--no-install": true,
		"-q": true, "--quiet": true, "--silent": true,
	}}
	bunRunner = execRunner{bun: true, switches: map[string]bool{
		"--bun": true, "-b": true, "--no-install": true, "--verbose": true, "--silent": true,
	}}
)

// toolNamePattern is a bare command name. A scope, version, path or URL is how
// a package is asked for.
var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// runsProjectBin reports whether this npx, npm exec, bunx or bun x line runs a
// tool that is already in the project's node_modules/.bin and fetches nothing.
// args are as readCommand returns them.
func runsProjectBin(cmd string, args []string) bool {
	runner, rest, ok := execInvocation(cmd, args)
	if !ok {
		return false
	}
	i, afterDashes, ok := firstPositional(rest, runner.switches)
	if !ok || !toolNamePattern.MatchString(rest[i]) || strings.HasSuffix(rest[i], ".") {
		return false
	}
	if runner.flagsAfter && !afterDashes && !onlySwitches(rest[i+1:], runner.switches) {
		return false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	if runtime.GOOS != "windows" {
		// Both package managers start from the real folder, which Getwd may not
		// return after a cd through a link.
		if cwd, err = filepath.EvalSymlinks(cwd); err != nil {
			return false
		}
	}
	return projectHasBin(cwd, rest[i], runner)
}

// execInvocation returns the runner for cmd and the arguments after its verb.
func execInvocation(cmd string, args []string) (execRunner, []string, bool) {
	switch strings.ToLower(cmd) {
	case "npx":
		return npmRunner, args, true
	case "bunx":
		return bunRunner, args, true
	case "npm":
		r := npmRunner
		r.flagsAfter = true
		return afterVerb(r, args, "exec", "x")
	case "bun":
		return afterVerb(bunRunner, args, "x")
	}
	return execRunner{}, nil, false
}

func afterVerb(r execRunner, args []string, verbs ...string) (execRunner, []string, bool) {
	i, _, ok := firstPositional(args, r.switches)
	if !ok || !slices.Contains(verbs, args[i]) {
		return r, nil, false
	}
	return r, args[i+1:], true
}

// firstPositional returns the index of the first argument that is not a flag,
// reading a flag only when it is one of switches. After "--" the next argument
// is the one, and the second result says so. The third is false when there is
// no such argument or a flag is not one of switches.
func firstPositional(args []string, switches map[string]bool) (int, bool, bool) {
	for i, a := range args {
		switch {
		case a == "--":
			return i + 1, true, i+1 < len(args)
		case isFlag(a):
			if name, _, _ := strings.Cut(a, "="); !switches[name] {
				return 0, false, false
			}
		default:
			return i, false, true
		}
	}
	return 0, false, false
}

// onlySwitches reports whether every flag in args, up to a "--", is one of
// switches.
func onlySwitches(args []string, switches map[string]bool) bool {
	for _, a := range args {
		if a == "--" {
			return true
		}
		if isFlag(a) {
			if name, _, _ := strings.Cut(a, "="); !switches[name] {
				return false
			}
		}
	}
	return true
}

func isFlag(arg string) bool { return strings.HasPrefix(arg, "-") && arg != "-" && arg != "--" }

// projectHasBin reports whether cwd's project holds name in node_modules/.bin
// where the runner finds it. The project is the one nvx scopes the sandbox to.
// Inside a workspace the root's folder counts too, which is where installs put
// what the members share.
func projectHasBin(cwd, name string, r execRunner) bool {
	project := findProjectRoot(cwd)
	if project == "" {
		return false
	}
	roots := []string{project}
	if ws := workspaceRoot(project); ws != "" {
		roots = append(roots, ws)
	}
	for _, root := range roots {
		if declaresBin(root, name) {
			return false
		}
	}
	if !r.bun && configuresPackage(roots) {
		return false
	}
	for _, root := range roots {
		// The nearest entry is the one that runs, so a bad one is not skipped for
		// a better one further out.
		if file, ok := binEntry(root, name, r); ok {
			return withinDir(file, root)
		}
	}
	return false
}

// configuresPackage reports a `package` setting where npm reads one: the
// project's or the user's .npmrc, or the environment. npm exec fetches what it
// names, whatever the command line says. A contained install can write the
// project's .npmrc.
func configuresPackage(roots []string) bool {
	files := []string{userNpmrcPath(false)}
	for _, root := range roots {
		files = append(files, filepath.Join(root, ".npmrc"))
	}
	for _, file := range files {
		for key := range readNpmrc(file) {
			if key == "package" || key == "package[]" {
				return true
			}
		}
	}
	return envCaseInsensitive("npm_config_package") != ""
}

// binEntry returns the file in root's node_modules/.bin that the runner starts
// for name. Bun's own lookup skips a file that is not executable, and on Windows
// finds only .exe, .cmd and .bat.
func binEntry(root, name string, r execRunner) (string, bool) {
	file := filepath.Join(projectNodeModulesBin(root), name)
	files := []string{file}
	if r.bun && runtime.GOOS == "windows" {
		files = []string{file + ".exe", file + ".cmd", file + ".bat"}
	}
	for _, f := range files {
		// Stat follows links, as both package managers do.
		info, err := os.Stat(f)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if r.bun && runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return f, true
	}
	return "", false
}

// withinDir reports whether file, once links are followed, is inside dir. A
// link out of the project leads to code the project does not hold, and a
// contained run can plant one.
func withinDir(file, dir string) bool {
	realFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		return false
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	return dirWithin(realFile, realDir)
}

// binManifest is the part of a package.json that decides where a name runs from.
type binManifest struct {
	Name        string          `json:"name"`
	Bin         json.RawMessage `json:"bin"`
	Directories struct {
		Bin string `json:"bin"`
	} `json:"directories"`
	Workspaces json.RawMessage `json:"workspaces"`
}

func readBinManifest(dir string) (binManifest, error) {
	var m binManifest
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return m, err
	}
	// PowerShell writes a byte order mark that npm reads past.
	return m, json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &m)
}

// declaresBin reports whether the package.json in dir lists name as one of its
// own bins. A file that cannot be read says so for every name, because nvx
// cannot tell what npm will do with it.
func declaresBin(dir, name string) bool {
	m, err := readBinManifest(dir)
	if err != nil {
		return !os.IsNotExist(err)
	}
	if m.Directories.Bin != "" {
		// Every file in that folder is a bin.
		return true
	}
	bin := bytes.TrimSpace(m.Bin)
	switch {
	case len(bin) == 0 || string(bin) == "null":
		return false
	case bin[0] == '{':
		var bins map[string]json.RawMessage
		if json.Unmarshal(bin, &bins) != nil {
			return true
		}
		_, ok := bins[name]
		return ok
	case bin[0] == '"':
		// A single path is named for the package, without its scope.
		return m.Name == "" || path.Base(m.Name) == name
	}
	return true
}

// workspaceRoot returns the nearest folder above project whose package.json
// lists project as a workspace, or "". Patterns are matched as in
// workspaceMemberSpecs.
func workspaceRoot(project string) string {
	for dir := project; ; {
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
		if listsWorkspace(dir, project) {
			return dir
		}
	}
}

func listsWorkspace(root, member string) bool {
	m, err := readBinManifest(root)
	if err != nil {
		return false
	}
	for _, p := range workspacePatterns(m.Workspaces) {
		if strings.HasPrefix(p, "!") {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(p, "**", "*"))))
		for _, match := range matches {
			if dirsEqual(match, member) {
				return true
			}
		}
	}
	return false
}
