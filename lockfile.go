package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pkgVersion is a resolved (name, version) pair from a lockfile.
type pkgVersion struct {
	Name    string
	Version string
}

type lockDep struct {
	Version      string             `json:"version"`
	Dependencies map[string]lockDep `json:"dependencies"`
}

type lockPackageEntry struct {
	Version string `json:"version"`
}

type packageLock struct {
	LockfileVersion int                         `json:"lockfileVersion"`
	Packages        map[string]lockPackageEntry `json:"packages"`
	Dependencies    map[string]lockDep          `json:"dependencies"`
}

// findNearestLockfile walks up from dir looking for a supported lockfile from
// any of the three major package managers (npm, yarn, pnpm).
func findNearestLockfile(dir, cmd string) string {
	// Prefer the lockfile matching the invoking package manager across the whole
	// walk-up first, so a stale package-lock.json in a subdir can't shadow the
	// real pnpm-lock.yaml/yarn.lock the command will actually use.
	if preferred := managerLockfileNames(cmd); len(preferred) > 0 {
		if p := walkUpForLockfile(dir, preferred); p != "" {
			return p
		}
	}
	return walkUpForLockfile(dir, []string{
		"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock",
	})
}

// managerLockfileNames returns the lockfile(s) native to a package manager.
func managerLockfileNames(cmd string) []string {
	switch cmd {
	case "npm":
		return []string{"package-lock.json", "npm-shrinkwrap.json"}
	case "pnpm":
		return []string{"pnpm-lock.yaml"}
	case "yarn":
		return []string{"yarn.lock"}
	default:
		return nil
	}
}

func walkUpForLockfile(dir string, names []string) string {
	for {
		for _, name := range names {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// parseLockfile dispatches to the right parser based on the lockfile's name.
func parseLockfile(path string) ([]pkgVersion, error) {
	switch filepath.Base(path) {
	case "package-lock.json", "npm-shrinkwrap.json":
		return parseNpmLockfile(path)
	case "yarn.lock":
		return parseYarnLock(path)
	case "pnpm-lock.yaml":
		return parsePnpmLock(path)
	default:
		return nil, fmt.Errorf("unsupported lockfile: %s", filepath.Base(path))
	}
}

// lockDepName extracts the REAL package name from a yarn/pnpm descriptor such as
// "lodash@^4.17.0", "@babel/core@^7.0.0", or "lodash@npm:^1.0.0". It resolves
// Yarn aliases ("alias@npm:real-package@^2") to the real package name so the
// installed code is scanned under an identity that matches blocklists/OSV.
func lockDepName(descriptor string) string {
	d := strings.Trim(strings.TrimSpace(descriptor), `"'`)
	// Handle the yarn "npm:" protocol. Two distinct shapes:
	//   lodash@npm:^1.0.0            -> plain protocol; real name is "lodash"
	//   my-alias@npm:real-pkg@^2.0.0 -> ALIAS; real name is "real-pkg"
	// Distinguish by what follows "@npm:": a version-range char => protocol
	// (keep the alias-side name); otherwise it's an aliased real package name.
	if i := strings.Index(d, "@npm:"); i >= 0 {
		rest := d[i+len("@npm:"):]
		if rest == "" || isVersionRangeStart(rest[0]) {
			d = d[:i] // protocol form: real name is before @npm:
		} else {
			d = rest // alias form: real name is after @npm:
		}
	}
	scoped := strings.HasPrefix(d, "@")
	body := d
	if scoped {
		body = d[1:]
	}
	at := strings.IndexByte(body, '@')
	if at < 0 {
		return d
	}
	if scoped {
		return "@" + body[:at]
	}
	return body[:at]
}

// parseYarnLock parses both classic (v1) and berry (v2+) yarn.lock files by
// pairing each descriptor block with its `version` line.
func parseYarnLock(path string) ([]pkgVersion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []pkgVersion
	var currentName string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indented := line[0] == ' ' || line[0] == '\t'
		if !indented && strings.HasSuffix(trimmed, ":") {
			// Descriptor line: "name@range, name@range2:" — take the first descriptor.
			desc := strings.TrimSuffix(trimmed, ":")
			if comma := strings.IndexByte(desc, ','); comma >= 0 {
				desc = desc[:comma]
			}
			currentName = lockDepName(desc)
			continue
		}
		if indented && strings.HasPrefix(trimmed, "version") && currentName != "" {
			rest := strings.TrimPrefix(trimmed, "version")
			rest = strings.TrimLeft(rest, " :\t")
			ver := strings.Trim(strings.TrimSpace(rest), `"'`)
			key := currentName + "@" + ver
			if ver != "" && !seen[key] {
				seen[key] = true
				out = append(out, pkgVersion{Name: currentName, Version: ver})
			}
			currentName = ""
		}
	}
	return out, nil
}

// splitPnpmNameVersion extracts (name, version) from a pnpm `packages:` key,
// handling ALL major formats:
//   - v6/v9:  /name@version            /@scope/name@version   name@ver(peer)
//   - v5:     /name/version            /@scope/name/version   /name/ver_peerhash
//
// It works by finding the FIRST separator ('@' or '/') that is immediately
// followed by a digit — the name|version boundary — which correctly ignores the
// scope '@' and any trailing peer suffix (stripped afterward). This fixes the
// silent "v5 lockfile parses to zero packages" scan-bypass.
func splitPnpmNameVersion(key string) (name, version string) {
	key = strings.Trim(strings.TrimSpace(key), `'"`)
	key = strings.TrimPrefix(key, "/")
	if i := strings.IndexByte(key, '('); i >= 0 { // v6 peer suffix "(react@18)"
		key = key[:i]
	}
	sep := -1
	for i := 0; i+1 < len(key); i++ {
		if (key[i] == '@' || key[i] == '/') && key[i+1] >= '0' && key[i+1] <= '9' {
			sep = i
			break
		}
	}
	if sep <= 0 {
		return "", ""
	}
	name = key[:sep]
	version = key[sep+1:]
	if u := strings.IndexByte(version, '_'); u >= 0 { // v5 peer suffix "1.2.3_react@18"
		version = version[:u]
	}
	return name, version
}

// parsePnpmLock parses pnpm-lock.yaml by scanning the `packages:`/`snapshots:`
// sections for package keys in any pnpm lockfile format (see splitPnpmNameVersion).
func parsePnpmLock(path string) ([]pkgVersion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []pkgVersion
	inPackages := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "packages:") || strings.HasPrefix(line, "snapshots:") {
			inPackages = true
			continue
		}
		// A new top-level key ends the packages/snapshots block.
		if line != "" && line[0] != ' ' && line[0] != '\t' && strings.HasSuffix(strings.TrimSpace(line), ":") {
			inPackages = strings.HasPrefix(line, "packages:") || strings.HasPrefix(line, "snapshots:")
			continue
		}
		if !inPackages {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasSuffix(trimmed, ":") {
			continue
		}
		key := strings.TrimSuffix(trimmed, ":")
		name, ver := splitPnpmNameVersion(key)
		if name == "" || ver == "" {
			continue
		}
		dedup := name + "@" + ver
		if !seen[dedup] {
			seen[dedup] = true
			out = append(out, pkgVersion{Name: name, Version: ver})
		}
	}
	return out, nil
}

// parseNpmLockfile extracts every resolved (name, version) from a
// package-lock.json — the v2/v3 "packages" map and the v1 nested
// "dependencies" tree — so the FULL transitive set can be scanned, not just
// the top-level packages a developer typed on the CLI.
func parseNpmLockfile(path string) ([]pkgVersion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lock packageLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []pkgVersion
	add := func(name, ver string) {
		name = strings.TrimSpace(name)
		if name == "" || ver == "" {
			return
		}
		key := name + "@" + ver
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, pkgVersion{Name: name, Version: ver})
	}

	// v2/v3: keys are "" (root) or "node_modules/<name>" (possibly nested,
	// e.g. "node_modules/a/node_modules/b"). The real name is the segment
	// after the last "node_modules/".
	for k, p := range lock.Packages {
		if k == "" {
			continue
		}
		name := k
		if idx := strings.LastIndex(k, "node_modules/"); idx >= 0 {
			name = k[idx+len("node_modules/"):]
		}
		add(name, p.Version)
	}

	// v1 fallback: nested dependencies tree.
	var walk func(map[string]lockDep)
	walk = func(deps map[string]lockDep) {
		for name, d := range deps {
			add(name, d.Version)
			if len(d.Dependencies) > 0 {
				walk(d.Dependencies)
			}
		}
	}
	walk(lock.Dependencies)

	return out, nil
}

// verifyResolvedTree scans the full resolved dependency tree from the nearest
// npm lockfile against the policy blocklist and the OSV vulnerability database.
// This is what makes a bare `npm install` / `npm ci` (which restores the whole
// tree from the lockfile, with no packages on the CLI) actually get checked.
func verifyResolvedTree(nvxHome, cmd string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	lockPath := findNearestLockfile(cwd, cmd)
	if lockPath == "" {
		return
	}

	pkgs, err := parseLockfile(lockPath)
	if err != nil {
		LogWarn("Could not parse %s: %v. Skipping resolved-tree scan.", filepath.Base(lockPath), err)
		return
	}
	if len(pkgs) == 0 {
		// A recognized lockfile that yields zero packages is more likely a parser
		// gap than a genuinely empty project — surface it rather than silently
		// treating it as "nothing to scan" (which would be a scan bypass).
		LogWarn("Parsed 0 packages from %s (unrecognized lockfile format?); resolved-tree scan skipped.", filepath.Base(lockPath))
		return
	}

	policy, _ := LoadPolicy(nvxHome)

	var osvQueries []OSVQuery
	for _, p := range pkgs {
		if policy.IsBlocked(p.Name) {
			LogError("Blocked by security policy: %q is present in the resolved dependency tree (%s).", p.Name, filepath.Base(lockPath))
			os.Exit(1)
		}
		osvQueries = append(osvQueries, OSVQuery{
			Package: OSVPackage{Name: p.Name, Ecosystem: "npm"},
			Version: p.Version,
		})
	}

	LogInfo("Scanning %d resolved dependencies from %s for known vulnerabilities...", len(osvQueries), filepath.Base(lockPath))
	vulns, err := ScanVulnerabilitiesBatch(osvQueries)
	if err != nil {
		failClosedOrWarn(policy, "Resolved-tree vulnerability scan failed: %v.", err)
		return
	}
	if len(vulns) > 0 {
		LogError("Vulnerability Scan Alert: Found active vulnerabilities in the dependency tree!")
		for pkgKey, list := range vulns {
			fmt.Fprintf(os.Stderr, "  \x1b[31m●\x1b[0m %s:\n", pkgKey)
			for _, v := range list {
				fmt.Fprintf(os.Stderr, "    - %s: %s\n", v.ID, v.Summary)
			}
		}
		fmt.Fprintln(os.Stderr)
		if !PromptYesNo("Proceed with installation despite active vulnerabilities in the dependency tree?") {
			LogError("Installation aborted due to active vulnerabilities in the resolved tree.")
			os.Exit(1)
		}
	} else {
		LogSuccess("Resolved-tree scan clean. No active CVEs across %d dependencies.", len(osvQueries))
	}
}

// installSubcommandIndex returns the index of the install-style subcommand in a
// package-manager argument list, or -1. The subcommand is the first non-flag
// argument, so leading flags cannot hide it.
func installSubcommandIndex(args []string) int {
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if installAliases[arg] {
			return i
		}
		return -1
	}
	return -1
}

// ensureIgnoreScripts appends --ignore-scripts to an install invocation if it
// is not already present, so enforce_ignore_scripts is actually enforced rather
// than merely warned about.
func ensureIgnoreScripts(args []string) []string {
	for _, a := range args {
		if a == "--ignore-scripts" {
			return args
		}
	}
	return append(args, "--ignore-scripts")
}

// isVersionRangeStart reports whether c begins a semver range (vs a package name).
func isVersionRangeStart(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c == '^' || c == '~' || c == '>' || c == '<' || c == '=' || c == '*' || c == '|' || c == ' ':
		return true
	}
	return false
}

// isInstallManager reports whether cmd is a package manager whose install
// commands should be verified (npm, yarn, pnpm, bun).
func isInstallManager(cmd string) bool {
	switch cmd {
	case "npm", "yarn", "pnpm", "bun":
		return true
	}
	return false
}

// isExecuteRunner reports whether cmd downloads-and-runs a package (npx, bunx).
// These are prime supply-chain vectors and must be verified before execution.
func isExecuteRunner(cmd string) bool {
	return cmd == "npx" || cmd == "bunx"
}

// firstNonFlagIndex returns the index of the first non-flag argument, or -1.
func firstNonFlagIndex(args []string) int {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return i
		}
	}
	return -1
}

// detectExecutePackage returns the registry package an execute-runner (npx/bunx,
// or `bun x`) will fetch and run, or "" for none/local paths. Scoped packages
// (@scope/name) are returned; local paths (./x, /x, x/y) are skipped.
func detectExecutePackage(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.HasPrefix(a, "@") {
			return a // scoped package spec
		}
		if strings.HasPrefix(a, ".") || strings.HasPrefix(a, "/") || strings.Contains(a, "/") {
			return "" // local path, not a registry package
		}
		return a
	}
	return ""
}

// isYarnBerry reports whether the nearest yarn project is Yarn Berry (v2+),
// which removed the --ignore-scripts CLI flag in favor of enableScripts /
// YARN_ENABLE_SCRIPTS. Detected via .yarnrc.yml or a berry-style yarn.lock.
func isYarnBerry() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".yarnrc.yml")); err == nil {
			return true
		}
		if data, err := os.ReadFile(filepath.Join(dir, "yarn.lock")); err == nil {
			if strings.Contains(string(data), "__metadata:") {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// applyIgnoreScripts enforces script-disabling appropriate to the manager.
// Yarn Berry removed --ignore-scripts, so for it we set YARN_ENABLE_SCRIPTS=false
// on the nvx process (allowlisted, so it survives sandbox env scrubbing and is
// inherited by the child); all others get the --ignore-scripts flag.
func applyIgnoreScripts(cmd string, args []string) []string {
	if cmd == "yarn" && isYarnBerry() {
		os.Setenv("YARN_ENABLE_SCRIPTS", "false")
		return args
	}
	return ensureIgnoreScripts(args)
}
