package main

import (
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
// variables and only allowing known-safe keys through. It also redirects HOME /
// USERPROFILE to the guest profile directory.
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

		cleanEnv = append(cleanEnv, envVar)
	}

	// Redirect home-related variables to the guest profile
	if runtime.GOOS == "windows" {
		cleanEnv = append(cleanEnv,
			fmt.Sprintf("USERPROFILE=%s", guestHome),
			fmt.Sprintf("HOMEPATH=%s", guestHome),
			fmt.Sprintf("APPDATA=%s", filepath.Join(guestHome, "AppData", "Roaming")),
			fmt.Sprintf("LOCALAPPDATA=%s", filepath.Join(guestHome, "AppData", "Local")),
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
		)
		_ = os.MkdirAll(filepath.Join(guestHome, ".local", "share"), 0755)
	}

	// Set a sandbox indicator so child processes can detect they're sandboxed
	cleanEnv = append(cleanEnv, "NVX_SANDBOX=1")

	return cleanEnv
}

// runSandbox is the main entry point for executing a command inside the nvx sandbox.
// It creates an ephemeral guest profile, scrubs the environment, applies OS-level
// isolation primitives, runs the command, and cleans up afterward.
// resolvePinnedCommandPath resolves standard node commands (node/npm/npx) to their pinned version binaries
func resolvePinnedCommandPath(command string, nvxHome string, pinnedVer string) string {
	if pinnedVer == "" {
		return ""
	}
	provider := Providers["node"]
	resolvedVer, err := resolveLocalVersion(provider, pinnedVer, nvxHome)
	if err != nil {
		return ""
	}

	var binaryPath string
	if runtime.GOOS == "windows" {
		if command == "node" {
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "node.exe")
		} else if command == "npm" {
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "npm.cmd")
		} else if command == "npx" {
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "npx.cmd")
		}
	} else {
		if command == "node" || command == "npm" || command == "npx" {
			binaryPath = filepath.Join(nvxHome, "versions", "node", resolvedVer, "bin", command)
		}
	}

	if binaryPath != "" {
		if _, err := os.Stat(binaryPath); err == nil {
			return binaryPath
		}
	}
	return ""
}

// runDockerSandbox runs the execution request inside a Docker container
func runDockerSandbox(config SandboxConfig, nvxHome string, pinnedVer string) int {
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
	policy, err := LoadPolicy(config.NvxHome)
	if err != nil {
		LogWarn("Failed to load policy: %v. Defaulting to native provider.", err)
	}

	provider := "native"
	if policy.Isolation.Enabled && policy.Isolation.Provider != "" {
		provider = strings.ToLower(policy.Isolation.Provider)
	}

	if provider == "docker" {
		return runDockerSandbox(config, config.NvxHome, policy.Isolation.Runtime.Version)
	}

	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return 1
	}

	LogInfo("Sandbox session: %s", sandboxID)

	// 1. Create ephemeral guest profile
	guestHome, err := createGuestProfile(config.NvxHome, sandboxID)
	if err != nil {
		LogError("Failed to create sandbox guest profile: %v", err)
		return 1
	}
	defer cleanupGuestProfile(config.NvxHome, sandboxID)

	// 2. Scrub environment
	cleanEnv := scrubEnvironment(guestHome)

	// 3. Resolve the command — if it's a bare name, look it up on PATH
	cmdPath := ""
	if policy.Isolation.Runtime.Version != "" {
		if policy.Isolation.Runtime.Command == "" || strings.ToLower(config.Command) == strings.ToLower(policy.Isolation.Runtime.Command) {
			cmdPath = resolvePinnedCommandPath(config.Command, config.NvxHome, policy.Isolation.Runtime.Version)
		}
	}
	if cmdPath == "" {
		var err error
		cmdPath, err = exec.LookPath(config.Command)
		if err != nil {
			LogError("Command not found: %s", config.Command)
			return 127
		}
	}



	// 4. Build the exec.Cmd
	cmd := exec.Command(cmdPath, config.Args...)
	cmd.Env = cleanEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if config.WorkDir != "" {
		cmd.Dir = config.WorkDir
	}

	// 5. Apply platform-specific isolation
	applySandboxIsolation(cmd, guestHome)
	defer closeTokenHandle(cmd)

	// 6. Execute

	LogInfo("Running in sandbox: %s %s", config.Command, strings.Join(config.Args, " "))

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("Sandbox execution failed: %v", err)
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
