package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// version is the nvx build version. It is injected at build time via
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
//
// and falls back to the module's embedded VCS info, then to "dev".
var version = "dev"

// nvxVersion resolves the effective version string for `nvx version`.
func nvxVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return "dev+" + s.Value[:7]
			}
		}
	}
	return "dev"
}

var yesFlag = false

func init() {
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "-y" || os.Args[i] == "--yes" {
			yesFlag = true
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			i--
			continue
		}
		if os.Args[i] == "--no-sandbox" {
			noSandboxFlag = true
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
		fmt.Println("nvx version " + nvxVersion())
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
		useVersion := ""
		for _, arg := range os.Args[2:] {
			if !strings.HasPrefix(arg, "-") {
				useVersion = arg
				break
			}
		}
		if useVersion == "" {
			LogError("Please specify a version to use. Example: nvx use 20")
			os.Exit(1)
		}
		runUse(useVersion, nvxHome, parseShellArg(os.Args[2:]))

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
		runEnv(parseShellArg(os.Args[2:]), nvxHome)

	case "auto":
		runAuto(nvxHome, parseShellArg(os.Args[2:]))

	case "verify-install":
		if len(os.Args) < 3 {
			LogError("Please specify one or more packages to verify. Example: nvx verify-install lodash express")
			os.Exit(1)
		}
		runVerifyInstall(os.Args[2:], nvxHome)

	case "init-shims":
		generateShims(nvxHome)
		if cwd, err := os.Getwd(); err == nil {
			if root := findProjectRoot(cwd); root != "" {
				if err := generateProjectBinShims(root, nvxHome); err != nil {
					LogWarn("Failed to generate project bin shims: %v", err)
				} else {
					LogSuccess("Generated project bin shims in %s", projectBinDir(root))
				}
			}
		}
		LogSuccess("Generated PATH shims in ~/.nvx/bin")

	case "policy":
		if len(os.Args) < 3 {
			LogError("Usage: nvx policy init [--global] [--project] [--force]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "init":
			os.Exit(runPolicyInit(os.Args[3:], nvxHome))
		default:
			LogError("Unknown policy subcommand: %s", os.Args[2])
			os.Exit(1)
		}

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

	case "upgrade", "self-update":
		checkOnly := false
		for _, a := range os.Args[2:] {
			if a == "--check" {
				checkOnly = true
			}
		}
		os.Exit(runUpgrade(checkOnly))

	case "doctor":
		runDoctor(nvxHome)
		maybeNotifyUpdate(nvxHome)

	case "which":
		if len(os.Args) < 3 {
			LogError("Usage: nvx which <command>  (e.g. nvx which node)")
			os.Exit(1)
		}
		os.Exit(runWhich(os.Args[2], nvxHome))

	case "current":
		runCurrent(nvxHome)

	case "__landlock-exec":
		guestHome, workDir, nvxHome, networkMode, shimCommand, proxyPort, cmdPath, cmdArgs, ok := parseLandlockExecArgs(os.Args[2:])
		if !ok {
			LogError("Invalid __landlock-exec arguments")
			os.Exit(1)
		}
		os.Exit(runLandlockExecChild(guestHome, workDir, nvxHome, networkMode, shimCommand, proxyPort, cmdPath, cmdArgs))

	case "help", "-h", "--help":
		printHelp()

	default:
		LogError("Unknown command: %s", command)
		printHelp()
		os.Exit(1)
	}
}

// defaultShell returns the shell whose syntax is emitted when none is specified.
func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "bash"
}

// parseShellArg extracts the target shell from trailing command arguments.
// It accepts "--shell=bash", "--shell bash", and a bare positional "bash".
func parseShellArg(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--shell=") {
			if v := strings.TrimPrefix(arg, "--shell="); v != "" {
				return v
			}
		} else if arg == "--shell" && i+1 < len(args) {
			return args[i+1]
		} else if !strings.HasPrefix(arg, "-") {
			switch strings.ToLower(arg) {
			case "powershell", "pwsh", "bash", "zsh":
				return strings.ToLower(arg)
			}
		}
	}
	return defaultShell()
}

func printHelp() {
	fmt.Println(`nvx - A modern, secure, cross-platform runtime version manager

Usage:
  nvx <command> [arguments]

A runtime is selected with a "<runtime>@<version>" prefix; a bare version uses
the default runtime (node). Registered runtimes: node, bun. Run "nvx doctor".

Commands:
  install <ver>          Install a runtime version (e.g. 20, lts, latest, bun@1.1)
  uninstall <ver>        Remove an installed runtime version
  use <ver>              Switch runtime version in the current terminal session
  default <ver>          Set the global default version (creates a link)
  list, ls               List installed versions across all runtimes
  list-remote, ls-remote List available Node.js versions from nodejs.org
  current                Show the active and default versions
  which <cmd>            Print the real binary nvx resolves for a command
  doctor                 Show runtime + isolation providers, availability, and policy
  upgrade [--check]      Update nvx to the latest release (checksum-verified)
  env [--shell=<type>]   Print shell integration script (powershell, bash, zsh)
  auto [--shell=<type>]  Auto-switch version based on .nvmrc / .node-version / package.json
  verify-install <pkgs>  Verify package safety before installing (called by wrappers)
  init-shims             Generate PATH shims in ~/.nvx/bin (and project bin shims when in a Node project)
  policy init            Scaffold ~/.nvx/policy.json and/or .nvx-policy.json
  cleanup                Remove stale sandbox sessions from previous runs

Options:
  --shell=<type>         Specify shell type: 'powershell', 'bash', 'zsh'
  --isolation-provider=<name>   Override the isolation backend (alias: --filesystem-provider)
  --no-sandbox           Disable sandbox for this shim invocation
  -y, --yes              Auto-approve all prompts

Examples:
  nvx install lts
  nvx install bun@1.1
  nvx use 20.11.0
  nvx default 18.16.0
  nvx doctor

Extending nvx: add custom runtime or isolation providers — see docs/EXTENDING.md`)
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
	// Prefer the real semver comparator (handles prerelease precedence correctly);
	// fall back to a lenient numeric compare for non-semver strings.
	if a, err1 := parseSemver(v1); err1 == nil {
		if b, err2 := parseSemver(v2); err2 == nil {
			return compareSemver(a, b)
		}
	}

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

	// Range expressions (^, ~, >=, x-ranges, ||, engines-style) pick the
	// highest locally-installed version that satisfies the range.
	if isRangeExpr(query) {
		if best := maxSatisfyingVersion(versions, query); best != "" {
			return best, nil
		}
		return "", fmt.Errorf("no installed version satisfies range '%s'", query)
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
	provider, verQuery := ResolveRuntimeSelector(query)
	err := provider.Install(verQuery, nvxHome)
	if err != nil {
		LogError("Installation failed: %v", err)
		os.Exit(1)
	}
}

func runUninstall(query string, nvxHome string) {
	provider, verQuery := ResolveRuntimeSelector(query)
	err := provider.Uninstall(verQuery, nvxHome)
	if err != nil {
		LogError("Uninstallation failed: %v", err)
		os.Exit(1)
	}
}

func runUse(query string, nvxHome string, shell string) {
	provider, verQuery := ResolveRuntimeSelector(query)
	label := runtimeLabel(provider.Name())
	resolvedVer, err := resolveLocalVersion(provider, verQuery, nvxHome)
	if err != nil {
		promptMsg := fmt.Sprintf("%s %s is not installed. Would you like to download and install it now?", label, verQuery)
		if PromptYesNo(promptMsg) {
			if ierr := provider.Install(verQuery, nvxHome); ierr != nil {
				LogError("Installation failed: %v", ierr)
				os.Exit(1)
			}
			resolvedVer, err = resolveLocalVersion(provider, verQuery, nvxHome)
			if err != nil {
				LogError("Failed to resolve newly installed version: %v", err)
				os.Exit(1)
			}
		} else {
			LogError("Could not find installed version matching '%s': %v", verQuery, err)
			os.Exit(1)
		}
	}

	targetDir := filepath.Join(nvxHome, "versions", provider.Name(), resolvedVer)
	emitSessionEnv(shell, nvxHome, targetDir)

	activeVer := getActiveShellVersion(nvxHome)
	if activeVer != "" && activeVer != resolvedVer {
		LogSuccess("%s swapped: %s ➔ %s (active in this shell)", label, activeVer, resolvedVer)
	} else {
		LogSuccess("Now using %s %s in this terminal.", label, resolvedVer)
	}
}

func runDefault(query string, nvxHome string) {
	provider, verQuery := ResolveRuntimeSelector(query)
	resolvedVer, err := resolveLocalVersion(provider, verQuery, nvxHome)
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

// runtimeLabel returns a human-friendly display name for a runtime.
func runtimeLabel(name string) string {
	switch name {
	case "node":
		return "Node.js"
	case "bun":
		return "Bun"
	default:
		if name == "" {
			return name
		}
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

func runList(nvxHome string) {
	activeVer := getActiveShellVersion(nvxHome)
	defaultVer := getGlobalDefaultVersion(nvxHome)
	any := false

	for _, name := range RuntimeNames() {
		provider := Providers[name]
		versions, err := provider.ListLocal(nvxHome)
		if err != nil {
			LogWarn("Failed to list %s versions: %v", runtimeLabel(name), err)
			continue
		}
		if len(versions) == 0 {
			continue
		}
		any = true
		fmt.Printf("\x1b[36mInstalled %s versions:\x1b[0m\n", runtimeLabel(name))
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

	if !any {
		LogWarn("No runtimes installed. Try 'nvx install 20' (Node.js) or 'nvx install bun'.")
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// runWhich prints the real binary nvx would execute for a shimmed command.
func runWhich(cmd, nvxHome string) int {
	rt := runtimeForShim(cmd)
	ver := getActiveShellVersion(nvxHome)
	if ver == "" {
		ver = getGlobalDefaultVersion(nvxHome)
	}
	if ver == "" {
		// Nothing active/default: fall back to the latest installed version of
		// the command's runtime so `which` is useful right after install.
		if locals, _ := rt.ListLocal(nvxHome); len(locals) > 0 {
			ver = getLatestLocal(locals)
		}
	}
	if p := resolvePinnedCommandPath(cmd, nvxHome, ver, rt); p != "" {
		fmt.Println(p) // version-pinned by nvx
		return 0
	}
	if p := resolveProjectBinCommand(cmd); p != "" {
		fmt.Printf("%s  (project node_modules/.bin)\n", p)
		return 0
	}
	if p, err := lookPathSkippingNvxShims(cmd, nvxHome); err == nil {
		// Resolved from the ambient PATH — nvx audits/sandboxes it but does NOT
		// version-pin it (e.g. yarn/pnpm; pin those via Corepack).
		fmt.Printf("%s  (ambient PATH — not version-pinned by nvx)\n", p)
		return 0
	}
	LogError("No binary found for %q", cmd)
	return 1
}

// runCurrent prints the active and default runtime versions.
func runCurrent(nvxHome string) {
	fmt.Printf("active (this shell): %s\n", orNone(getActiveShellVersion(nvxHome)))
	fmt.Printf("global default:      %s\n", orNone(getGlobalDefaultVersion(nvxHome)))
}

// runDoctor reports the registered runtime and isolation providers, their
// availability on this host, and the effective policy — a single place for
// users (and contributors of custom providers) to confirm what is wired in.
func runDoctor(nvxHome string) {
	fmt.Printf("nvx %s  (%s/%s)\n\n", nvxVersion(), runtime.GOOS, runtime.GOARCH)

	fmt.Println("\x1b[36mRuntime providers:\x1b[0m")
	for _, name := range RuntimeNames() {
		p := Providers[name]
		locals, _ := p.ListLocal(nvxHome)
		// Distinguish version-pinned shims from ambient-PATH passthrough (only
		// determinable when a version is installed to probe ResolveBinary).
		ver := ""
		if len(locals) > 0 {
			ver = getLatestLocal(locals)
		}
		var pinned, passthrough []string
		for _, cmd := range p.ShimCommands() {
			if ver != "" && p.ResolveBinary(cmd, nvxHome, ver) == "" {
				passthrough = append(passthrough, cmd)
			} else {
				pinned = append(pinned, cmd)
			}
		}
		fmt.Printf("  %-8s installed: %-2d  pinned: %s\n", name, len(locals), strings.Join(pinned, ", "))
		if len(passthrough) > 0 {
			fmt.Printf("           passthrough (audited/sandboxed, not version-pinned): %s\n", strings.Join(passthrough, ", "))
		}
	}

	fmt.Println("\n\x1b[36mIsolation providers:\x1b[0m")
	for _, name := range IsolationProviderNames() {
		p, _ := GetIsolationProvider(name)
		status := "\x1b[90munavailable\x1b[0m"
		if p.Available() {
			status = "\x1b[32mavailable\x1b[0m"
		}
		fmt.Printf("  %-14s %-22s %s\n", name, status, p.Description())
	}

	policy, _ := LoadPolicy(nvxHome)
	fmt.Println("\n\x1b[36mEffective policy:\x1b[0m")
	fmt.Printf("  isolation.enabled:      %v\n", policy.Isolation.Enabled)
	fmt.Printf("  isolation.provider:     %s\n", policy.IsolationProviderName())
	fmt.Printf("  network.mode:           %s\n", policy.Isolation.Network.Mode)
	fmt.Printf("  enforce_ignore_scripts: %v\n", policy.EnforceIgnoreScripts)
	fmt.Printf("  fail_closed:            %v\n", policy.FailClosed)
	fmt.Printf("  typosquatting.enabled:  %v\n", policy.Typosquatting.Enabled)

	fmt.Println("\n\x1b[36mActive:\x1b[0m")
	fmt.Printf("  shell version:  %s\n", orNone(getActiveShellVersion(nvxHome)))
	fmt.Printf("  global default: %s\n", orNone(getGlobalDefaultVersion(nvxHome)))
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
		fmt.Printf(`__nvx_shell_type() {
    if [ -n "$ZSH_VERSION" ]; then
        echo "zsh"
    else
        echo "bash"
    fi
}

nvx() {
    local cmd="$1"
    if [ "$cmd" = "use" ] || [ "$cmd" = "auto" ]; then
        local stdout
        stdout=$(%q "$@" --shell="$(__nvx_shell_type)")
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
        stdout=$(%q auto --shell="$(__nvx_shell_type)")
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
        stdout=$(%q auto --shell=zsh)
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
`, exePath, exePath, exePath, exePath)
	} else {
		// PowerShell default
		fmt.Printf(`$global:__nvx_last_pwd = ""

function nvx {
    $cmd = $args[0]
    if ($cmd -eq "use" -or $cmd -eq "auto") {
        $stdout = & %q @args --shell=powershell
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
        $stdout = & %q auto --shell=powershell
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
	emitSessionEnv(shell, nvxHome, targetDir)
}

// resolveNpmPrefixDir returns the npm global prefix for the session: the
// project-local .nvx/npm_global dir when isolated tools are enabled by policy,
// otherwise the per-version npm_global dir shared across projects.
func resolveNpmPrefixDir(nvxHome, targetVersionDir string) string {
	policy, err := LoadPolicy(nvxHome)
	if err == nil && policy.Environment.IsolatedTools && policy.ProjectDir != "" {
		prefixDir := filepath.Join(policy.ProjectDir, ".nvx", "npm_global")
		if mkErr := os.MkdirAll(prefixDir, 0755); mkErr == nil {
			return prefixDir
		}
		LogWarn("Failed to create project tools directory %s; falling back to version-level npm prefix.", prefixDir)
	}
	return filepath.Join(targetVersionDir, "npm_global")
}

// shellSingleQuote renders s as a POSIX-shell single-quoted literal, safe to
// embed in output that the shell wrapper eval's. This prevents a hostile PATH
// entry (containing ", `, or $()) from injecting code into the calling shell.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// powershellSingleQuote renders s as a PowerShell single-quoted literal.
func powershellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// emitSessionEnv prints the shell statements that activate a Node version
// (and its npm prefix) for the current terminal session.
func emitSessionEnv(shell, nvxHome, targetDir string) {
	npmPrefixDir := resolveNpmPrefixDir(nvxHome, targetDir)
	newPath := CleanAndBuildPath(os.Getenv("PATH"), nvxHome, targetDir, npmPrefixDir)
	formattedPath := FormatPathForShell(shell, newPath)
	formattedNpmPrefix := FormatPathForShell(shell, npmPrefixDir)

	if shell == "bash" || shell == "zsh" {
		fmt.Printf("export PATH=%s\n", shellSingleQuote(formattedPath))
		fmt.Printf("export NPM_CONFIG_PREFIX=%s\n", shellSingleQuote(formattedNpmPrefix))
	} else {
		fmt.Printf("$env:PATH = %s\n", powershellSingleQuote(formattedPath))
		fmt.Printf("$env:NPM_CONFIG_PREFIX = %s\n", powershellSingleQuote(formattedNpmPrefix))
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

	// Fail closed when no interactive TTY is available. Auto-approving in CI
	// would silently bypass security prompts (vulnerability warnings, install
	// script confirmations) in exactly the environments where supply-chain
	// attacks land. Non-interactive users must opt in via -y or NVX_YES=true.
	if runtime.GOOS == "windows" {
		ttyOut, err = os.OpenFile("CONOUT$", os.O_WRONLY, 0)
		if err != nil {
			LogWarn("Non-interactive environment: denying prompt. Use -y / --yes or set NVX_YES=true to approve automatically. Prompt was: %s", message)
			return false
		}
		defer ttyOut.Close()

		ttyIn, err = os.OpenFile("CONIN$", os.O_RDONLY, 0)
		if err != nil {
			LogWarn("Non-interactive environment: denying prompt. Use -y / --yes or set NVX_YES=true to approve automatically. Prompt was: %s", message)
			return false
		}
		defer ttyIn.Close()
	} else {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			LogWarn("Non-interactive environment: denying prompt. Use -y / --yes or set NVX_YES=true to approve automatically. Prompt was: %s", message)
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

// failClosedOrWarn aborts the install when policy.FailClosed is set (so a
// registry/OSV outage cannot silently disable checks), otherwise it warns and
// lets the caller continue in a clearly-labeled degraded mode.
func failClosedOrWarn(policy Policy, format string, a ...interface{}) {
	if policy.FailClosed {
		LogError(format+" — aborting (fail_closed policy).", a...)
		os.Exit(1)
	}
	LogWarn(format+" Proceeding in degraded mode (set \"fail_closed\": true to block).", a...)
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
			failClosedOrWarn(policy, "Could not resolve registry metadata for %s: %v.", pkgName, err)
			continue
		}

		// 3. Installation Script Execution Check
		if hasScripts {
			LogWarn("Package %s@%s contains installation scripts (preinstall/postinstall/install).", pkgName, resolvedVer)
			LogWarn("Malicious packages often execute rogue code during the install phase.")
			if policy.EnforceIgnoreScripts {
				// The shim injects --ignore-scripts into the real invocation
				// (see runShim), so hooks are actually disabled, not just warned about.
				LogWarn("enforce_ignore_scripts is set: install scripts will be disabled via --ignore-scripts.")
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

		// 5. Registry signature / provenance verification (ECDSA over
		// name@version:integrity, keys from the registry). Applied to explicitly
		// named packages, where it is cheap and highest-value.
		if !verifyPackageProvenance(policy, pkgName, resolvedVer) {
			LogError("Installation aborted: registry signature verification failed for %s.", pkgName)
			os.Exit(1)
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
			failClosedOrWarn(policy, "Vulnerability database scan failed: %v.", err)
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
