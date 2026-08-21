package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// nvxActiveEnvVar marks that the current process tree already reported its
// top-level containment status, so a nested nvx-shimmed invocation (e.g. a
// build script's own npm/node calls) does not repeat it. Set once and inherited
// by every child process from then on — see runShim's uncontained-path status
// line for why a per-process guard is not enough here.
const nvxActiveEnvVar = "NVX_SHIM_ACTIVE"

// isTopLevelShimInvocation reports whether this process is the outermost
// nvx-shimmed invocation in its process tree, marking the tree as active for
// any child so nested invocations answer false. os.Setenv only affects this
// process's own environment block, but child processes started afterwards
// (exec.Command with a nil Env) inherit that block, so the mark propagates
// down through however many nested nvx.exe processes a build script spawns.
func isTopLevelShimInvocation() bool {
	if os.Getenv(nvxActiveEnvVar) != "" {
		return false
	}
	_ = os.Setenv(nvxActiveEnvVar, "1")
	return true
}

// GetHomeDir returns the root directory for nvx (defaults to ~/.nvx)
func GetHomeDir() string {
	if home := os.Getenv("NVX_HOME"); home != "" {
		return filepath.Clean(home)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ".nvx"
	}
	return filepath.Join(userHome, ".nvx")
}

// GetVersionsDir returns the path to the versions subdirectory
func GetVersionsDir() string {
	return filepath.Join(GetHomeDir(), "versions")
}

// GetDownloadsDir returns the path to the temporary downloads directory
func GetDownloadsDir() string {
	return filepath.Join(GetHomeDir(), "downloads")
}

// GetCurrentLinkPath returns the path of the global default link
func GetCurrentLinkPath() string {
	return currentLinkPath(GetHomeDir())
}

func currentLinkPath(nvxHome string) string {
	return filepath.Join(nvxHome, "current")
}

// GetVersionBinDir returns the directory containing a runtime's executables for
// a given version folder. On Unix that is always <versionDir>/bin. On Windows
// most runtimes place the binary at the version root (node.exe, bun.exe), but
// some (e.g. Go) keep a bin/ subdir on every platform; prefer it when present.
func GetVersionBinDir(versionDir string) string {
	binSub := filepath.Join(versionDir, "bin")
	if runtime.GOOS == "windows" {
		if info, err := os.Stat(binSub); err == nil && info.IsDir() {
			return binSub
		}
		return versionDir
	}
	return binSub
}

// GetNpmPrefixBinDir returns the executable directory for a given npm prefix
// (on Windows npm places global binaries in the prefix itself, on Unix in bin/)
func GetNpmPrefixBinDir(prefixDir string) string {
	if runtime.GOOS == "windows" {
		return prefixDir
	}
	return filepath.Join(prefixDir, "bin")
}

// GetNpmGlobalBinDir returns the directory containing globally installed npm packages
func GetNpmGlobalBinDir(versionDir string) string {
	return GetNpmPrefixBinDir(filepath.Join(versionDir, "npm_global"))
}

// CreateLink creates a link (Junction on Windows, Symlink on Unix)
func CreateLink(link, target string) error {
	// Clean up existing link/file if it exists
	if _, err := os.Lstat(link); err == nil {
		err = os.Remove(link)
		if err != nil {
			return fmt.Errorf("failed to remove existing link: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "mklink", "/j", link, target)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create directory junction: %w", err)
		}
	} else {
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("failed to create symbolic link: %w", err)
		}
	}
	return nil
}

// projectToolsMarker matches PATH entries produced by project-scoped tool
// isolation (<project>/.nvx/npm_global[/bin]) so stale ones can be removed.
var projectToolsMarker = string(os.PathSeparator) + ".nvx" + string(os.PathSeparator) + "npm_global"

// CleanAndBuildPath cleans nvx-related directories from the PATH and prepends
// the new version and npm prefix directories. npmPrefixDir may be a
// version-level npm_global dir or a project-local .nvx/npm_global dir.
func CleanAndBuildPath(currentPath, nvxHome, targetVersionDir, npmPrefixDir string) string {
	parts := filepath.SplitList(currentPath)
	var cleaned []string

	versionsDir := filepath.Join(nvxHome, "versions")
	currentLink := filepath.Join(nvxHome, "current")
	currentLinkBin := GetVersionBinDir(currentLink)
	currentLinkNpm := GetNpmGlobalBinDir(currentLink)

	// Scope the strip to a single runtime when the target names one, so that
	// switching (say) Bun does not evict an active Node from PATH. Legacy/flat
	// version dirs and node keep the original whole-versions-dir behavior.
	targetRuntime := runtimeFromVersionDir(nvxHome, targetVersionDir)
	isNodeScope := targetRuntime == "" || targetRuntime == "node"
	stripPrefix := filepath.Clean(versionsDir) + string(os.PathSeparator)
	if targetRuntime != "" {
		stripPrefix = filepath.Clean(filepath.Join(versionsDir, targetRuntime)) + string(os.PathSeparator)
	}

	for _, part := range parts {
		if part == "" {
			continue
		}
		normPart := filepath.Clean(part)
		normCurrentLink := filepath.Clean(currentLink)
		normCurrentLinkBin := filepath.Clean(currentLinkBin)
		normCurrentLinkNpm := filepath.Clean(currentLinkNpm)

		// Remove v* version and npm_global paths for the runtime being switched.
		if strings.HasPrefix(strings.ToLower(normPart), strings.ToLower(stripPrefix)) {
			continue
		}
		// Clean the node default-link paths only when switching node itself.
		if isNodeScope && (strings.ToLower(normPart) == strings.ToLower(normCurrentLink) ||
			strings.ToLower(normPart) == strings.ToLower(normCurrentLinkBin) ||
			strings.ToLower(normPart) == strings.ToLower(normCurrentLinkNpm)) {
			continue
		}
		// Remove stale project-scoped tool dirs from previously visited projects
		if strings.Contains(strings.ToLower(normPart), strings.ToLower(projectToolsMarker)) {
			continue
		}

		cleaned = append(cleaned, part)
	}

	// Remove existing shim dir if present to avoid duplicates
	shimDir := filepath.Join(nvxHome, "bin")
	var finalCleaned []string
	for _, part := range cleaned {
		if strings.EqualFold(filepath.Clean(part), filepath.Clean(shimDir)) {
			continue
		}
		finalCleaned = append(finalCleaned, part)
	}
	cleaned = finalCleaned

	// Prepend the new target version directory and (for node) its npm prefix bin.
	if targetVersionDir != "" {
		binDir := GetVersionBinDir(targetVersionDir)
		if isNodeScope {
			if npmPrefixDir == "" {
				npmPrefixDir = filepath.Join(targetVersionDir, "npm_global")
			}
			npmBinDir := GetNpmPrefixBinDir(npmPrefixDir)
			cleaned = append([]string{npmBinDir, binDir}, cleaned...)
		} else {
			cleaned = append([]string{binDir}, cleaned...)
		}
	}

	// Global nvx shims first, then project node_modules/.bin shims, then runtime paths.
	var prefix []string
	prefix = append(prefix, shimDir)
	if cwd, err := os.Getwd(); err == nil {
		if root := findProjectRoot(cwd); root != "" {
			pb := projectBinDir(root, nvxHome)
			if _, err := os.Stat(pb); err == nil {
				prefix = append(prefix, pb)
			}
		}
	}
	cleaned = append(prefix, cleaned...)

	return strings.Join(cleaned, string(filepath.ListSeparator))
}

// lookPathSkippingNvxShims resolves cmdName on PATH with ~/.nvx/bin removed so
// shim wrappers (node.cmd) are not mistaken for the real runtime binary. The
// (slow, on Windows) PATH scan is memoized per PATH via the bin-resolve cache.
func lookPathSkippingNvxShims(cmdName, nvxHome string) (string, error) {
	if cached := lookupBinCache(nvxHome, cmdName); cached != "" {
		return cached, nil
	}
	resolved, err := lookPathSkippingNvxShimsUncached(cmdName, nvxHome)
	if err == nil {
		storeBinCache(nvxHome, cmdName, resolved)
	}
	return resolved, err
}

func lookPathSkippingNvxShimsUncached(cmdName, nvxHome string) (string, error) {
	shimDir := filepath.Join(nvxHome, "bin")
	pathEnv := os.Getenv("PATH")
	var filtered []string
	for _, part := range filepath.SplitList(pathEnv) {
		if strings.EqualFold(filepath.Clean(part), filepath.Clean(shimDir)) {
			continue
		}
		filtered = append(filtered, part)
	}
	restore := pathEnv
	if err := os.Setenv("PATH", strings.Join(filtered, string(filepath.ListSeparator))); err != nil {
		return "", fmt.Errorf("temporarily update PATH: %w", err)
	}
	defer func() {
		if err := os.Setenv("PATH", restore); err != nil {
			LogWarn("Failed to restore PATH after runtime lookup: %v", err)
		}
	}()
	return exec.LookPath(cmdName)
}

// preferWindowsRuntimeExe returns the PE executable for a Windows runtime shim path.
func preferWindowsRuntimeExe(cmdPath string) string {
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Ext(cmdPath), ".cmd") {
		return cmdPath
	}
	exePath := strings.TrimSuffix(cmdPath, filepath.Ext(cmdPath)) + ".exe"
	if _, err := os.Stat(exePath); err == nil {
		return exePath
	}
	return cmdPath
}

func generateShims(nvxHome string) error {
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0700); err != nil {
		return fmt.Errorf("create shim directory: %w", err)
	}

	exePath := stableShimTarget(nvxHome)
	for _, cmd := range allShimCommands() {
		if runtime.GOOS == "windows" {
			exeCmd := quoteWindowsBatchArg(exePath)
			content := fmt.Sprintf("@echo off\r\n%s shim %s %%*\r\n", exeCmd, quoteWindowsBatchArg(cmd))
			if err := writeExecutableFile(filepath.Join(shimDir, cmd+".cmd"), []byte(content)); err != nil {
				return fmt.Errorf("write cmd shim for %s: %w", cmd, err)
			}

			contentPs1 := fmt.Sprintf("& %s shim %s @args\r\n", quotePowerShell(exePath), quotePowerShell(cmd))
			if err := writeExecutableFile(filepath.Join(shimDir, cmd+".ps1"), []byte(contentPs1)); err != nil {
				return fmt.Errorf("write PowerShell shim for %s: %w", cmd, err)
			}

			// Also write an extensionless POSIX shim on Windows, for bash.
			//
			// cmd.exe and PowerShell find `npm` through PATHEXT and pick up the
			// .cmd/.ps1 above. bash does not consult PATHEXT: it looks for a file
			// named exactly `npm`, so with only those two present a bare `npm` in
			// Git Bash resolved straight past nvx to the real npm -- no audit, no
			// sandbox, and `nvx doctor` reported interception as healthy because it
			// was answering the PATHEXT question.
			//
			// That is not an edge case on this platform: Git Bash is what most
			// agent harnesses run on Windows, and agent-driven installs are the
			// case nvx exists for.
			contentSh := fmt.Sprintf("#!/bin/sh\nexec %s shim %s \"$@\"\n", quotePOSIXShell(exePath), quotePOSIXShell(cmd))
			if err := writeExecutableFile(filepath.Join(shimDir, cmd), []byte(contentSh)); err != nil {
				return fmt.Errorf("write POSIX shim for %s: %w", cmd, err)
			}
		} else {
			content := fmt.Sprintf("#!/bin/sh\nexec %s shim %s \"$@\"\n", quotePOSIXShell(exePath), quotePOSIXShell(cmd))
			shimPath := filepath.Join(shimDir, cmd)
			if err := writeExecutableFile(shimPath, []byte(content)); err != nil {
				return fmt.Errorf("write shim for %s: %w", cmd, err)
			}
		}
	}
	return nil
}

func quoteWindowsBatchArg(s string) string {
	escaped := strings.ReplaceAll(s, `%`, `%%`)
	escaped = strings.ReplaceAll(escaped, `"`, `""`)
	return `"` + escaped + `"`
}

func writeExecutableFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0700) // #nosec G302 -- generated shim wrappers must be executable by the owner.
}

// installAliases covers the install/add spellings accepted by npm, yarn, and
// pnpm, including npm's typo aliases (isntall etc.) which would otherwise
// bypass verification.
var installAliases = map[string]bool{
	"install": true, "i": true, "in": true, "ins": true, "inst": true,
	"insta": true, "instal": true, "isnt": true, "isnta": true,
	"isntall": true, "add": true,
}

// findInstallVerbIndex scans args for a token matching installAliases or one
// of extraVerbs, and returns its index, or -1 if none is found. It does NOT
// assume the first non-flag token is the subcommand — it scans every
// non-flag-shaped token until it finds a real match (or a "--" passthrough
// separator, which conventionally ends flag/subcommand parsing). This means
// an unrecognized value-taking flag ahead of the real subcommand (e.g. `npm
// --loglevel verbose install pkg`, where --loglevel isn't in any hardcoded
// "takes a value" list) can never hide an install: the scan simply keeps
// going past the flag's value and finds "install" further along. The only
// failure direction is a stray non-flag token that happens to match a verb
// name being mistaken for the subcommand, which pushes a command toward MORE
// containment/verification, never less — the safe direction for a security
// classifier to fail in.
func findInstallVerbIndex(args []string, extraVerbs ...string) int {
	extra := make(map[string]bool, len(extraVerbs))
	for _, v := range extraVerbs {
		extra[strings.ToLower(v)] = true
	}
	for i, arg := range args {
		if arg == "--" {
			return -1
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		lower := strings.ToLower(arg)
		if installAliases[lower] || extra[lower] {
			return i
		}
	}
	return -1
}

// hasInstallVerb reports whether args contains an install-style subcommand
// (installAliases plus any extraVerbs). See findInstallVerbIndex for how it
// avoids being fooled by an unrecognized value-taking flag.
func hasInstallVerb(args []string, extraVerbs ...string) bool {
	return findInstallVerbIndex(args, extraVerbs...) != -1
}

// installPackagesArg finds an install-style subcommand in args (installAliases
// plus any extraVerbs) and returns the non-flag tokens that follow it — the
// explicitly named packages. Returns nil if no install verb is found.
func installPackagesArg(args []string, extraVerbs ...string) []string {
	subIdx := findInstallVerbIndex(args, extraVerbs...)
	if subIdx == -1 {
		return nil
	}
	var pkgs []string
	for _, arg := range args[subIdx+1:] {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			pkgs = append(pkgs, arg)
		}
	}
	return pkgs
}

// detectInstallPackages scans package manager arguments for an install-style
// subcommand and returns the package names being installed.
func detectInstallPackages(args []string) []string {
	return installPackagesArg(args)
}

// globalInstallFlags are the npm/yarn/pnpm flags that make an install target
// the shared, version-wide npm_global prefix instead of the project's
// node_modules.
var globalInstallFlags = map[string]bool{"-g": true, "--global": true}

// isGlobalInstall reports whether args requests a global package-manager
// install. The sandbox deliberately never grants write access to npm_global
// on any platform (see prepareAppContainerFilesystem, applyLandlockSandbox,
// buildSeatbeltProfile): a shared, version-wide location that every future
// invocation trusts (runtime_exec.go's npmGlobalOverridePath) must never be
// writable by contained, potentially malicious code, or a single compromised
// install could plant a binary that silently becomes the resolved npm/npx
// for every project going forward. So a global install can only run
// un-contained — runShim checks this to fail with a clear message instead of
// a confusing permission error partway through.
func isGlobalInstall(cmdName string, args []string) bool {
	switch strings.ToLower(cmdName) {
	case "npm", "yarn", "pnpm":
	default:
		return false
	}
	// yarn spells it as a subcommand, not a flag: `yarn global add <pkg>`.
	//
	// Only the flags were matched, so this fell straight through to the sandbox
	// and then failed partway with the confusing permission error the check exists
	// to replace -- the exact outcome this function's doc comment promises not to
	// produce. Checked before the install-verb test because `global` precedes the
	// verb, and `yarn global remove`/`upgrade` write to the same shared prefix.
	if strings.EqualFold(cmdName, "yarn") && hasLeadingSubcommand(args, "global") {
		return true
	}

	if !hasInstallVerb(args, "ci") {
		return false
	}
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if globalInstallFlags[arg] {
			return true
		}
	}
	return false
}

// hasLeadingSubcommand reports whether name is the first non-flag token in args.
//
// Position matters: `yarn global add foo` is a global install, while
// `yarn add global` installs a package that happens to be called "global". A
// contains-check would conflate them and refuse a legitimate install.
func hasLeadingSubcommand(args []string, name string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			continue // a flag before the subcommand, e.g. `yarn --silent global add`
		}
		return strings.EqualFold(arg, name)
	}
	return false
}

func detectShimPackagesForVerification(cmdName string, args []string) []string {
	switch strings.ToLower(cmdName) {
	case "npm", "yarn", "pnpm":
		if pkgs := detectInstallPackages(args); len(pkgs) > 0 {
			return pkgs
		}
		if hasInstallVerb(args, "ci") {
			if pkgs := packagesFromPackageLock(); len(pkgs) > 0 {
				return pkgs
			}
			return packagesFromPackageJSON()
		}
	case "bun":
		// bun add/install/i/a [pkg...]; "a" is Bun's short alias for add.
		if pkgs := installPackagesArg(args, "a"); len(pkgs) > 0 {
			return pkgs
		}
		if hasInstallVerb(args, "a") {
			return packagesFromPackageJSON()
		}
	case "npx", "bunx":
		return detectExecutorPackages(args)
	}
	return nil
}

func detectExecutorPackages(args []string) []string {
	var pkgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-p" || arg == "--package":
			if i+1 < len(args) {
				pkgs = append(pkgs, args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--package="):
			if v := strings.TrimPrefix(arg, "--package="); v != "" {
				pkgs = append(pkgs, v)
			}
		case strings.HasPrefix(arg, "-"):
			if flagTakesValue(arg) && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
			}
		default:
			if len(pkgs) > 0 {
				return dedupeStrings(pkgs)
			}
			return []string{arg}
		}
	}
	return dedupeStrings(pkgs)
}

// flagTakesValue reports whether arg is a known flag that consumes the next
// token as its value, so callers that need positional detection (which
// package/tool a command targets) can skip over it correctly. This list can
// never be exhaustive across every package manager's evolving flag set — it
// is a best-effort improvement for helpers where the failure mode of missing
// an entry is a wrong verification target or a missed UX prompt, not a
// containment decision. Containment itself (classifyInvocation) does not
// depend on this list; it scans for the subcommand verb directly instead
// (see hasInstallVerb) so an unrecognized flag here cannot bypass a sandbox.
func flagTakesValue(arg string) bool {
	switch arg {
	case "-p", "--package", "--prefix", "--registry", "--cache", "-c", "--call", "--shell",
		"--loglevel", "--tag", "--scope", "--userconfig", "--otp",
		"-w", "--workspace", "--cwd", "--filter":
		return true
	}
	return false
}

type packageLockFile struct {
	Packages     map[string]packageLockPackage `json:"packages"`
	Dependencies map[string]packageLockPackage `json:"dependencies"`
}

type packageLockPackage struct {
	Version      string                        `json:"version"`
	Dependencies map[string]packageLockPackage `json:"dependencies"`
}

func packagesFromPackageLock() []string {
	data, err := os.ReadFile("package-lock.json")
	if err != nil {
		return nil
	}
	var lock packageLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		LogWarn("Failed to parse package-lock.json for verification: %v", err)
		return nil
	}
	seen := map[string]bool{}
	var pkgs []string
	for path, pkg := range lock.Packages {
		name := packageNameFromLockPath(path)
		if name == "" || pkg.Version == "" {
			continue
		}
		query := name + "@" + pkg.Version
		if !seen[query] {
			seen[query] = true
			pkgs = append(pkgs, query)
		}
	}
	addLockDependencies(lock.Dependencies, seen, &pkgs)
	sort.Strings(pkgs)
	return pkgs
}

func addLockDependencies(deps map[string]packageLockPackage, seen map[string]bool, out *[]string) {
	for name, dep := range deps {
		if dep.Version != "" {
			query := name + "@" + dep.Version
			if !seen[query] {
				seen[query] = true
				*out = append(*out, query)
			}
		}
		addLockDependencies(dep.Dependencies, seen, out)
	}
}

func packageNameFromLockPath(path string) string {
	const marker = "node_modules/"
	idx := strings.LastIndex(path, marker)
	if idx == -1 {
		return ""
	}
	return path[idx+len(marker):]
}

func packagesFromPackageJSON() []string {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return nil
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		LogWarn("Failed to parse package.json for verification: %v", err)
		return nil
	}
	seen := map[string]bool{}
	var pkgs []string
	for _, deps := range []map[string]string{
		pkg.Dependencies,
		pkg.DevDependencies,
		pkg.OptionalDependencies,
		pkg.PeerDependencies,
	} {
		for name := range deps {
			if !seen[name] {
				seen[name] = true
				pkgs = append(pkgs, name)
			}
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// runShim runs a wrapped command, contained or not. It wraps runShimTraced so
// that every exit path -- refusal, sandbox, direct -- lands in one run record.
func runShim(cmdName string, args []string, nvxHome string) int {
	trace := beginRunTrace(nvxHome, cmdName, args)
	code := runShimTraced(trace, cmdName, args, nvxHome)
	trace.finish(code)
	return code
}

func runShimTraced(trace *runTrace, cmdName string, args []string, nvxHome string) int {
	opts := parseShimOptions(args)
	args = opts.args
	opts.strictFlag = strictFlag
	opts.standardFlag = standardFlag

	hintIfShadowed(nvxHome)

	if err := ensureProjectPolicyTrust(nvxHome); err != nil {
		LogError("Failed to load security policy: %v", err)
		return 1
	}
	policy, err := LoadPolicy(nvxHome)
	if err != nil {
		LogError("Failed to load security policy: %v", err)
		return 1
	}

	// Refuse a contained global install BEFORE verifying anything.
	//
	// The refusal is deterministic -- it depends only on the flags -- while
	// verification hits the network, can take seconds, and can stop to ask whether
	// to proceed despite known vulnerabilities. Running it first meant a user was
	// shown a vulnerability prompt, answered it, and only then learned the command
	// could never have run. Reported from real use 2026-08-20.
	//
	// Ordered before verification rather than merged into the block below because
	// that block is inside shouldSandbox, which is where the refusal belongs: a
	// global install is fine when isolation is off.
	contain := shouldSandbox(cmdName, args, policy, opts)
	if contain && isGlobalInstall(cmdName, args) {
		trace.note(runModeRefused, "global install cannot be contained")
		refuseContainedGlobalInstall(cmdName)
		return 1
	}

	switch cmdName {
	case "npm", "yarn", "pnpm", "npx", "bun", "bunx":
		if pkgs := detectShimPackagesForVerification(cmdName, args); len(pkgs) > 0 {
			// Returning rather than exiting here is what lets runShim record the
			// abort. A blocked or refused install is a run, and the one a later
			// review most wants to find.
			if code := runVerifyInstall(pkgs, nvxHome); code != 0 {
				trace.note(runModeRefused, "blocked by pre-install verification")
				return code
			}
		}
	}
	if cmdName == "npm" || cmdName == "yarn" || cmdName == "pnpm" {
		if cwd, err := os.Getwd(); err == nil {
			if root := findProjectRoot(cwd); root != "" {
				if err := generateProjectBinShims(root, nvxHome); err != nil {
					LogWarn("Failed to refresh project bin shims: %v", err)
				}
			}
		}
	}

	if contain {
		trace.note(runModeSandboxed, "")
		if opts.payloadNoSandbox {
			LogInfo("--no-sandbox is ignored when passed to a wrapped command. To run without isolation, use: nvx --no-sandbox %s ...", cmdName)
		}
		if isGlobalInstall(cmdName, args) {
			// Normally unreachable: the same check runs before verification above.
			// Kept so the rule holds if that early return is ever moved.
			refuseContainedGlobalInstall(cmdName)
			return 1
		}
		toolName := ""
		if tool, wantsPersistence := trustedToolCandidate(cmdName, args); wantsPersistence {
			if ensureTrustedToolGrant(nvxHome, tool) {
				toolName = tool
			}
		}
		return runSandbox(SandboxConfig{
			NvxHome:            nvxHome,
			Command:            cmdName,
			Args:               args,
			FilesystemProvider: opts.filesystemProvider,
			ToolName:           toolName,
		})
	}

	// Uncontained is the "your own code" path (run scripts, publish, whoami,
	// ...): whether that's true or not is exactly what a security-conscious
	// user needs to see, not infer from an absent sandbox banner. But a single
	// typed command routinely spawns a whole tree of further nvx-shimmed
	// processes (an npm lifecycle script alone can nest prepublishOnly ->
	// build -> clean -> node, each a distinct process) — unlike the sandboxed
	// path, whose guest PATH never includes nvx's own shim dir, so nothing it
	// spawns re-enters nvx's classification at all. Only the outermost
	// invocation in that tree should report its own status.
	//
	// trace.top rather than a fresh isTopLevelShimInvocation(): that call marks
	// the process tree as claimed, so asking twice in one invocation answers
	// "no" the second time and the status line disappears.
	trace.note(runModeDirect, describeSandboxSkip(cmdName, args, policy, opts))
	if trace.isTop() {
		LogInfo("Running directly (not sandboxed): %s %s", cmdName, strings.Join(args, " "))
	}

	rt := runtimeForShim(cmdName)
	activeVer := getActiveShellVersionFor(nvxHome, rt.Name())
	if activeVer == "" {
		activeVer = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}

	binaryPath := resolvePinnedCommandPath(cmdName, nvxHome, activeVer, rt)
	if binaryPath == "" {
		if p := resolveProjectBinCommand(cmdName); p != "" {
			binaryPath = p
		}
	}
	if binaryPath == "" {
		var err error
		binaryPath, err = lookPathSkippingNvxShims(cmdName, nvxHome)
		if err != nil {
			LogError("Could not find real executable for %s", cmdName)
			return 1
		}
	}
	binaryPath = preferWindowsRuntimeExe(binaryPath)

	cmd := exec.Command(binaryPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		LogError("Failed to execute %s: %v", cmdName, err)
		return 1
	}
	return 0
}

// ToBashPath converts a Windows path to Git Bash path format (e.g. C:\Users -> /c/Users)
func ToBashPath(winPath string) string {
	winPath = filepath.Clean(winPath)
	if len(winPath) >= 2 && winPath[1] == ':' {
		drive := strings.ToLower(string(winPath[0]))
		rest := strings.ReplaceAll(winPath[2:], "\\", "/")
		return "/" + drive + rest
	}
	return strings.ReplaceAll(winPath, "\\", "/")
}

// FormatPathForShell formats the PATH string for the specific shell
func FormatPathForShell(shell, rawPath string) string {
	if shell == "bash" || shell == "zsh" {
		if runtime.GOOS == "windows" {
			parts := filepath.SplitList(rawPath)
			var bashParts []string
			for _, p := range parts {
				bashParts = append(bashParts, ToBashPath(p))
			}
			return strings.Join(bashParts, ":")
		}
		// On non-windows, rawPath is already colon-separated and doesn't need Windows-to-UNIX translation
		return strings.ReplaceAll(rawPath, ";", ":")
	}
	return rawPath
}

func shellEnvAssignment(shell, key, value string) string {
	if shell == "bash" || shell == "zsh" {
		return "export " + key + "=" + quotePOSIXShell(value) + "\n"
	}
	return "$env:" + key + " = " + quotePowerShell(value) + "\n"
}

func quotePOSIXShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// PackageJSON is the minimal structure needed to extract the node version engines
type PackageJSON struct {
	Engines struct {
		Node string `json:"node"`
		Bun  string `json:"bun"`
	} `json:"engines"`
	Volta struct {
		Node string `json:"node"`
	} `json:"volta"`
}

// CleanEngineRange parses and cleans a semver engine range into a simple version query
func CleanEngineRange(raw string) string {
	raw = strings.TrimSpace(raw)
	prefixes := []string{">=", "<=", ">", "<", "^", "~", "="}
	for {
		matched := false
		for _, p := range prefixes {
			if strings.HasPrefix(raw, p) {
				raw = strings.TrimPrefix(raw, p)
				raw = strings.TrimSpace(raw)
				matched = true
			}
		}
		if !matched {
			break
		}
	}

	if idx := strings.Index(raw, " "); idx != -1 {
		raw = raw[:idx]
	}
	if parts := strings.Split(raw, "||"); len(parts) > 1 {
		raw = strings.TrimSpace(parts[0])
	}

	raw = strings.ReplaceAll(raw, ".x", "")
	raw = strings.ReplaceAll(raw, ".X", "")
	raw = strings.ReplaceAll(raw, ".*", "")

	return strings.TrimPrefix(raw, "v")
}

// DetectVersionConfig scans the current directory and ascends to root looking for Node version indicators
func DetectVersionConfig(startDir string) (version string, sourceFile string, err error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		dir = startDir
	}

	for {
		// 1. .nvmrc
		nvmrc := filepath.Join(dir, ".nvmrc")
		if info, err := os.Stat(nvmrc); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(nvmrc); err == nil {
				return strings.TrimSpace(string(content)), nvmrc, nil
			}
		}

		// 2. .node-version
		nodeVersion := filepath.Join(dir, ".node-version")
		if info, err := os.Stat(nodeVersion); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(nodeVersion); err == nil {
				return strings.TrimSpace(string(content)), nodeVersion, nil
			}
		}

		// 3. package.json
		pkgJSON := filepath.Join(dir, "package.json")
		if info, err := os.Stat(pkgJSON); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(pkgJSON); err == nil {
				var pkg PackageJSON
				if err := json.Unmarshal(content, &pkg); err == nil {
					if pkg.Volta.Node != "" {
						return pkg.Volta.Node, pkgJSON, nil
					}
					if pkg.Engines.Node != "" {
						return CleanEngineRange(pkg.Engines.Node), pkgJSON, nil
					}
				}
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", "", nil
}

// refuseContainedGlobalInstall explains why -g cannot run contained, and how to
// run it deliberately.
//
// The suggested command names nvx itself, which is only useful if `nvx` is on
// PATH. The installer puts nvx.exe in ~/.nvx/bin alongside the shims, so it
// normally is -- but a source build that ran `init-shims` leaves shims pointing at
// the build tree with no nvx.exe installed, and then this advice cannot be
// followed. See installedNvxHint.
func refuseContainedGlobalInstall(cmdName string) {
	LogError("Global installs (-g) can't run inside the sandbox.")
	LogInfo("They need write access to a location every future nvx invocation trusts, which the sandbox deliberately never grants — a contained install must not be able to plant something that later runs un-contained.")
	LogInfo("Run it without OS isolation instead:  %s --no-sandbox %s ...", installedNvxHint(), cmdName)
}

// installedNvxHint returns how to invoke nvx in a message the user will retype.
//
// "nvx" is right when it is on PATH. When it is not -- a source build, or an
// install whose bin directory is not on PATH -- printing "nvx" tells the user to
// run a command their shell cannot find, which is what happened in the report this
// came from. Fall back to the absolute path of the running binary, which is always
// correct even if it is less pretty.
func installedNvxHint() string {
	if _, err := exec.LookPath("nvx"); err == nil {
		return "nvx"
	}
	if self, err := os.Executable(); err == nil {
		return quoteForShellHint(self)
	}
	return "nvx"
}

func quoteForShellHint(path string) string {
	if strings.ContainsAny(path, " \t") {
		return `"` + path + `"`
	}
	return path
}

// stableShimTarget returns the nvx path shims should invoke, installing a copy at
// that path when needed.
//
// Shims used to embed os.Executable() -- whichever binary happened to run
// `init-shims`. Do that from a build tree and every shim depends on a file that
// gets rebuilt, moved or deleted, so `npm` starts failing with a missing-file
// error that names a path in someone's source directory. Observed three times in
// one session on the machine this was written on: each smoke-test run repointed
// the real ~/.nvx/bin shims at a repo build, which was then deleted.
//
// The installer already places nvx beside the shims, so that path is the stable
// one and shims point there instead. Copying self into it also repairs an install
// whose nvx.exe is missing -- the state that made `nvx --no-sandbox ...` advice
// unfollowable, because `nvx` was not on PATH at all.
func stableShimTarget(nvxHome string) string {
	self, err := os.Executable()
	if err != nil {
		// Nothing better to offer: a bare name at least works wherever nvx is on
		// PATH, which is the normal installed case.
		return "nvx"
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}

	target := filepath.Join(nvxHome, "bin", nvxExecutableName())
	if sameExistingFile(self, target) {
		return target // already running from the stable location
	}

	if err := installNvxCopy(self, target); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			// A usable nvx is already there and could not be replaced -- typically
			// because another nvx is running it. Pointing at it keeps the shims
			// stable, which matters more than it being this exact build.
			LogWarn("Could not update %s (%v); shims will use the copy already there.", target, err)
			return target
		}
		// No stable copy and none could be made. Fall back to the running binary
		// so shims work now, and say why they may not later.
		LogWarn("Could not install nvx at %s (%v); shims will point at %s instead.", target, err, self)
		LogInfo("If that file moves or is deleted, the shims stop working. Re-run 'nvx init-shims' from an installed nvx to repair them.")
		return self
	}
	return target
}

func nvxExecutableName() string {
	if runtime.GOOS == "windows" {
		return "nvx.exe"
	}
	return "nvx"
}

// sameExistingFile reports whether two paths are the same file on disk.
func sameExistingFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// installNvxCopy places a copy of src at dst, via a temporary name so a
// concurrent run never observes a half-written binary and so replacing one that
// is in use fails cleanly rather than truncating it.
func installNvxCopy(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", dst, os.Getpid())
	// NOT copyLogFile: that caps at 8 MB to stop a contained process filling the
	// disk with a "log". nvx is larger than that, so reusing it silently installed
	// a truncated, unrunnable binary -- caught only by checking the copy's size
	// was exactly the cap. A binary copy must be complete or fail.
	if err := copyWholeFile(src, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// copyWholeFile copies src to dst in full, and verifies the result matches the
// source size before reporting success.
//
// The size check is not belt-and-braces: the first version of installNvxCopy
// reused a size-capped log copier and produced a silently truncated nvx.exe that
// would have failed at exec time, far from the cause. A partial binary must be an
// error here, not a surprise later.
func copyWholeFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	srcInfo, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != srcInfo.Size() {
		return fmt.Errorf("copied %d of %d bytes", written, srcInfo.Size())
	}
	return nil
}
