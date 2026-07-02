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
	"PATH":              true,
	"PATHEXT":           true,
	"SYSTEMROOT":        true,
	"SYSTEMDRIVE":       true,
	"COMSPEC":           true,
	"TEMP":              true,
	"TMP":               true,
	"WINDIR":            true,
	"HTTP_PROXY":        true,
	"HTTPS_PROXY":       true,
	"ALL_PROXY":         true,
	"NO_PROXY":          true,
	"PROCESSOR_ARCHITECTURE": true,
	"NUMBER_OF_PROCESSORS":   true,
	"OS":                true,
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
	"HTTP_PROXY":  true,
	"HTTPS_PROXY": true,
	"ALL_PROXY":   true,
	"NO_PROXY":    true,
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

// createGuestProfile creates an ephemeral guest home directory for the sandbox session.
// Returns the path to the guest home and any error encountered.
func createGuestProfile(nvxHome string, sandboxID string) (string, error) {
	guestHome := filepath.Join(getSandboxHomeDir(nvxHome), sandboxID)
	if err := os.MkdirAll(guestHome, 0755); err != nil {
		return "", fmt.Errorf("failed to create guest profile directory: %w", err)
	}

	// Create minimal directory structure inside the guest home
	for _, subdir := range []string{"tmp", ".config", ".cache"} {
		_ = os.MkdirAll(filepath.Join(guestHome, subdir), 0755)
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
			// Create the AppData directories
			_ = os.MkdirAll(filepath.Join(guestHome, "AppData", "Roaming"), 0755)
			_ = os.MkdirAll(filepath.Join(guestHome, "AppData", "Local"), 0755)
		} else {
			cleanEnv = append(cleanEnv,
				fmt.Sprintf("HOME=%s", guestHome),
				fmt.Sprintf("XDG_CONFIG_HOME=%s", filepath.Join(guestHome, ".config")),
				fmt.Sprintf("XDG_CACHE_HOME=%s", filepath.Join(guestHome, ".cache")),
				fmt.Sprintf("XDG_DATA_HOME=%s", filepath.Join(guestHome, ".local", "share")),
				fmt.Sprintf("TMPDIR=%s", guestTmp),
			)
			_ = os.MkdirAll(filepath.Join(guestHome, ".local", "share"), 0755)
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

// runDockerSandbox runs the execution request inside a Docker container
func runDockerSandbox(config SandboxConfig, nvxHome string, pinnedVer string, egress *EgressProxy) int {
	nodeVer := pinnedVer
	if nodeVer == "" {
		nodeVer = getActiveShellVersion(nvxHome)
	}
	if nodeVer == "" {
		nodeVer = getGlobalDefaultVersion(nvxHome)
	}

	imageTag := "latest"
	if nodeVer != "" {
		imageTag = strings.TrimPrefix(nodeVer, "v")
	}
	imageName := "node:" + imageTag

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}

	dockerArgs := []string{
		"run",
		"--rm",
		"-i",
	}

	// Bind mount the current directory to container /app directory
	dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:/app", cwd))
	dockerArgs = append(dockerArgs, "-w", "/app")

	// Scrub and carry over safe environment variables
	cleanEnv := scrubEnvironment("")
	cleanEnv = applyProxyEnv(cleanEnv, egress)
	for _, envVar := range cleanEnv {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 && parts[0] != "PATH" && parts[0] != "NVX_SANDBOX" {
			dockerArgs = append(dockerArgs, "-e", envVar)
		}
	}

	dockerArgs = append(dockerArgs, imageName)
	dockerArgs = append(dockerArgs, config.Command)
	dockerArgs = append(dockerArgs, config.Args...)

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
		LogWarn("Failed to load policy: %v", err)
	}

	provider := policy.FilesystemProvider()
	if config.FilesystemProvider != "" {
		provider = strings.ToLower(config.FilesystemProvider)
	}

	rt := runtimeForShim(config.Command)
	pinnedVer := policy.PinnedRuntimeVersion(rt.Name())

	ctx := context.Background()
	var egress *EgressProxy
	// Linux native re-execs into a loopback-only network namespace and starts its
	// own egress proxy inside the child; a parent proxy would be unreachable.
	skipParentProxy := runtime.GOOS == "linux" && provider == "native" &&
		networkModeRequiresNamespace(policy.Isolation.Network.Mode)
	if !skipParentProxy {
		var err error
		egress, err = startEgressProxy(ctx, policy, rt)
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

	switch provider {
	case "native":
		return runNativeSandbox(config, policy, egress, netCtx)
	case "docker":
		return runDockerSandbox(config, config.NvxHome, pinnedVer, egress)
	case "wslc", "wsl-container", "container":
		return runWslcSandbox(config, config.NvxHome, pinnedVer)
	case "wsl", "wsl-distro":
		return runWslSandbox(config)
	case "sandbox-exec", "seatbelt":
		return runSeatbeltSandbox(config, netCtx)
	case "systemd-nspawn", "nspawn":
		return runNspawnSandbox(config)
	default:
		LogError("Unknown filesystem provider %q. Supported: native, docker, wslc, wsl, sandbox-exec, systemd-nspawn.", provider)
		return 1
	}
}

func execBareCommand(config SandboxConfig) int {
	rt := runtimeForShim(config.Command)
	nodeVer := getActiveShellVersion(config.NvxHome)
	if nodeVer == "" {
		nodeVer = getGlobalDefaultVersion(config.NvxHome)
	}
	binaryPath := resolvePinnedCommandPath(config.Command, config.NvxHome, nodeVer, rt)
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
