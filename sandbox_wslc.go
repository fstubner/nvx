package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// findWslcExecutable locates the WSL Containers CLI (wslc.exe). Microsoft
// also ships container.exe as an alias to the same binary.
func findWslcExecutable() (string, error) {
	for _, name := range []string{"wslc.exe", "container.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidates := []string{
		filepath.Join(systemRoot, "System32", "wslc.exe"),
		filepath.Join(systemRoot, "Sysnative", "wslc.exe"),
	}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		candidates = append(candidates, filepath.Join(pf, "WSL", "wslc.exe"))
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("wslc.exe not found")
}

// resolveWslcNodeImage picks a node:<tag> image for WSL Containers, mirroring
// the Docker provider's version resolution.
func resolveWslcNodeImage(nvxHome, pinnedVer string) string {
	nodeVer := pinnedVer
	if nodeVer == "" {
		nodeVer = getActiveShellVersion(nvxHome)
	}
	if nodeVer == "" {
		nodeVer = getGlobalDefaultVersion(nvxHome)
	}
	if nodeVer == "" {
		return "node:latest"
	}
	return "node:" + strings.TrimPrefix(nodeVer, "v")
}

// containerSafeEnv returns a minimal environment allowlist for container
// providers. Unlike the native sandbox we do not forward the host PATH or
// home-related variables into the guest.
func containerSafeEnv() []string {
	return []string{
		"TERM=xterm-256color",
		"NVX_SANDBOX=1",
	}
}

// runWslcSandbox executes the command inside a WSL Container (wslc.exe): a
// dedicated Hyper-V utility VM with OCI-style isolation, separate from both
// the Windows host and any installed WSL distros. Requires WSL 2.9.3+
// pre-release (wsl --update --pre-release).
func runWslcSandbox(config SandboxConfig, nvxHome string, pinnedVer string) int {
	if runtime.GOOS != "windows" {
		LogError("The 'wslc' isolation provider is only available on Windows.")
		return 1
	}

	wslcPath, err := findWslcExecutable()
	if err != nil {
		LogError("WSL Containers (wslc.exe) is not installed.")
		LogInfo("Install the WSL Containers preview with: wsl --update --pre-release")
		LogInfo("For weaker isolation without containers, use --provider=wsl to run inside your default WSL distro instead.")
		return 1
	}

	cwd := config.WorkDir
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			cwd = `C:\`
		}
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		LogError("Failed to resolve working directory: %v", err)
		return 1
	}

	image := resolveWslcNodeImage(nvxHome, pinnedVer)
	mountTarget := "/workspace"

	args := []string{
		"run",
		"--rm",
		"-i",
		"-v", fmt.Sprintf("%s:%s", cwd, mountTarget),
		"-w", mountTarget,
	}
	for _, envVar := range containerSafeEnv() {
		args = append(args, "-e", envVar)
	}

	args = append(args, image)
	args = append(args, config.Command)
	args = append(args, config.Args...)

	cmd := exec.Command(wslcPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	LogInfo("Running in WSL Container (%s): %s %s", image, config.Command, strings.Join(config.Args, " "))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		LogError("WSL Container execution failed: %v. Ensure WSL Containers is installed (wsl --update --pre-release).", err)
		return 1
	}
	return 0
}
