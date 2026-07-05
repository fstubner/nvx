package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

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

// GetVersionBinDir returns the directory containing the node executable for a given version folder
func GetVersionBinDir(versionDir string) string {
	if runtime.GOOS == "windows" {
		return versionDir
	}
	return filepath.Join(versionDir, "bin")
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

	for _, part := range parts {
		if part == "" {
			continue
		}
		normPart := filepath.Clean(part)
		normVersionsDir := filepath.Clean(versionsDir)
		normCurrentLink := filepath.Clean(currentLink)
		normCurrentLinkBin := filepath.Clean(currentLinkBin)
		normCurrentLinkNpm := filepath.Clean(currentLinkNpm)

		// Remove any specific v* version paths or npm_global paths inside versions Dir
		if strings.HasPrefix(strings.ToLower(normPart), strings.ToLower(normVersionsDir)+string(os.PathSeparator)) {
			continue
		}
		// Also clean the .nvx\current path and default npm_global paths if we are setting a terminal version
		if strings.ToLower(normPart) == strings.ToLower(normCurrentLink) ||
			strings.ToLower(normPart) == strings.ToLower(normCurrentLinkBin) ||
			strings.ToLower(normPart) == strings.ToLower(normCurrentLinkNpm) {
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

	// Prepend the new target version directory and the npm prefix bin directory
	if targetVersionDir != "" {
		binDir := GetVersionBinDir(targetVersionDir)
		if npmPrefixDir == "" {
			npmPrefixDir = filepath.Join(targetVersionDir, "npm_global")
		}
		npmBinDir := GetNpmPrefixBinDir(npmPrefixDir)
		cleaned = append([]string{npmBinDir, binDir}, cleaned...)
	}

	// Global nvx shims first, then project node_modules/.bin shims, then runtime paths.
	var prefix []string
	prefix = append(prefix, shimDir)
	if cwd, err := os.Getwd(); err == nil {
		if root := findProjectRoot(cwd); root != "" {
			pb := projectBinDir(root)
			if _, err := os.Stat(pb); err == nil {
				prefix = append(prefix, pb)
			}
		}
	}
	cleaned = append(prefix, cleaned...)

	return strings.Join(cleaned, string(filepath.ListSeparator))
}

// lookPathSkippingNvxShims resolves cmdName on PATH with ~/.nvx/bin removed so
// shim wrappers (node.cmd) are not mistaken for the real runtime binary.
func lookPathSkippingNvxShims(cmdName, nvxHome string) (string, error) {
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

	exePath, err := os.Executable()
	if err != nil {
		exePath = "nvx"
	}
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

// detectInstallPackages scans package manager arguments for an install-style
// subcommand and returns the package names being installed. The subcommand is
// the first non-flag argument, so leading flags (e.g. `npm --loglevel=error
// install pkg`) cannot bypass detection.
func detectInstallPackages(args []string) []string {
	subIdx := -1
	for i, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			subIdx = i
			break
		}
	}
	if subIdx == -1 || !installAliases[args[subIdx]] {
		return nil
	}

	var pkgs []string
	for _, arg := range args[subIdx+1:] {
		if !strings.HasPrefix(arg, "-") {
			pkgs = append(pkgs, arg)
		}
	}
	return pkgs
}

func detectShimPackagesForVerification(cmdName string, args []string) []string {
	switch strings.ToLower(cmdName) {
	case "npm", "yarn", "pnpm":
		if pkgs := detectInstallPackages(args); len(pkgs) > 0 {
			return pkgs
		}
		sub := firstNonFlagArg(args)
		if sub == "ci" || installAliases[sub] {
			if pkgs := packagesFromPackageLock(); len(pkgs) > 0 {
				return pkgs
			}
			return packagesFromPackageJSON()
		}
	case "npx", "bunx":
		return detectExecutorPackages(args)
	}
	return nil
}

func firstNonFlagArg(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
		if flagTakesValue(arg) && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
		}
	}
	return ""
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

func flagTakesValue(arg string) bool {
	switch arg {
	case "-p", "--package", "--prefix", "--registry", "--cache", "-c", "--call", "--shell":
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

func runShim(cmdName string, args []string, nvxHome string) int {
	opts := parseShimOptions(args)
	args = opts.args

	if err := ensureProjectPolicyTrust(nvxHome); err != nil {
		LogError("Failed to load security policy: %v", err)
		return 1
	}
	policy, err := LoadPolicy(nvxHome)
	if err != nil {
		LogError("Failed to load security policy: %v", err)
		return 1
	}

	if cmdName == "npm" || cmdName == "yarn" || cmdName == "pnpm" || cmdName == "npx" || cmdName == "bunx" {
		if pkgs := detectShimPackagesForVerification(cmdName, args); len(pkgs) > 0 {
			runVerifyInstall(pkgs, nvxHome)
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

	if shouldSandbox(cmdName, policy, opts) {
		return runSandbox(SandboxConfig{
			NvxHome:            nvxHome,
			Command:            cmdName,
			Args:               args,
			FilesystemProvider: opts.filesystemProvider,
		})
	}

	rt := runtimeForShim(cmdName)
	nodeVer := getActiveShellVersion(nvxHome)
	if nodeVer == "" {
		nodeVer = getGlobalDefaultVersion(nvxHome)
	}

	binaryPath := resolvePinnedCommandPath(cmdName, nvxHome, nodeVer, rt)
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
