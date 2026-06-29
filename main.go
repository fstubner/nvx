package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var yesFlag = false

func init() {
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "-y" || os.Args[i] == "--yes" {
			yesFlag = true
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			i--
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	command := strings.ToLower(os.Args[1])
	nvxHome := GetHomeDir()

	switch command {
	case "version", "-v", "--version":
		fmt.Println("nvx version 0.1.0")
		return


	case "install", "i":

		if len(os.Args) < 3 {
			LogError("Please specify a version to install. Example: nvx install 20")
			os.Exit(1)
		}
		runInstall(os.Args[2], nvxHome)

	case "uninstall", "uni":
		if len(os.Args) < 3 {
			LogError("Please specify a version to uninstall. Example: nvx uninstall 18.16.0")
			os.Exit(1)
		}
		runUninstall(os.Args[2], nvxHome)

	case "use":
		if len(os.Args) < 3 {
			LogError("Please specify a version to use. Example: nvx use 20")
			os.Exit(1)
		}
		shell := "powershell"
		if len(os.Args) >= 4 && strings.HasPrefix(os.Args[3], "--shell=") {
			shell = strings.TrimPrefix(os.Args[3], "--shell=")
		} else if len(os.Args) >= 4 {
			shell = os.Args[3]
		}
		runUse(os.Args[2], nvxHome, shell)

	case "default":
		if len(os.Args) < 3 {
			LogError("Please specify a version to set as default. Example: nvx default 20")
			os.Exit(1)
		}
		runDefault(os.Args[2], nvxHome)

	case "list", "ls":
		runList(nvxHome)

	case "list-remote", "ls-remote":
		runListRemote()

	case "env":
		shell := "powershell"
		if len(os.Args) >= 3 && strings.HasPrefix(os.Args[2], "--shell=") {
			shell = strings.TrimPrefix(os.Args[2], "--shell=")
		} else if len(os.Args) >= 3 {
			shell = os.Args[2]
		}
		runEnv(shell, nvxHome)

	case "auto":
		shell := "powershell"
		if len(os.Args) >= 3 && strings.HasPrefix(os.Args[2], "--shell=") {
			shell = strings.TrimPrefix(os.Args[2], "--shell=")
		} else if len(os.Args) >= 3 {
			shell = os.Args[2]
		}
		runAuto(nvxHome, shell)

	case "verify-install":
		if len(os.Args) < 3 {
			os.Exit(0)
		}
		runVerifyInstall(os.Args[2:], nvxHome)

	case "sandbox", "exec", "s":
		if len(os.Args) < 3 {
			LogError("Please specify a command to run in the sandbox. Example: nvx s node app.js")
			os.Exit(1)
		}
		exitCode := runSandbox(SandboxConfig{
			NvxHome: nvxHome,
			Command: os.Args[2],
			Args:    os.Args[3:],
		})
		os.Exit(exitCode)


	case "init-shims":
		generateShims(nvxHome)
		LogSuccess("Generated PATH shims in ~/.nvx/bin")

	case "shim":
		if len(os.Args) < 3 {
			LogError("Please specify a command to shim")
			os.Exit(1)
		}
		exitCode := runShim(os.Args[2], os.Args[3:], nvxHome)
		os.Exit(exitCode)

	case "cleanup":
		LogInfo("Cleaning up stale sandbox sessions...")
		cleanupStaleSandboxes(nvxHome)
		LogSuccess("Sandbox cleanup complete.")

	case "help", "-h", "--help":
		printHelp()

	default:
		LogError("Unknown command: %s", command)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`nvx - A modern, secure, cross-platform runtime version manager

Usage:
  nvx <command> [arguments]

Commands:
  install <version>      Download and install a Node.js version (e.g. 20, lts, latest)
  uninstall <version>    Remove an installed Node.js version
  use <version>          Switch Node.js version in the current terminal session
  default <version>      Set the global default Node.js version (creates a link)
  list, ls               List all installed Node.js versions
  list-remote, ls-remote List available Node.js versions from nodejs.org
  env [--shell=<type>]   Print shell integration script (powershell, bash, zsh)
  auto [--shell=<type>]  Auto-switch version based on .nvmrc / .node-version / package.json
  verify-install <pkgs>  Verify package safety before installing (called by wrappers)
  sandbox, s <cmd> [arg] Run a command inside the nvx sandbox (env isolation + OS primitives)
  init-shims             Generate PATH shims in ~/.nvx/bin
  shim <cmd> [args]      Internal shim router for package managers
  cleanup                Remove stale sandbox sessions from previous runs

Options:
  --shell=<type>         Specify shell type: 'powershell', 'bash', 'zsh'

Examples:
  nvx install lts
  nvx use 20.11.0
  nvx s npx create-react-app my-app
  nvx default 18.16.0`)
}

// UI Logging helpers (stderr)
func LogSuccess(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "\x1b[32m✔\x1b[0m "+format+"\n", a...)
}

func LogInfo(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "\x1b[36mℹ\x1b[0m "+format+"\n", a...)
}

func LogWarn(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "\x1b[33m⚠\x1b[0m "+format+"\n", a...)
}

func LogError(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "\x1b[31m✘\x1b[0m "+format+"\n", a...)
}

func CompareVersions(v1, v2 string) int {
	v1Clean := strings.TrimPrefix(strings.ToLower(v1), "v")
	v2Clean := strings.TrimPrefix(strings.ToLower(v2), "v")

	parts1 := strings.Split(v1Clean, ".")
	parts2 := strings.Split(v2Clean, ".")

	for i := 0; i < 3; i++ {
		var p1, p2 int
		if i < len(parts1) {
			fmt.Sscanf(parts1[i], "%d", &p1)
		}
		if i < len(parts2) {
			fmt.Sscanf(parts2[i], "%d", &p2)
		}
		if p1 > p2 {
			return 1
		}
		if p1 < p2 {
			return -1
		}
	}
	return 0
}

func getLatestLocal(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	latest := versions[0]
	for _, v := range versions[1:] {
		if CompareVersions(v, latest) > 0 {
			latest = v
		}
	}
	return latest
}

func resolveLocalVersion(provider RuntimeProvider, query string, nvxHome string) (string, error) {
	versions, err := provider.ListLocal(nvxHome)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no %s versions are currently installed", provider.Name())
	}

	query = strings.TrimSpace(strings.ToLower(query))
	if query == "latest" || query == "current" {
		return getLatestLocal(versions), nil
	}

	q := query
	if !strings.HasPrefix(q, "v") {
		q = "v" + q
	}

	for _, v := range versions {
		if strings.ToLower(v) == q {
			return v, nil
		}
	}

	var matches []string
	for _, v := range versions {
		vLower := strings.ToLower(v)
		if vLower == q || strings.HasPrefix(vLower, q+".") {
			matches = append(matches, v)
		}
	}

	if len(matches) > 0 {
		return getLatestLocal(matches), nil
	}

	return "", fmt.Errorf("no installed version matches query '%s'", query)
}

func getActiveShellVersion(nvxHome string) string {
	currentPath := os.Getenv("PATH")
	parts := filepath.SplitList(currentPath)
	versionsDir := filepath.Clean(filepath.Join(nvxHome, "versions"))

	for _, part := range parts {
		if part == "" {
			continue
		}
		normPart := filepath.Clean(part)
		if strings.HasPrefix(strings.ToLower(normPart), strings.ToLower(versionsDir)+string(os.PathSeparator)) {
			rel, err := filepath.Rel(versionsDir, normPart)
			if err == nil {
				pathParts := strings.Split(rel, string(os.PathSeparator))
				for _, subPart := range pathParts {
					if strings.HasPrefix(subPart, "v") {
						return subPart
					}
				}
			}
		}
	}
	return ""
}

func getGlobalDefaultVersion(nvxHome string) string {
	currentLink := GetCurrentLinkPath()
	target, err := os.Readlink(currentLink)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func getOS() string {
	if runtime.GOOS == "windows" {
		return "win"
	}
	return runtime.GOOS
}

func getExtension() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
}

func runInstall(query string, nvxHome string) {
	provider := Providers["node"]
	err := provider.Install(query, nvxHome)
	if err != nil {
		LogError("Installation failed: %v", err)
		os.Exit(1)
	}
}

func runUninstall(query string, nvxHome string) {
	provider := Providers["node"]
	err := provider.Uninstall(query, nvxHome)
	if err != nil {
		LogError("Uninstallation failed: %v", err)
		os.Exit(1)
	}
}

func runUse(query string, nvxHome string, shell string) {
	provider := Providers["node"]
	resolvedVer, err := resolveLocalVersion(provider, query, nvxHome)
	if err != nil {
		promptMsg := fmt.Sprintf("Node.js %s is not installed. Would you like to download and install it now?", query)
		if PromptYesNo(promptMsg) {
			runInstall(query, nvxHome)
			resolvedVer, err = resolveLocalVersion(provider, query, nvxHome)
			if err != nil {
				LogError("Failed to resolve newly installed version: %v", err)
				os.Exit(1)
			}
		} else {
			LogError("Could not find installed version matching '%s': %v", query, err)
			os.Exit(1)
		}
	}

	targetDir := filepath.Join(nvxHome, "versions", provider.Name(), resolvedVer)
	currentPath := os.Getenv("PATH")

	newPath := CleanAndBuildPath(currentPath, nvxHome, targetDir)
	formattedPath := FormatPathForShell(shell, newPath)

	npmGlobalDir := filepath.Join(targetDir, "npm_global")
	formattedNpmGlobal := FormatPathForShell(shell, npmGlobalDir)

	if shell == "bash" || shell == "zsh" {
		fmt.Printf("export PATH=\"%s\"\n", formattedPath)
		fmt.Printf("export NPM_CONFIG_PREFIX=\"%s\"\n", formattedNpmGlobal)
	} else {
		fmt.Printf("$env:PATH = \"%s\"\n", formattedPath)
		fmt.Printf("$env:NPM_CONFIG_PREFIX = \"%s\"\n", formattedNpmGlobal)
	}

	activeVer := getActiveShellVersion(nvxHome)
	if activeVer != "" && activeVer != resolvedVer {
		LogSuccess("Node.js swapped: %s ➔ %s (active in this shell)", activeVer, resolvedVer)
	} else {
		LogSuccess("Now using Node.js %s in this terminal.", resolvedVer)
	}
}

func runDefault(query string, nvxHome string) {
	provider := Providers["node"]
	resolvedVer, err := resolveLocalVersion(provider, query, nvxHome)
	if err != nil {
		LogError("Could not find installed version matching '%s': %v", query, err)
		os.Exit(1)
	}

	targetDir := filepath.Join(nvxHome, "versions", provider.Name(), resolvedVer)
	currentLink := GetCurrentLinkPath()

	err = CreateLink(currentLink, targetDir)
	if err != nil {
		LogError("Failed to set default version: %v", err)
		os.Exit(1)
	}

	LogSuccess("Global default version set to %s.", resolvedVer)
	LogInfo("Make sure '%s' is added to your environment PATH.", GetVersionBinDir(currentLink))
}

func runList(nvxHome string) {
	provider := Providers["node"]
	versions, err := provider.ListLocal(nvxHome)
	if err != nil {
		LogError("Failed to list installed versions: %v", err)
		os.Exit(1)
	}

	if len(versions) == 0 {
		LogWarn("No Node.js versions are installed. Run 'nvx install <version>' first.")
		return
	}

	activeVer := getActiveShellVersion(nvxHome)
	defaultVer := getGlobalDefaultVersion(nvxHome)

	fmt.Println("\x1b[36mInstalled Node.js versions:\x1b[0m")
	for _, v := range versions {
		prefix := "  "
		suffix := ""

		if v == activeVer {
			prefix = "\x1b[32m* \x1b[0m"
			suffix += " \x1b[32m(active in this shell)\x1b[0m"
		}
		if v == defaultVer {
			suffix += " \x1b[33m(global default)\x1b[0m"
		}

		fmt.Printf("%s%s%s\n", prefix, v, suffix)
	}
}

func runListRemote() {
	LogInfo("Fetching remote release list from nodejs.org...")
	releases, err := FetchReleases()
	if err != nil {
		LogError("Error fetching releases: %v", err)
		os.Exit(1)
	}

	var majorSeen = make(map[string]bool)
	var filtered []Release

	for _, r := range releases {
		parts := strings.Split(strings.TrimPrefix(r.Version, "v"), ".")
		if len(parts) == 0 {
			continue
		}
		major := parts[0]
		if !majorSeen[major] {
			majorSeen[major] = true
			filtered = append(filtered, r)
		}
		if len(filtered) >= 12 {
			break
		}
	}

	fmt.Println("\n\x1b[36mLatest release of each major Node.js version:\x1b[0m")
	fmt.Printf("%-10s  %-12s  %-15s  %-8s\n", "Version", "Release Date", "LTS Status", "Npm version")
	fmt.Println(strings.Repeat("-", 55))

	for _, r := range filtered {
		ltsStr := "No"
		if r.IsLTS() {
			ltsStr = fmt.Sprintf("Yes (%s)", r.LTSName())
		}
		fmt.Printf("%-10s  %-12s  %-15s  %-8s\n", r.Version, r.Date, ltsStr, r.Npm)
	}
	fmt.Println("\nRun 'nvx install <version>' to download any of these versions.")
}

func runEnv(shell string, nvxHome string) {
	generateShims(nvxHome)

	exePath, err := os.Executable()
	if err != nil {
		exePath = "nvx"
	}
	exePath = strings.ReplaceAll(exePath, "\\", "/")

	if shell == "bash" || shell == "zsh" {
		fmt.Printf(`nvx() {
    local cmd="$1"
    if [ "$cmd" = "use" ] || [ "$cmd" = "auto" ]; then
        local stdout
        stdout=$(%q "$@")
        if [ -n "$stdout" ]; then
            eval "$stdout"
        fi
    else
        %q "$@"
    fi
}

nvx_prompt_hook() {
    local exit_code=$?
    if [ "$PWD" != "$__nvx_last_pwd" ]; then
        export __nvx_last_pwd="$PWD"
        local stdout
        stdout=$(%q auto --shell %s)
        if [ -n "$stdout" ]; then
            eval "$stdout"
        fi
    fi
    return $exit_code
}

if [[ -n "$ZSH_VERSION" ]]; then
    # Optimize using native zsh chpwd hook instead of prompt command
    nvx_chpwd_hook() {
        local stdout
        stdout=$(%q auto --shell zsh)
        if [ -n "$stdout" ]; then
            eval "$stdout"
        fi
    }
    autoload -U add-zsh-hook
    add-zsh-hook chpwd nvx_chpwd_hook
elif [[ -n "$BASH_VERSION" ]]; then
    if [[ ! "$PROMPT_COMMAND" =~ nvx_prompt_hook ]]; then
        PROMPT_COMMAND="nvx_prompt_hook; $PROMPT_COMMAND"
    fi
fi
`, exePath, exePath, exePath, shell, exePath)
	} else {
		// PowerShell default
		fmt.Printf(`$global:__nvx_last_pwd = ""

function nvx {
    $cmd = $args[0]
    if ($cmd -eq "use" -or $cmd -eq "auto") {
        $stdout = & %q @args
        if ($stdout) {
            $stdout | Out-String | Invoke-Expression
        }
    } else {
        & %q @args
    }
}

function nvx_prompt_hook {
    if ($global:__nvx_last_pwd -ne $pwd) {
        $global:__nvx_last_pwd = $pwd
        $stdout = & %q auto --shell powershell
        if ($stdout) {
            $stdout | Out-String | Invoke-Expression
        }
    }
}

if (Test-Path Function:\prompt) {
    $old_prompt = $function:prompt
    $function:prompt = {
        nvx_prompt_hook
        . $old_prompt
    }
} else {
    $function:prompt = {
        nvx_prompt_hook
        "PS $pwd> "
    }
}
`, exePath, exePath, exePath)
	}
}

func runAuto(nvxHome string, shell string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	query, sourceFile, err := DetectVersionConfig(cwd)
	if err != nil || query == "" {
		return
	}

	provider := Providers["node"]
	resolvedVer, err := resolveLocalVersion(provider, query, nvxHome)
	if err != nil {
		promptMsg := fmt.Sprintf("Directory requires Node.js %s (from %s), but it is not installed. Install it now?", query, filepath.Base(sourceFile))
		if PromptYesNo(promptMsg) {
			runInstall(query, nvxHome)
			resolvedVer, err = resolveLocalVersion(provider, query, nvxHome)
			if err != nil {
				LogError("[nvx] Failed to resolve newly installed version: %v", err)
				return
			}
		} else {
			LogWarn("[nvx] Directory requires Node.js %s (from %s) but it is not installed.", query, filepath.Base(sourceFile))
			LogWarn("[nvx] Run 'nvx install %s' to install it.", query)
			return
		}
	}

	activeVer := getActiveShellVersion(nvxHome)
	if activeVer == resolvedVer {
		return
	}

	LogInfo("[nvx] Found %s: switching to Node.js %s", filepath.Base(sourceFile), resolvedVer)

	targetDir := filepath.Join(nvxHome, "versions", provider.Name(), resolvedVer)
	currentPath := os.Getenv("PATH")
	newPath := CleanAndBuildPath(currentPath, nvxHome, targetDir)
	formattedPath := FormatPathForShell(shell, newPath)

	npmGlobalDir := filepath.Join(targetDir, "npm_global")
	formattedNpmGlobal := FormatPathForShell(shell, npmGlobalDir)

	if shell == "bash" || shell == "zsh" {
		fmt.Printf("export PATH=\"%s\"\n", formattedPath)
		fmt.Printf("export NPM_CONFIG_PREFIX=\"%s\"\n", formattedNpmGlobal)
	} else {
		fmt.Printf("$env:PATH = \"%s\"\n", formattedPath)
		fmt.Printf("$env:NPM_CONFIG_PREFIX = \"%s\"\n", formattedNpmGlobal)
	}
}

// PromptYesNo prints a message to the console TTY and reads a Y/N keypress, bypassing standard redirections.
func PromptYesNo(message string) bool {
	if yesFlag {
		return true
	}
	if os.Getenv("NVX_YES") == "true" || os.Getenv("NVX_YES") == "1" {
		return true
	}

	var ttyIn, ttyOut *os.File
	var err error

	if runtime.GOOS == "windows" {
		ttyOut, err = os.OpenFile("CONOUT$", os.O_WRONLY, 0)
		if err != nil {
			LogWarn("Non-TTY environment detected. Proceeding automatically in CI or aborting. Use -y / --yes or set NVX_YES=true to bypass. Prompt was: %s", message)
			// In standard CI if we can't open TTY, check if CI variable is set.
			// For safety reasons, default to true for version installs, but for package vulnerability checks, maybe false.
			// Actually, let's treat CI=true as auto-approving unless NVX_STRICT=true is set. Let's do:
			if os.Getenv("CI") == "true" || os.Getenv("CI") == "1" {
				return true
			}
			return false
		}
		defer ttyOut.Close()

		ttyIn, err = os.OpenFile("CONIN$", os.O_RDONLY, 0)
		if err != nil {
			if os.Getenv("CI") == "true" || os.Getenv("CI") == "1" {
				return true
			}
			return false
		}
		defer ttyIn.Close()
	} else {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			LogWarn("Non-TTY environment detected. Proceeding automatically in CI or aborting. Use -y / --yes or set NVX_YES=true to bypass. Prompt was: %s", message)
			if os.Getenv("CI") == "true" || os.Getenv("CI") == "1" {
				return true
			}
			return false
		}
		defer tty.Close()
		ttyIn = tty
		ttyOut = tty
	}

	fmt.Fprintf(ttyOut, "\x1b[33m?\x1b[0m %s [Y/n]: ", message)

	var buf [10]byte
	n, err := ttyIn.Read(buf[:])
	if err != nil || n == 0 {
		return false
	}

	char := strings.ToLower(string(buf[0]))
	if char == "y" || buf[0] == '\r' || buf[0] == '\n' {
		return true
	}
	return false
}

// parsePackageQuery splits a package install query (e.g. lodash@4.17.21 or @types/node@18.0.0)
func parsePackageQuery(query string) (string, string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", ""
	}

	isScoped := false
	if strings.HasPrefix(query, "@") {
		isScoped = true
		query = query[1:]
	}

	parts := strings.Split(query, "@")
	name := parts[0]
	if isScoped {
		name = "@" + name
	}

	version := ""
	if len(parts) > 1 {
		version = parts[1]
	}
	return name, version
}

// runVerifyInstall verifies packages against policy blocklists, typosquatting, and the real-time OSV CVE database.
func runVerifyInstall(args []string, nvxHome string) {
	policy, err := LoadPolicy(nvxHome)
	if err != nil {
		LogWarn("Failed to load security policy: %v. Bypassing blocklist.", err)
	}

	popularList := LoadPopularPackages(nvxHome)
	var osvQueries []OSVQuery

	for _, arg := range args {
		pkgName, versionQuery := parsePackageQuery(arg)
		if pkgName == "" {
			continue
		}

		// 1. Policy Blocklist Check
		if policy.IsBlocked(pkgName) {
			LogError("Blocked by security policy: Package %q is blacklisted.", pkgName)
			os.Exit(1)
		}

		// 2. Typosquatting Check
		if policy.Typosquatting.Enabled {
			isTrusted := false
			for _, t := range policy.Typosquatting.TrustedPackages {
				if strings.ToLower(pkgName) == strings.ToLower(t) {
					isTrusted = true
					break
				}
			}

			if !isTrusted {
				maxDist := policy.Typosquatting.MaxDistance
				if maxDist <= 0 {
					maxDist = 2
				}
				if suspect := CheckTyposquattingAuthority(pkgName, popularList, maxDist); suspect != "" {
					pkgDownloads, _ := GetWeeklyDownloads(pkgName)
					suspectDownloads, _ := GetWeeklyDownloads(suspect)

					var msg string
					if suspectDownloads > 0 {
						msg = fmt.Sprintf("Package %q is suspiciously close to popular package %q (edit distance <= %d).\n"+
							"    - %s: %d weekly downloads\n"+
							"    - %s: %d weekly downloads\n"+
							"  This is a high-probability typosquatting threat. Proceed anyway?",
							pkgName, suspect, maxDist, pkgName, pkgDownloads, suspect, suspectDownloads)
					} else {
						msg = fmt.Sprintf("Package %q is suspiciously close to popular package %q (edit distance <= %d). Typo threat? Proceed anyway?",
							pkgName, suspect, maxDist)
					}

					if !PromptYesNo(msg) {
						LogError("Installation aborted by user due to typosquatting risk.")
						os.Exit(1)
					}
				}
			}
		}

		LogInfo("Verifying package %q...", pkgName)
		resolvedVer, pubTime, hasScripts, err := ResolveNpmPackageDetails(pkgName, versionQuery)
		if err != nil {
			LogWarn("Could not resolve registry metadata for %s: %v. Bypassing metadata checks.", pkgName, err)
			continue
		}

		// 3. Installation Script Execution Check
		if hasScripts {
			LogWarn("Package %s@%s contains installation scripts (preinstall/postinstall/install).", pkgName, resolvedVer)
			LogWarn("Malicious packages often execute rogue code during the install phase.")
			if policy.EnforceIgnoreScripts {
				LogError("Blocked by security policy: Package scripts are disallowed. Please run with --ignore-scripts.")
				os.Exit(1)
			} else {
				msg := fmt.Sprintf("Package %s@%s contains install scripts. Run these scripts on your host?", pkgName, resolvedVer)
				if !PromptYesNo(msg) {
					LogError("Installation aborted by user due to script execution warning.")
					os.Exit(1)
				}
			}
		}

		// 4. Release Age Check (24-hour supply chain window)
		if !pubTime.IsZero() {
			age := time.Since(pubTime)
			if age < 24*time.Hour {
				msg := fmt.Sprintf("Package %s@%s was published only %.1f hours ago (on %s). Supply chain compromises are often caught within 24 hours. Proceed?",
					pkgName, resolvedVer, age.Hours(), pubTime.Format("2006-01-02 15:04:05"))
				if !PromptYesNo(msg) {
					LogError("Installation aborted by user due to release age warning.")
					os.Exit(1)
				}
			}
		}

		osvQueries = append(osvQueries, OSVQuery{
			Package: OSVPackage{Name: pkgName, Ecosystem: "npm"},
			Version: resolvedVer,
		})
	}

	// 5. Batch Vulnerability Scan (CVEs / OSV database)
	if len(osvQueries) > 0 {
		LogInfo("Scanning OSV database for known vulnerabilities...")
		vulns, err := ScanVulnerabilitiesBatch(osvQueries)
		if err != nil {
			LogWarn("Vulnerability database scan failed: %v. Bypassing CVE checks.", err)
		} else if len(vulns) > 0 {
			LogError("Vulnerability Scan Alert: Found active vulnerabilities!")
			for pkgKey, list := range vulns {
				fmt.Fprintf(os.Stderr, "  \x1b[31m●\x1b[0m %s:\n", pkgKey)
				for _, v := range list {
					fmt.Fprintf(os.Stderr, "    - %s: %s\n", v.ID, v.Summary)
				}
			}
			fmt.Fprintln(os.Stderr)
			if !PromptYesNo("Proceed with installation despite active vulnerabilities?") {
				LogError("Installation aborted due to active package vulnerabilities.")
				os.Exit(1)
			}
		} else {
			LogSuccess("Vulnerability scan clean. No active CVEs found.")
		}
	}
}
