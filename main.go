package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var yesFlag = false
var quietFlag = false
var agentModeFlag = false

// exposePortsFlag holds --expose values, which add to whatever
// isolation.network.expose_ports already lists rather than replacing it.
//
// A package-level variable, like quietFlag above, rather than a sixth value out
// of parseStartupFlags: that already returns five booleans and every caller
// spells out the ones it does not want.
var exposePortsFlag []string

// connectPortsFlag holds --connect values: host services the sandbox may reach.
var connectPortsFlag []string

func init() {
	var yes, noSandbox, strict, standard bool
	os.Args, yes, noSandbox, strict, standard = parseStartupFlags(os.Args)
	yesFlag = yes
	noSandboxFlag = noSandbox
	// If both are passed, fail toward more containment, not less.
	strictFlag = strict
	standardFlag = standard && !strict
	if os.Getenv("NVX_QUIET") == "1" || strings.EqualFold(os.Getenv("NVX_QUIET"), "true") {
		quietFlag = true
	}
	if os.Getenv("NVX_AGENT_MODE") == "1" || strings.EqualFold(os.Getenv("NVX_AGENT_MODE"), "true") {
		agentModeFlag = true
		yesFlag = true
		// Documented as "-y -q"; it only ever did the -y half. quietFlag gates
		// success and info lines only -- warnings and errors still print, so this
		// hides progress chatter and not security output.
		quietFlag = true
	}
}

// addExposePortFlag records one --expose value. A bad one is refused loudly
// rather than ignored: the developer asked for a port to be reachable, and
// silently not publishing it leaves them debugging a server that looks fine.
func addExposePortFlag(v string) {
	if _, err := parseExposeSpec(v); err != nil {
		LogError("--expose: %v.", err)
		LogInfo("Use --expose <port-inside-the-sandbox> or --expose <inside>:<host>, e.g. --expose 5173:8080.")
		os.Exit(1)
	}
	exposePortsFlag = append(exposePortsFlag, v)
}

// addConnectPortFlag records one --connect value.
func addConnectPortFlag(v string) {
	if _, err := parseConnectSpec(v); err != nil {
		LogError("--connect: %v.", err)
		LogInfo("Use --connect <port-on-your-machine> or --connect <host>:<in-sandbox>, e.g. --connect 9222:19222.")
		os.Exit(1)
	}
	connectPortsFlag = append(connectPortsFlag, v)
}

func parseStartupFlags(args []string) ([]string, bool, bool, bool, bool) {
	if len(args) <= 1 {
		return args, false, false, false, false
	}
	filtered := []string{args[0]}
	yes := false
	noSandbox := false
	strict := false
	standard := false
	i := 1
	for ; i < len(args); i++ {
		switch args[i] {
		case "-y", "--yes":
			yes = true
		case "-q", "--quiet":
			quietFlag = true
		case "--agent-mode":
			agentModeFlag = true
			yes = true
			quietFlag = true // documented as "-y -q"
		case "--no-sandbox":
			noSandbox = true
		case "--strict":
			strict = true
		case "--standard":
			standard = true
		default:
			// --expose=<port>, and --expose <port>. Both spellings, because the
			// other valued flag nvx accepts (--filesystem-provider=) uses the
			// first and every developer's muscle memory from docker -p uses the
			// second.
			if v, ok := strings.CutPrefix(args[i], "--expose="); ok {
				addExposePortFlag(v)
				continue
			}
			if args[i] == "--expose" && i+1 < len(args) {
				addExposePortFlag(args[i+1])
				i++
				continue
			}
			if v, ok := strings.CutPrefix(args[i], "--connect="); ok {
				addConnectPortFlag(v)
				continue
			}
			if args[i] == "--connect" && i+1 < len(args) {
				addConnectPortFlag(args[i+1])
				i++
				continue
			}
			filtered = append(filtered, args[i:]...)
			return filtered, yes, noSandbox, strict, standard
		}
	}
	return filtered, yes, noSandbox, strict, standard
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	command := strings.ToLower(os.Args[1])
	nvxHome := GetHomeDir()

	if command == "help" && len(os.Args) >= 3 {
		if text := commandHelpText(strings.ToLower(os.Args[2])); text != "" {
			fmt.Print(text)
			return
		}
	}
	if len(os.Args) >= 3 && (os.Args[2] == "--help" || os.Args[2] == "-h") {
		if text := commandHelpText(command); text != "" {
			fmt.Print(text)
			return
		}
	}

	switch command {
	case "version", "-v", "--version":
		fmt.Println("nvx version " + appVersion)
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

	case "import":
		source := "all"
		if len(os.Args) >= 3 {
			source = os.Args[2]
		}
		runImport(source, nvxHome)

	case "verify-install":
		if len(os.Args) < 3 {
			LogError("Usage: nvx verify-install <package> [package...]")
			os.Exit(1)
		}
		code, _ := runVerifyInstall(os.Args[2:], nvxHome)
		os.Exit(code)

	case "init-shims":
		if err := generateShims(nvxHome); err != nil {
			LogError("Failed to generate PATH shims: %v", err)
			os.Exit(1)
		}
		if cwd, err := os.Getwd(); err == nil {
			if root := findProjectRoot(cwd); root != "" {
				if err := generateProjectBinShims(root, nvxHome); err != nil {
					LogWarn("Failed to generate project bin shims: %v", err)
				} else {
					LogSuccess("Generated project bin shims in %s", projectBinDir(root, nvxHome))
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
		removed, skipped := cleanupStaleSandboxes(nvxHome, 0)
		if skipped > 0 {
			LogInfo("Left %d sandbox session(s) alone because they are still running.", skipped)
		}
		if removed > 0 {
			LogInfo("Removed %d stale sandbox session(s).", removed)
		}
		// Staged supervisor copies from other nvx builds. Reclaimed here rather
		// than on the launch path, where deleting one could race a sandbox about
		// to execute it.
		pruneUnusedSupervisors(nvxHome)
		LogSuccess("Sandbox cleanup complete.")

	case "doctor":
		fixPath := false
		for _, a := range os.Args[2:] {
			if a == "--fix" {
				fixPath = true
			}
		}
		os.Exit(runDoctor(nvxHome, fixPath))

	case "grants":
		if len(os.Args) < 3 {
			LogError("Usage: nvx grants list | nvx grants reset [--all]")
			os.Exit(1)
		}
		os.Exit(runGrants(os.Args[2:], nvxHome))

	case "audit":
		os.Exit(runAuditCommand(os.Args[2:], nvxHome))

	case "setup":
		undo := false
		for _, a := range os.Args[2:] {
			if a == "--undo" || a == "-u" {
				undo = true
			}
		}
		os.Exit(runWindowsSetup(nvxHome, undo))

	case "__landlock-exec":
		a, ok := parseSupervisorExecArgs(os.Args[2:])
		if !ok {
			LogError("Invalid __landlock-exec arguments")
			os.Exit(1)
		}
		os.Exit(runLandlockExecChild(a))

	case "__appcontainer-exec":
		a, ok := parseSupervisorExecArgs(os.Args[2:])
		if !ok {
			LogError("Invalid __appcontainer-exec arguments")
			os.Exit(1)
		}
		os.Exit(runAppContainerExecChild(a))

	case "help", "-h", "--help":
		printHelp()

	default:
		// Direct nvx invocation of a wrapped command, e.g.
		// `nvx --no-sandbox npx wrangler login`. This is the explicit way to
		// disable isolation (a --no-sandbox smuggled through the PATH shim is
		// ignored). It also lets users run a wrapped command through nvx directly.
		if isShimCommand(command) {
			os.Exit(runShim(command, os.Args[2:], nvxHome))
		}
		LogError("Unknown command: %s", command)
		printHelp()
		os.Exit(1)
	}
}

// isShimCommand reports whether name is a package manager / runtime command that
// nvx wraps (npm, npx, node, bun, ...).
func isShimCommand(name string) bool {
	for _, c := range allShimCommands() {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}

func commandHelpText(command string) string {
	switch command {
	case "install", "i":
		return "nvx install <[runtime@]version>\n\nDownload and install a runtime version. A bare version installs Node.js\n(e.g. 20, lts, latest, 20.11.0); prefix another runtime with '@'\n(e.g. bun@1.2, bun).\n"
	case "uninstall", "uni":
		return "nvx uninstall <[runtime@]version>\n\nRemove an installed runtime version. Refuses to remove the active shell\nversion or global default.\n"
	case "use":
		return "nvx use <[runtime@]version> [--shell=<powershell|bash|zsh>]\n\nEmit shell commands that switch the current terminal session to the\nrequested runtime version (defaults to Node.js for a bare version).\n"
	case "default":
		return "nvx default <[runtime@]version>\n\nSet the global default version link for a runtime.\n"
	case "env":
		return "nvx env [--shell=<powershell|bash|zsh>]\n\nPrint shell integration code. Installers normally add this to your shell profile.\n"
	case "auto":
		return "nvx auto [--shell=<powershell|bash|zsh>]\n\nDetect .nvmrc, .node-version, package.json engines, or Volta config and switch the current shell when needed.\n"
	case "verify-install":
		return "nvx verify-install <package> [package...]\n\nInternal security verifier used by shims. Checks policy blocklists, typosquatting, install scripts, release age, and OSV vulnerabilities.\n"
	case "policy":
		return "nvx policy init [--global] [--project] [--force]\n\nCreate a global or project .nvx policy file. Includes isolation.level\n(\"standard\" or \"strict\") — standard contains installs and ad-hoc tool runs;\nstrict also contains your own code. Override per-invocation with\nnvx --strict/--standard.\n"
	case "init-shims":
		return "nvx init-shims\n\nGenerate PATH shims in ~/.nvx/bin and project-bin shims for node_modules/.bin when run in a Node project.\n"
	case "shim":
		return "nvx shim <cmd> [args...]\n\nInternal shim router used by generated command wrappers.\n"
	case "cleanup":
		return `nvx cleanup

Reclaim disk from interrupted runs now, rather than waiting.

You should not need this. Every nvx run already reclaims a few abandoned
sandbox profiles after the command finishes, skipping any whose owning process
is still alive. This does the same thing without a limit, and additionally
prunes staged sandbox supervisors from other nvx builds -- which is not done
automatically, because a supervisor another nvx has staged but not yet executed
cannot be told apart from an unused one.
`
	case "doctor":
		return "nvx doctor [--fix]\n\nCheck that ~/.nvx/bin is first on PATH so nvx intercepts node/npm/npx/bun.\nRegenerates shims and reports what is wrong.\n\n--fix  Also repair a shadowed persistent PATH (Windows). That edits your\n       user PATH, so nvx does not do it unless you ask.\n"
	case "audit":
		return `nvx audit [--runs] [--failures] [--summary] [--limit=N] [--all]

Show what nvx has recorded locally in ~/.nvx/audit.log. Nothing is ever sent
anywhere.

Security decisions -- egress denials, grants, policy pins -- are always
recorded. Per-run records (what ran, whether it was contained, why not, exit
code, duration, warnings) are a debugging aid and are OFF by default. Turn them
on for a session with NVX_TRACE=1.

--summary   Counts instead of lines: how often runs were contained, why not
            when they were not, and which warnings keep firing.
`
	case "grants":
		return "nvx grants list\nnvx grants reset [--all]\n\nInspect or forget the approve-once grants recorded for the current project\n(or every project, with --all): egress hosts, trusted tools, and trusted\nproject policy files. Grants live under ~/.nvx/grants, never in the project.\n"
	}
	return ""
}

// defaultShell returns the shell whose syntax is emitted when none is specified.
// defaultShell guesses the shell that will evaluate nvx's output.
//
// On Windows it used to answer "powershell" unconditionally, so `nvx use 20` in
// Git Bash emitted PowerShell assignments that bash cannot evaluate. Nothing
// applied, `node -v` was unchanged, and nvx still printed "Now using Node.js
// v20" -- a success message for something that did not happen. Auto-switch on
// `cd` never fired there either.
//
// MSYSTEM is set by Git Bash and MSYS2 and by little else; a SHELL naming bash or
// zsh is the fallback for other POSIX emulations. Both are heuristics, and
// `--shell=` still overrides, which is what the shell integration snippets pass.
func defaultShell() string {
	if runtime.GOOS != "windows" {
		return "bash"
	}
	if os.Getenv("MSYSTEM") != "" {
		return "bash"
	}
	sh := strings.ToLower(os.Getenv("SHELL"))
	if strings.Contains(sh, "bash") {
		return "bash"
	}
	if strings.Contains(sh, "zsh") {
		return "zsh"
	}
	return "powershell"
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
	fmt.Println(helpText())
}

// helpText is separated from printing so a test can compare it against the same
// list in README.md. The two are hand-maintained copies and had already drifted:
// the README was missing doctor, grants, import, setup and shim.
func helpText() string {
	return `nvx - A modern, secure, cross-platform runtime version manager

Usage:
  nvx <command> [arguments]

Runtimes: Node.js and Bun. A bare version is Node.js (nvm-compatible);
prefix Bun with '@' (e.g. bun@1.2).

Commands:
  install <[rt@]version>   Download and install a runtime version (e.g. 20, lts, bun@1.2)
  uninstall <[rt@]version> Remove an installed runtime version
  use <[rt@]version>       Switch the current terminal session to a runtime version
  default <[rt@]version>   Set the global default for a runtime (creates a link)
  list, ls                 List installed runtimes and versions
  list-remote, ls-remote   List available Node.js versions from nodejs.org
  env [--shell=<type>]     Print shell integration script (powershell, bash, zsh)
  auto [--shell=<type>]    Auto-switch runtimes from .nvmrc / .node-version /
                           .bun-version / package.json
  verify-install <pkgs>    Verify package safety before installing (called by wrappers)
  init-shims               Generate PATH shims in ~/.nvx/bin (and project bin shims in a project)
  policy init              Scaffold ~/.nvx/policy.json and/or .nvx-policy.json
  shim <cmd> [args]        Internal shim router for package managers
  cleanup                  Reclaim disk from interrupted runs now (rarely needed;
                           every run reclaims some automatically)
  setup                    (Windows, optional) One-time elevated setup adding
                           drive-root access, and removing a loopback exemption
                           an older nvx left behind. Egress is allowlisted with
                           or without it. 'setup --undo' reverses it
  doctor [--fix]           Check that nvx intercepts node/npm/npx on PATH (--fix repairs)
  grants list              Show this project's approved egress hosts, trusted tools, and policy pins
  grants reset [--all]     Forget this project's grants (or every project's, with --all)
  audit [--summary]        Review the local record of past runs and security decisions
  import [nvm|fnm|volta]   Import Node.js versions already installed via nvm, fnm, or volta
                           (defaults to scanning all three)
  version, -v              Print version info

Options:
  --shell=<type>         Specify shell type: 'powershell', 'bash', 'zsh'
  --no-sandbox           Disable sandbox for this shim invocation. Must come
                         BEFORE the command; ignored if passed to it
  --standard             Force standard containment, overriding a project's
                         strict policy. Must come BEFORE the command
  --strict               Contain your own code too (not just installs/ad-hoc
                         tools). Must come BEFORE the command, like the two
                         above; after it, the flag belongs to the command
  --expose <in>[:<host>] (Windows) Publish a port a server inside the sandbox
                         listens on, so the host can reach it. The two numbers
                         must differ. Omit the host port to have one picked and
                         printed. Must come BEFORE the command
  --connect <host>[:<in>]  (Windows) Let the sandbox reach ONE service already
                         running on your machine, over a tunnel nvx dials. The
                         two numbers must differ; the in-sandbox one is printed
                         and set as NVX_CONNECT_<host>. Must come BEFORE the
                         command
  --filesystem-provider=<name>  Override isolation.filesystem.provider. Passed
                         TO the command: nvx npm --filesystem-provider=...
  -y, --yes              Auto-approve all prompts
  -q, --quiet            Suppress success/info messages (errors and warnings still print)
  --agent-mode           Auto-approve all prompts and suppress success/info messages
                         (equivalent to -y -q; also settable via NVX_AGENT_MODE=1)

Environment:
  NVX_TRACE=1            Record one line per run in ~/.nvx/audit.log for
                         'nvx audit'. Off by default; a local debugging aid
  NVX_YES=true           Auto-approve prompts (same as -y)
  NVX_HOME=<dir>         Use a different nvx home instead of ~/.nvx

Examples:
  nvx install lts
  nvx install bun@1.2
  nvx use 20.11.0
  nvx use bun@1.2`
}

// UI Logging helpers (stderr)
func LogSuccess(format string, a ...interface{}) {
	if quietFlag {
		return
	}
	fmt.Fprintf(os.Stderr, "\x1b[32m✔\x1b[0m "+format+"\n", a...)
}

func LogInfo(format string, a ...interface{}) {
	if quietFlag {
		return
	}
	fmt.Fprintf(os.Stderr, "\x1b[36mℹ\x1b[0m "+format+"\n", a...)
}

func LogWarn(format string, a ...interface{}) {
	// The FORMAT STRING is recorded, never the rendered text.
	//
	// Recording the rendered text put a live password in ~/.nvx/audit.log:
	// `nvx npm install https://deploy:s3cr3t@git.internal/pkg.git` reaches
	// LogWarn("Proceeding without registry metadata checks for %s.", pkgName),
	// and %s is the URL. run_trace.go goes to some trouble to keep argv out of
	// the log and this was the back door straight past it.
	//
	// The template is developer-authored and fixed at compile time, so it cannot
	// carry runtime data -- which is also what aggregation wants, since the same
	// warning about different packages is one recurring warning.
	// TestEveryLogWarnUsesALiteralFormat pins the invariant this relies on.
	recordWarning(format)
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
			if n, err := strconv.Atoi(parts1[i]); err == nil {
				p1 = n
			}
		}
		if i < len(parts2) {
			if n, err := strconv.Atoi(parts2[i]); err == nil {
				p2 = n
			}
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
	currentLink := currentLinkPath(nvxHome)
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
	provider, version := parseRuntimeSpec(query)
	err := provider.Install(version, nvxHome)
	if err != nil {
		LogError("Installation failed: %v", err)
		os.Exit(1)
	}
}

func runUninstall(query string, nvxHome string) {
	provider, version := parseRuntimeSpec(query)
	err := provider.Uninstall(version, nvxHome)
	if err != nil {
		LogError("Uninstallation failed: %v", err)
		os.Exit(1)
	}
}

func runUse(query string, nvxHome string, shell string) {
	provider, version := parseRuntimeSpec(query)
	display := runtimeDisplayName(provider.Name())
	resolvedVer, err := resolveLocalVersion(provider, version, nvxHome)
	if err != nil {
		promptMsg := fmt.Sprintf("%s %s is not installed. Would you like to download and install it now?", display, version)
		if PromptYesNo(promptMsg) {
			if instErr := provider.Install(version, nvxHome); instErr != nil {
				LogError("Installation failed: %v", instErr)
				os.Exit(1)
			}
			resolvedVer, err = resolveLocalVersion(provider, version, nvxHome)
			if err != nil {
				LogError("Failed to resolve newly installed version: %v", err)
				os.Exit(1)
			}
		} else {
			LogError("Could not find installed version matching '%s': %v", version, err)
			os.Exit(1)
		}
	}

	targetDir := filepath.Join(nvxHome, "versions", provider.Name(), resolvedVer)
	emitSessionEnv(shell, nvxHome, targetDir)

	activeVer := getActiveShellVersionFor(nvxHome, provider.Name())
	if activeVer != "" && activeVer != resolvedVer {
		LogSuccess("%s swapped: %s ➔ %s (active in this shell)", display, activeVer, resolvedVer)
	} else {
		LogSuccess("Now using %s %s in this terminal.", display, resolvedVer)
	}
}

func runDefault(query string, nvxHome string) {
	provider, version := parseRuntimeSpec(query)
	resolvedVer, err := resolveLocalVersion(provider, version, nvxHome)
	if err != nil {
		LogError("Could not find installed version matching '%s': %v", version, err)
		os.Exit(1)
	}

	targetDir := filepath.Join(nvxHome, "versions", provider.Name(), resolvedVer)
	currentLink := runtimeCurrentLinkPath(nvxHome, provider.Name())

	err = CreateLink(currentLink, targetDir)
	if err != nil {
		LogError("Failed to set default version: %v", err)
		os.Exit(1)
	}

	LogSuccess("Global default %s version set to %s.", runtimeDisplayName(provider.Name()), resolvedVer)
	LogInfo("Make sure '%s' is added to your environment PATH.", GetVersionBinDir(currentLink))
}

func runList(nvxHome string) {
	printedAny := false
	for _, name := range orderedRuntimeNames() {
		provider := Providers[name]
		versions, err := provider.ListLocal(nvxHome)
		if err != nil || len(versions) == 0 {
			continue
		}
		printedAny = true

		activeVer := getActiveShellVersionFor(nvxHome, name)
		defaultVer := getGlobalDefaultVersionFor(nvxHome, name)

		fmt.Printf("\x1b[36mInstalled %s versions:\x1b[0m\n", runtimeDisplayName(name))
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

	if !printedAny {
		LogWarn("No runtimes are installed. Run 'nvx install <version>' (Node.js) or 'nvx install bun' first.")
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
	if err := generateShims(nvxHome); err != nil {
		LogWarn("Failed to generate PATH shims: %v", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		exePath = "nvx"
	}
	fmt.Print(envScript(shell, exePath, filepath.Join(nvxHome, "bin")))
}

// envScript builds the shell integration script printed by `nvx env`. It fronts
// the shim dir on PATH (so nvx intercepts commands in every new shell) and then
// defines the `nvx` function plus directory-change auto-switch hooks. exePath is
// the nvx binary; shimDir is ~/.nvx/bin.
func envScript(shell, exePath, shimDir string) string {
	exe := strings.ReplaceAll(exePath, "\\", "/")
	prepend := shimPathPrependSnippet(shell, shimDir)

	if shell == "bash" || shell == "zsh" {
		return prepend + fmt.Sprintf(`__nvx_shell_type() {
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
`, exe, exe, exe, exe)
	}

	// PowerShell default
	return prepend + fmt.Sprintf(`$global:__nvx_last_pwd = ""

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
`, exe, exe, exe)
}

func runAuto(nvxHome string, shell string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	// Detect and switch every runtime the directory declares (e.g. .nvmrc and
	// .bun-version), building one combined PATH so Node and Bun coexist.
	pathAcc := os.Getenv("PATH")
	npmPrefix := ""
	var sessionEnv [][2]string
	changed := false

	for _, name := range orderedRuntimeNames() {
		provider := Providers[name]
		query, sourceFile, derr := provider.DetectConfig(cwd)
		if derr != nil || query == "" {
			continue
		}
		display := runtimeDisplayName(name)

		resolvedVer, rerr := resolveLocalVersion(provider, query, nvxHome)
		if rerr != nil {
			promptMsg := fmt.Sprintf("Directory requires %s %s (from %s), but it is not installed. Install it now?", display, query, filepath.Base(sourceFile))
			if PromptYesNo(promptMsg) {
				if ierr := provider.Install(query, nvxHome); ierr != nil {
					LogError("[nvx] Failed to install %s: %v", display, ierr)
					continue
				}
				resolvedVer, rerr = resolveLocalVersion(provider, query, nvxHome)
				if rerr != nil {
					continue
				}
			} else {
				LogWarn("[nvx] Directory requires %s %s (from %s) but it is not installed. Run 'nvx install %s@%s'.", display, query, filepath.Base(sourceFile), name, query)
				continue
			}
		}

		if getActiveShellVersionFor(nvxHome, name) == resolvedVer {
			continue
		}

		targetDir := filepath.Join(nvxHome, "versions", name, resolvedVer)
		prefix := ""
		if name == "node" {
			prefix = resolveNpmPrefixDir(nvxHome, targetDir)
			npmPrefix = prefix
		}
		pathAcc = CleanAndBuildPath(pathAcc, nvxHome, targetDir, prefix)
		for k, v := range provider.SessionEnv(targetDir) {
			sessionEnv = append(sessionEnv, [2]string{k, v})
		}
		changed = true
		LogInfo("[nvx] Found %s: switching to %s %s", filepath.Base(sourceFile), display, resolvedVer)
	}

	if !changed {
		return
	}
	fmt.Print(shellEnvAssignment(shell, "PATH", FormatPathForShell(shell, pathAcc)))
	if npmPrefix != "" {
		fmt.Print(shellEnvAssignment(shell, "NPM_CONFIG_PREFIX", FormatPathForShell(shell, npmPrefix)))
	}
	for _, kv := range sessionEnv {
		fmt.Print(shellEnvAssignment(shell, kv[0], kv[1]))
	}
}

// resolveNpmPrefixDir returns the npm global prefix for the session: the
// project-local .nvx/npm_global dir when isolated tools are enabled by policy,
// otherwise the per-version npm_global dir shared across projects.
func resolveNpmPrefixDir(nvxHome, targetVersionDir string) string {
	policy, err := LoadPolicy(nvxHome)
	if err == nil && policy.Environment.IsolatedTools && policy.ProjectDir != "" {
		prefixDir := filepath.Join(policy.ProjectDir, ".nvx", "npm_global")
		if mkErr := os.MkdirAll(prefixDir, 0700); mkErr == nil {
			return prefixDir
		}
		LogWarn("Failed to create project tools directory %s; falling back to version-level npm prefix.", prefixDir)
	}
	return filepath.Join(targetVersionDir, "npm_global")
}

// emitSessionEnv prints the shell statements that activate a Node version
// (and its npm prefix) for the current terminal session.
func emitSessionEnv(shell, nvxHome, targetDir string) {
	runtimeName := runtimeFromVersionDir(nvxHome, targetDir)
	npmPrefixDir := ""
	if runtimeName == "" || runtimeName == "node" {
		npmPrefixDir = resolveNpmPrefixDir(nvxHome, targetDir)
	}

	newPath := CleanAndBuildPath(os.Getenv("PATH"), nvxHome, targetDir, npmPrefixDir)
	fmt.Print(shellEnvAssignment(shell, "PATH", FormatPathForShell(shell, newPath)))

	if npmPrefixDir != "" {
		fmt.Print(shellEnvAssignment(shell, "NPM_CONFIG_PREFIX", FormatPathForShell(shell, npmPrefixDir)))
	}

	// Runtime-specific session variables (none for node/bun today; the hook lets
	// future runtimes like Go/Rust set GOROOT/RUSTUP_HOME without new plumbing).
	lookupName := runtimeName
	if lookupName == "" {
		lookupName = "node"
	}
	if provider, ok := Providers[lookupName]; ok {
		for key, value := range provider.SessionEnv(targetDir) {
			fmt.Print(shellEnvAssignment(shell, key, value))
		}
	}
}

// PromptTrustBoundary asks a question that WIDENS what nvx allows, and refuses to
// take -y, --agent-mode or NVX_YES for an answer.
//
// Two prompts decide the security model rather than a step inside it: trusting a
// project's own `.nvx-policy.json` when it loosens settings, and adding a host to
// the egress allowlist. Both persist. Both were covered by the blanket yes, and
// --agent-mode sets that yes -- so the mode built for AI agents, which clone
// repositories they have not read, auto-approved a repository's request to turn
// containment off. Measured: a `.nvx-policy.json` carrying
// `{"isolation":{"enabled":false}}` was refused without the flag and silently
// trusted with it, after which the sandbox was gone for every later command in
// that project. The same yes approved arbitrary egress hosts, including an IP on
// a C2-style port, and wrote them to the grants store.
//
// This is the reasoning already applied to `nvx doctor --fix`, where a prompt was
// rejected because "NVX_YES is set as a matter of course by agents and CI, so a
// prompt would auto-approve a persistent system change for exactly the callers
// least able to notice it". That argument was made about a PATH edit and not
// carried to the prompts that gate containment itself.
//
// NVX_TRUST_YES exists for the case where someone genuinely means it -- a CI job
// pinning its own policy. It is deliberately not NVX_YES: nothing sets it by
// habit, so setting it is a decision rather than an inheritance.
func PromptTrustBoundary(message string) bool {
	if os.Getenv("NVX_TRUST_YES") == "true" || os.Getenv("NVX_TRUST_YES") == "1" {
		LogWarn("NVX_TRUST_YES is set: approving a request that widens nvx's trust boundary. %s", message)
		return true
	}
	if !stdinIsInteractive() {
		LogWarn("Denying a request that widens nvx's trust boundary, because nobody is here to approve it: %s", message)
		LogInfo("-y, --agent-mode and NVX_YES deliberately do not approve this. Set NVX_TRUST_YES=true only if you have read what you are trusting.")
		return false
	}
	return promptConsoleYesNo(message)
}

// PromptYesNo prints a message to the console TTY and reads a Y/N keypress, bypassing standard redirections.
func PromptYesNo(message string) bool {
	if yesFlag {
		return true
	}
	if os.Getenv("NVX_YES") == "true" || os.Getenv("NVX_YES") == "1" {
		return true
	}
	if os.Getenv("NVX_NONINTERACTIVE") == "true" || os.Getenv("NVX_NONINTERACTIVE") == "1" {
		LogWarn("Non-interactive environment: denying prompt. Use -y / --yes or set NVX_YES=true to approve automatically. Prompt was: %s", message)
		return false
	}

	// A console existing is not the same as a human being present.
	//
	// This used to go straight to CONIN$/dev/tty, deliberately bypassing
	// redirections so a prompt still worked when stdout was piped. But that also
	// meant a caller with stdin redirected -- a CI step, an agent harness, a
	// script piping input -- got a prompt read from a console nobody was watching,
	// and simply stopped. README and SECURITY.md both promise the operation is
	// denied in that case; instead it neither approved nor denied, and an install
	// sat at "Verifying package" until it was killed.
	//
	// Gate on stdin specifically. A redirected stdin means nobody is there to
	// answer, whatever the console says. Redirected stdout is still fine: stdin
	// stays a character device, so an interactive user piping output is unaffected
	// -- which is the case the CONIN$ path was written for in the first place.
	if !stdinIsInteractive() {
		LogWarn("Non-interactive environment: denying prompt. Use -y / --yes or set NVX_YES=true to approve automatically. Prompt was: %s", message)
		return false
	}

	return promptConsoleYesNo(message)
}

// promptConsoleYesNo does the console interaction itself, shared by PromptYesNo
// and PromptTrustBoundary so the two cannot drift in how they read an answer.
func promptConsoleYesNo(message string) bool {
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

// runVerifyInstall verifies packages against policy blocklists, typosquatting, and the real-time OSV CVE database.
// runVerifyInstall runs the pre-install security checks and returns the exit
// code the caller should use: 0 to proceed, 1 to abort.
//
// It used to call os.Exit itself. That skipped runShim's run trace on every
// abort path, so the runs that never got recorded were exactly the ones worth
// recording -- policy blocks, typosquat aborts, CVE aborts, script refusals --
// and `nvx audit --failures` could not see a single one of them. Returning the
// code means the caller's bookkeeping runs.
// The second return value is a short, fixed description of why a run was
// refused, or "" when it was not. It is developer-authored text and never
// contains a package specifier: one can be a URL with credentials in it (see the
// note on LogWarn), and this reason is shown to an MCP client and may be logged
// there. See mcp_refusal.go.
func runVerifyInstall(args []string, nvxHome string) (int, string) {
	policy, err := LoadPolicy(nvxHome)
	if err != nil {
		LogError("Failed to load security policy: %v", err)
		return 1, "its security policy could not be loaded"
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
			return 1, "the security policy blocks one of its packages"
		}

		// 2. Typosquatting Check
		if policy.Typosquatting.Enabled && !policy.IsTrustedPackage(pkgName) {
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
					LogError("Installation aborted: the typosquatting warning was not approved.")
					return 1, "a package looked like a typosquat and the warning was not approved"
				}
			}
		}

		LogInfo("Verifying package %q...", pkgName)
		resolvedVer, pubTime, hasScripts, err := resolveNpmPackageDetailsForVerify(pkgName, versionQuery)
		if err != nil {
			msg := fmt.Sprintf("Could not verify registry metadata for %s: %v. Proceed without metadata checks?", pkgName, err)
			if !PromptYesNo(msg) {
				LogError("Installation aborted because registry metadata could not be verified.")
				return 1, "the registry metadata for a package could not be verified"
			}
			LogWarn("Proceeding without registry metadata checks for %s.", pkgName)
			continue
		}

		// 3. Installation Script Execution Check
		if hasScripts {
			LogWarn("Package %s@%s contains installation scripts (preinstall/postinstall/install).", pkgName, resolvedVer)
			LogWarn("Malicious packages often execute rogue code during the install phase.")
			if policy.EnforceIgnoreScripts {
				LogError("Blocked by security policy: Package scripts are disallowed. Please run with --ignore-scripts.")
				return 1, "the security policy disallows package install scripts"
			} else {
				// Not "on your host": these run contained, and saying otherwise
				// overstates the risk of approving while understating what the
				// sandbox is doing for you.
				msg := fmt.Sprintf("Package %s@%s contains install scripts. Run them (contained)?", pkgName, resolvedVer)
				if !PromptYesNo(msg) {
					LogError("Installation aborted: the install-script warning was not approved.")
					return 1, "a package runs install scripts and the warning was not approved"
				}
			}
		}

		// 4. Release Age Check (supply chain cooling-off window)
		if policy.ReleaseAgeEnabled() && !policy.IsTrustedPackage(pkgName) && publishAgeShouldWarn(pubTime, policy.ReleaseAgeMinHours(), time.Now()) {
			age := time.Since(pubTime)
			windowHours := policy.ReleaseAgeMinHours()
			msg := fmt.Sprintf("Package %s@%s was published only %.1f hours ago (on %s). Supply chain compromises are often caught within %d hours. Proceed?",
				pkgName, resolvedVer, age.Hours(), pubTime.Format("2006-01-02 15:04:05"), windowHours)
			if !PromptYesNo(msg) {
				LogError("Installation aborted: the release-age warning was not approved.")
				return 1, "a package version was published inside the release-age cooling-off window"
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
		vulns, err := scanVulnerabilitiesBatchForVerify(osvQueries)
		if err != nil {
			msg := fmt.Sprintf("Vulnerability database scan failed: %v. Proceed without CVE checks?", err)
			if !PromptYesNo(msg) {
				LogError("Installation aborted because vulnerability checks could not be completed.")
				return 1, "its vulnerability checks could not be completed"
			}
			LogWarn("Proceeding without vulnerability database results.")
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
				LogError("Installation aborted: the vulnerability warning was not approved.")
				return 1, "a package has a known active vulnerability and the warning was not approved"
			}
		} else {
			LogSuccess("Vulnerability scan clean. No active CVEs found.")
		}
	}
	return 0, ""
}
