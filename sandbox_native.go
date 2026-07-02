package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runNativeSandbox is the hardened default sandbox: platform-specific OS
// primitives (AppContainer + Low IL on Windows, Landlock on Linux, Seatbelt
// on macOS) layered on env scrubbing and an ephemeral guest profile.
func runNativeSandbox(config SandboxConfig, policy Policy, egress *EgressProxy, netCtx NetworkLaunchContext) int {
	sandboxID, err := generateSandboxID()
	if err != nil {
		LogError("Sandbox initialization failed: %v", err)
		return 1
	}

	LogInfo("Sandbox session: %s", sandboxID)

	guestHome, err := createGuestProfile(config.NvxHome, sandboxID)
	if err != nil {
		LogError("Failed to create sandbox guest profile: %v", err)
		return 1
	}
	defer cleanupGuestProfile(config.NvxHome, sandboxID)

	cleanEnv := scrubEnvironment(guestHome)
	cleanEnv = applyProxyEnv(cleanEnv, egress)

	cmdPath := resolveSandboxCommand(config, policy)
	if cmdPath == "" {
		return 127
	}

	workDir := config.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	workDir, _ = filepath.Abs(workDir)

	LogInfo("Running in native sandbox: %s %s", config.Command, strings.Join(config.Args, " "))
	return platformLaunchNative(config, guestHome, workDir, cmdPath, cleanEnv, netCtx)
}

func resolveSandboxCommand(config SandboxConfig, policy Policy) string {
	rt := runtimeForShim(config.Command)
	pinned := policy.PinnedRuntimeVersion(rt.Name())
	if pinned != "" {
		if policy.Runtime.Command == "" || strings.EqualFold(config.Command, policy.Runtime.Command) {
			if p := resolvePinnedCommandPath(config.Command, config.NvxHome, pinned, rt); p != "" {
				return p
			}
		}
	}
	cmdPath, err := exec.LookPath(config.Command)
	if err != nil {
		LogError("Command not found: %s", config.Command)
		return ""
	}
	return cmdPath
}

// parseLandlockExecArgs parses internal __landlock-exec arguments.
func parseLandlockExecArgs(argv []string) (guestHome, workDir, nvxHome, networkMode string, proxyPort int, cmdPath string, cmdArgs []string, ok bool) {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case strings.HasPrefix(arg, "--guest-home="):
			guestHome = strings.TrimPrefix(arg, "--guest-home=")
		case strings.HasPrefix(arg, "--work-dir="):
			workDir = strings.TrimPrefix(arg, "--work-dir=")
		case strings.HasPrefix(arg, "--nvx-home="):
			nvxHome = strings.TrimPrefix(arg, "--nvx-home=")
		case strings.HasPrefix(arg, "--network-mode="):
			networkMode = strings.TrimPrefix(arg, "--network-mode=")
		case strings.HasPrefix(arg, "--proxy-port="):
			fmt.Sscanf(strings.TrimPrefix(arg, "--proxy-port="), "%d", &proxyPort)
		case arg == "--":
			if i+1 < len(argv) {
				cmdPath = argv[i+1]
				cmdArgs = argv[i+2:]
				return guestHome, workDir, nvxHome, networkMode, proxyPort, cmdPath, cmdArgs, guestHome != "" && workDir != "" && cmdPath != ""
			}
			return "", "", "", "", 0, "", nil, false
		}
	}
	return "", "", "", "", 0, "", nil, false
}
