package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	return filepath.Join(GetHomeDir(), "current")
}

// GetVersionBinDir returns the directory containing the node executable for a given version folder
func GetVersionBinDir(versionDir string) string {
	if runtime.GOOS == "windows" {
		return versionDir
	}
	return filepath.Join(versionDir, "bin")
}

// GetNpmGlobalBinDir returns the directory containing globally installed npm packages
func GetNpmGlobalBinDir(versionDir string) string {
	globalDir := filepath.Join(versionDir, "npm_global")
	if runtime.GOOS == "windows" {
		return globalDir
	}
	return filepath.Join(globalDir, "bin")
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

	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
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

// CleanAndBuildPath cleans nvx-related directories from the PATH and prepends the new one
func CleanAndBuildPath(currentPath, nvxHome, targetVersionDir string) string {
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

	// Prepend the new target version directory and its npm global bin directory
	if targetVersionDir != "" {
		binDir := GetVersionBinDir(targetVersionDir)
		npmBinDir := GetNpmGlobalBinDir(targetVersionDir)
		cleaned = append([]string{npmBinDir, binDir}, cleaned...)
	}

	// Always ensure shim directory is at the absolute front
	cleaned = append([]string{shimDir}, cleaned...)

	return strings.Join(cleaned, string(filepath.ListSeparator))
}

func generateShims(nvxHome string) {
	shimDir := filepath.Join(nvxHome, "bin")
	os.MkdirAll(shimDir, 0755)

	exePath, err := os.Executable()
	if err != nil {
		exePath = "nvx"
	}
	// On Windows, the path needs to be formatted for cmd
	exeCmd := fmt.Sprintf("\"%s\"", exePath)
	if runtime.GOOS != "windows" {
		exeCmd = fmt.Sprintf("'%s'", exePath)
	}

	commands := []string{"npm", "npx", "yarn", "pnpm", "bunx", "nvxs"}

	for _, cmd := range commands {
		if runtime.GOOS == "windows" {
			// .cmd shim
			content := fmt.Sprintf("@echo off\r\n%s shim %s %%*\r\n", exeCmd, cmd)
			os.WriteFile(filepath.Join(shimDir, cmd+".cmd"), []byte(content), 0755)
			
			// .ps1 shim (optional, but good for powershell native execution)
			contentPs1 := fmt.Sprintf("& %s shim %s @args\r\n", exeCmd, cmd)
			os.WriteFile(filepath.Join(shimDir, cmd+".ps1"), []byte(contentPs1), 0755)
		} else {
			// shell script shim
			content := fmt.Sprintf("#!/bin/sh\nexec %s shim %s \"$@\"\n", exeCmd, cmd)
			shimPath := filepath.Join(shimDir, cmd)
			os.WriteFile(shimPath, []byte(content), 0755)
		}
	}
}

func runShim(cmdName string, args []string, nvxHome string) int {
	// Security audit for install commands
	if cmdName == "npm" || cmdName == "yarn" || cmdName == "pnpm" {
		isInstall := false
		var pkgs []string
		if len(args) > 0 {
			cmdArg := args[0]
			if cmdArg == "install" || cmdArg == "i" || cmdArg == "add" {
				isInstall = true
				for _, arg := range args[1:] {
					if !strings.HasPrefix(arg, "-") {
						pkgs = append(pkgs, arg)
					}
				}
			}
		}

		if isInstall && len(pkgs) > 0 {
			runVerifyInstall(pkgs, nvxHome)
		}
	}

	// Route executor commands through sandbox if NVX_SANDBOX is not already set
	if (cmdName == "npx" || cmdName == "bunx") && os.Getenv("NVX_SANDBOX") == "" {
		return runSandbox(SandboxConfig{
			NvxHome: nvxHome,
			Command: cmdName,
			Args:    args,
		})
	}
	
	if cmdName == "nvxs" {
		if len(args) == 0 {
			return 0
		}
		return runSandbox(SandboxConfig{
			NvxHome: nvxHome,
			Command: args[0],
			Args:    args[1:],
		})
	}

	// We need to execute the real command. 
	nodeVer := getActiveShellVersion(nvxHome)
	if nodeVer == "" {
		nodeVer = getGlobalDefaultVersion(nvxHome)
	}
	
	binaryPath := resolvePinnedCommandPath(cmdName, nvxHome, nodeVer)
	if binaryPath == "" {
		// Fallback to searching the system PATH (excluding our shim dir)
		shimDir := filepath.Join(nvxHome, "bin")
		pathEnv := os.Getenv("PATH")
		var newPathParts []string
		for _, part := range filepath.SplitList(pathEnv) {
			if strings.EqualFold(filepath.Clean(part), filepath.Clean(shimDir)) {
				continue
			}
			newPathParts = append(newPathParts, part)
		}
		newPath := strings.Join(newPathParts, string(filepath.ListSeparator))
		os.Setenv("PATH", newPath)
		
		var err error
		binaryPath, err = exec.LookPath(cmdName)
		if err != nil {
			LogError("Could not find real executable for %s", cmdName)
			return 1
		}
	}

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
