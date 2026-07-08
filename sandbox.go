package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// SandboxConfig holds the parameters for an isolated execution environment.
type SandboxConfig struct {
	// NvxHome is the root nvx directory (~/.nvx)
	NvxHome string
	// Command is the executable to run (e.g. "node", "npx", full path)
	Command string
	// Args are the arguments to pass to the command
	Args []string
	// WorkDir is the working directory for the sandboxed process (defaults to cwd)
	WorkDir string
	// FilesystemProvider overrides isolation.filesystem.provider from policy.
	FilesystemProvider string
	// ToolName is set when this invocation is a granted trusted tool (see
	// ensureTrustedToolGrant) — the native sandbox uses the real home
	// directory instead of an ephemeral guest profile for the run. Empty
	// means "use the ephemeral guest home" (the default, contained behavior).
	ToolName string
}

// sensitiveEnvPrefixes are environment variable prefixes that will be scrubbed
// to prevent credential harvesting from sandboxed processes.
var sensitiveEnvPrefixes = []string{
	"AWS_",
	"AZURE_",
	"GCP_",
	"GOOGLE_",
	"GITHUB_",
	"GITLAB_",
	"NPM_TOKEN",
	"NPM_AUTH",
	"NODE_AUTH",
	"SSH_",
	"SECRET_",
	"TOKEN_",
	"API_KEY",
	"PRIVATE_KEY",
	"CREDENTIAL",
	"PASSWORD",
	"DOCKER_",
	"KUBECONFIG",
	"OPENAI_",
	"ANTHROPIC_",
	"HF_TOKEN",
}

// windowsAllowedEnvKeys are the only environment variables allowed through on Windows
// when running in sandbox mode.
var windowsAllowedEnvKeys = map[string]bool{
	"PATH":                   true,
	"PATHEXT":                true,
	"SYSTEMROOT":             true,
	"SYSTEMDRIVE":            true,
	"COMSPEC":                true,
	"TEMP":                   true,
	"TMP":                    true,
	"WINDIR":                 true,
	"PROCESSOR_ARCHITECTURE": true,
	"NUMBER_OF_PROCESSORS":   true,
	"OS":                     true,
}

// unixAllowedEnvKeys are the only environment variables allowed through on Unix
// when running in sandbox mode.
var unixAllowedEnvKeys = map[string]bool{
	"PATH":   true,
	"TMPDIR": true,
	"SHELL":  true,
	"TERM":   true,
	"LANG":   true,
	"LC_ALL": true,
	"USER":   true,
}

// generateSandboxID creates a short random identifier for an ephemeral sandbox session.
func generateSandboxID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate sandbox ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// getSandboxHomeDir returns the root directory for sandbox ephemeral homes.
func getSandboxHomeDir(nvxHome string) string {
	return filepath.Join(nvxHome, "sandbox_home")
}

// realHomeSwapSupported reports whether this platform can safely swap a
// trusted tool's sandbox HOME for the user's real home directory. Windows is
// excluded: granting an AppContainer write access to a real home directory
// would require the same recursive icacls write on the profile root that is
// already known to hang behind the OneDrive/Defender filter driver (see
// windows_setup_windows.go's profile-root exclusion). Linux (Landlock) and
// macOS (Seatbelt) grant filesystem access via an in-process rule/profile
// list, not a filesystem ACL mutation, so neither has this risk.
func realHomeSwapSupported() bool {
	return runtime.GOOS != "windows"
}

// createGuestProfile creates an ephemeral guest home directory for the sandbox session.
// Returns the path to the guest home and any error encountered.
func createGuestProfile(nvxHome string, sandboxID string) (string, error) {
	guestHome := filepath.Join(getSandboxHomeDir(nvxHome), sandboxID)
	if err := os.MkdirAll(guestHome, 0700); err != nil {
		return "", fmt.Errorf("failed to create guest profile directory: %w", err)
	}

	// Create minimal directory structure inside the guest home
	subdirs := []string{"tmp", ".config", ".cache"}
	if runtime.GOOS == "windows" {
		subdirs = append(subdirs, filepath.Join("AppData", "Roaming"), filepath.Join("AppData", "Local"))
	} else {
		subdirs = append(subdirs, filepath.Join(".local", "share"))
	}
	for _, subdir := range subdirs {
		if err := os.MkdirAll(filepath.Join(guestHome, subdir), 0700); err != nil {
			return "", fmt.Errorf("failed to create guest profile subdirectory %s: %w", subdir, err)
		}
	}

	return guestHome, nil
}

// cleanupGuestProfile removes the ephemeral guest home directory after the sandbox exits.
func cleanupGuestProfile(nvxHome string, sandboxID string) {
	guestHome := filepath.Join(getSandboxHomeDir(nvxHome), sandboxID)
	if err := os.RemoveAll(guestHome); err != nil {
		LogWarn("Failed to clean up sandbox guest profile at %s: %v", guestHome, err)
	}
}

// scrubEnvironment filters the current process environment, removing sensitive
// variables and only allowing known-safe keys through. When guestHome is
// non-empty, home- and temp-related variables are redirected into the guest
// profile; providers with their own filesystem view (e.g. Docker) pass "".
func scrubEnvironment(guestHome string) []string {
	var allowed map[string]bool
	if runtime.GOOS == "windows" {
		allowed = windowsAllowedEnvKeys
	} else {
		allowed = unixAllowedEnvKeys
	}

	var cleanEnv []string
	for _, envVar := range os.Environ() {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		keyUpper := strings.ToUpper(key)

		// Skip sensitive prefixes
		isSensitive := false
		for _, prefix := range sensitiveEnvPrefixes {
			if strings.HasPrefix(keyUpper, prefix) {
				isSensitive = true
				break
			}
		}
		if isSensitive {
			continue
		}

		// Only allow known-safe keys through
		if !allowed[keyUpper] {
			continue
		}

		// Temp dirs are redirected into the guest profile below so a
		// low-privilege sandboxed process can still write scratch files.
		if guestHome != "" && (keyUpper == "TEMP" || keyUpper == "TMP" || keyUpper == "TMPDIR") {
			continue
		}

		cleanEnv = append(cleanEnv, envVar)
	}

	// Redirect home- and temp-related variables to the guest profile
	if guestHome != "" {
		guestTmp := filepath.Join(guestHome, "tmp")
		if runtime.GOOS == "windows" {
			cleanEnv = append(cleanEnv,
				fmt.Sprintf("USERPROFILE=%s", guestHome),
				fmt.Sprintf("HOMEDRIVE=%s", filepath.VolumeName(guestHome)),
				fmt.Sprintf("HOMEPATH=%s", strings.TrimPrefix(guestHome, filepath.VolumeName(guestHome))),
				fmt.Sprintf("APPDATA=%s", filepath.Join(guestHome, "AppData", "Roaming")),
				fmt.Sprintf("LOCALAPPDATA=%s", filepath.Join(guestHome, "AppData", "Local")),
				fmt.Sprintf("TEMP=%s", guestTmp),
				fmt.Sprintf("TMP=%s", guestTmp),
			)
		} else {
			cleanEnv = append(cleanEnv,
				fmt.Sprintf("HOME=%s", guestHome),
				fmt.Sprintf("XDG_CONFIG_HOME=%s", filepath.Join(guestHome, ".config")),
				fmt.Sprintf("XDG_CACHE_HOME=%s", filepath.Join(guestHome, ".cache")),
				fmt.Sprintf("XDG_DATA_HOME=%s", filepath.Join(guestHome, ".local", "share")),
				fmt.Sprintf("TMPDIR=%s", guestTmp),
			)
		}
	}

	// Sandbox depth indicator for nested invocations (internal).
	cleanEnv = append(cleanEnv, "NVX_SANDBOX=1")

	return cleanEnv
}

// NetworkLaunchContext carries egress proxy endpoints for OS network rules.
type NetworkLaunchContext struct {
	Mode           string
	HTTPProxyHost  string
	HTTPProxyPort  uint16
	SOCKSProxyHost string
	SOCKSProxyPort uint16
}

// runSandbox is the main entry point for executing a command inside the nvx sandbox.
// It creates an ephemeral guest profile, scrubs the environment, applies OS-level
// isolation primitives, runs the command, and cleans up afterward.
// resolvePinnedCommandPath is defined in runtime_exec.go.

// runDockerSandbox runs the execution request inside a Docker container. The
// image comes from the runtime provider (node -> node:<ver>, bun -> oven/bun),
// and offline/loopback network modes are enforced with `--network none`.
func runDockerSandbox(config SandboxConfig, nvxHome string, pinnedVer string, egress *EgressProxy, rt RuntimeProvider, netCtx NetworkLaunchContext) int {
	ver := pinnedVer
	if ver == "" {
		ver = getActiveShellVersionFor(nvxHome, rt.Name())
	}
	if ver == "" {
		ver = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}

	imageName := rt.SandboxImage(ver)
	if imageName == "" {
		LogError("The %s runtime does not provide a Docker image; use the native provider.", rt.Name())
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}

	dockerArgs := dockerRunArgs(imageName, cwd, config, egress, netCtx)
	cmd := exec.Command("docker", dockerArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	LogInfo("Running in Docker sandbox: docker %s", strings.Join(dockerArgs, " "))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Docker execution failed: %v. Make sure Docker is running.", err)
		return 1
	}

	return 0
}

// dockerRunArgs builds the `docker run` argument list. It is a pure function so
// the hardening flags and network handling can be unit-tested without Docker.
func dockerRunArgs(imageName, cwd string, config SandboxConfig, egress *EgressProxy, netCtx NetworkLaunchContext) []string {
	args := []string{
		"run", "--rm", "-i",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit=512",
		"--tmpfs", "/tmp",
	}

	switch strings.ToLower(strings.TrimSpace(netCtx.Mode)) {
	case "offline", "loopback":
		// No network interfaces at all: genuine enforcement, not cooperative.
		args = append(args, "--network", "none")
	}

	args = append(args, "-v", fmt.Sprintf("%s:/app", cwd), "-w", "/app")

	cleanEnv := applyProxyEnv(scrubEnvironment(""), egress)
	for _, envVar := range cleanEnv {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 && parts[0] != "PATH" && parts[0] != "NVX_SANDBOX" {
			args = append(args, "-e", envVar)
		}
	}

	args = append(args, imageName, config.Command)
	return append(args, config.Args...)
}

// runSandbox is the main entry point for executing a command inside the nvx sandbox.
// It creates an ephemeral guest profile, scrubs the environment, applies OS-level
// isolation primitives, runs the command, and cleans up afterward.
func runSandbox(config SandboxConfig) int {
	if inSandboxSession() {
		return execBareCommand(config)
	}

	enterSandboxSession()
	defer leaveSandboxSession()

	policy, err := LoadPolicy(config.NvxHome)
	if err != nil {
		LogError("Failed to load policy: %v", err)
		return 1
	}

	providerName := policy.FilesystemProvider()
	if config.FilesystemProvider != "" {
		providerName = strings.ToLower(config.FilesystemProvider)
	}
	fsProvider, ok := lookupFilesystemProvider(providerName)
	if !ok {
		LogError("Unknown filesystem provider %q. Supported: native, docker.", providerName)
		return 1
	}
	canonical := fsProvider.Name()

	if fsProvider.Experimental() && !experimentalProvidersEnabled() {
		LogError("Filesystem provider %q is experimental and unsupported. Set NVX_EXPERIMENTAL=1 to enable it, or use the native or docker provider.", canonical)
		return 1
	}
	if err := fsProvider.Available(); err != nil {
		LogError("Filesystem provider %q is not available: %v", canonical, err)
		return 1
	}
	if !fsProvider.SupportsNetworkMode(policy.Isolation.Network.Mode) {
		LogError("Filesystem provider %q does not enforce network.mode=%q. Use network.mode=open or the native provider.", canonical, policy.Isolation.Network.Mode)
		return 1
	}

	rt := runtimeForShim(config.Command)
	pinnedVer := policy.PinnedRuntimeVersion(rt.Name())

	ctx := context.Background()
	var egress *EgressProxy
	// Linux native re-execs into a loopback-only network namespace and starts its
	// own egress proxy inside the child; a parent proxy would be unreachable.
	skipParentProxy := runtime.GOOS == "linux" && canonical == "native" &&
		networkModeRequiresNamespace(policy.Isolation.Network.Mode)
	if !skipParentProxy {
		var err error
		egress, err = startEgressProxy(ctx, policy, rt, config.NvxHome)
		if err != nil {
			LogError("Egress proxy failed: %v", err)
			return 1
		}
		if egress != nil {
			defer egress.Close()
		}
	}

	netCtx := NetworkLaunchContext{Mode: policy.Isolation.Network.Mode}
	if egress != nil {
		netCtx.HTTPProxyHost, netCtx.HTTPProxyPort = egress.HTTPListenHostPort()
		netCtx.SOCKSProxyHost, netCtx.SOCKSProxyPort = egress.SOCKSListenHostPort()
	}

	return fsProvider.Run(SandboxRequest{
		Config:  config,
		Policy:  policy,
		Runtime: rt,
		Pinned:  pinnedVer,
		Egress:  egress,
		NetCtx:  netCtx,
	})
}

func providerSupportsNetworkMode(provider, mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "open" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "native", "sandbox-exec", "seatbelt":
		return true
	case "docker":
		// Docker enforces offline/loopback with `--network none`. Proxy mode is
		// cooperative-only under Docker (the allowlist is not truly enforced),
		// so it stays disallowed and callers must use the native provider.
		return mode == "offline" || mode == "loopback"
	default:
		return false
	}
}

func execBareCommand(config SandboxConfig) int {
	rt := runtimeForShim(config.Command)
	activeVer := getActiveShellVersionFor(config.NvxHome, rt.Name())
	if activeVer == "" {
		activeVer = getGlobalDefaultVersionFor(config.NvxHome, rt.Name())
	}
	binaryPath := resolvePinnedCommandPath(config.Command, config.NvxHome, activeVer, rt)
	if binaryPath == "" {
		var err error
		binaryPath, err = exec.LookPath(config.Command)
		if err != nil {
			LogError("Command not found: %s", config.Command)
			return 127
		}
	}
	cmd := exec.Command(binaryPath, config.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if config.WorkDir != "" {
		cmd.Dir = config.WorkDir
	}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// cleanupStaleSandboxes removes any leftover sandbox home directories from
// previous sessions that failed to clean up (e.g., due to crashes).
func cleanupStaleSandboxes(nvxHome string) {
	sandboxDir := getSandboxHomeDir(nvxHome)
	entries, err := os.ReadDir(sandboxDir)
	if err != nil {
		return // Directory doesn't exist or can't be read
	}

	for _, entry := range entries {
		if entry.IsDir() {
			fullPath := filepath.Join(sandboxDir, entry.Name())
			if err := os.RemoveAll(fullPath); err != nil {
				LogWarn("Failed to clean stale sandbox: %s", entry.Name())
			}
		}
	}
}
