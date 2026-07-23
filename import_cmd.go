package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// runImport scans existing nvm, fnm, and volta installation paths for Node.js versions
// and installs any missing versions into nvx's version store.
func runImport(source string, nvxHome string) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = "all"
	}

	discovered := make(map[string]string) // version -> source

	if source == "all" || source == "nvm" {
		importNvm(discovered)
	}
	if source == "all" || source == "fnm" {
		importFnm(discovered)
	}
	if source == "all" || source == "volta" {
		importVolta(discovered)
	}

	if len(discovered) == 0 {
		LogInfo("No previous Node.js installations found for source '%s'.", source)
		return
	}

	provider := Providers["node"]
	installedCount := 0
	alreadyInstalled := 0

	for ver, src := range discovered {
		cleanVer := strings.TrimPrefix(strings.ToLower(ver), "v")
		cleanVer = strings.TrimPrefix(cleanVer, "node-")
		if cleanVer == "" {
			continue
		}

		targetDir := filepath.Join(nvxHome, "versions", provider.Name(), "v"+cleanVer)
		if _, err := os.Stat(targetDir); err == nil {
			alreadyInstalled++
			LogInfo("Node.js v%s (from %s) is already installed in nvx.", cleanVer, src)
			continue
		}

		LogInfo("Importing Node.js v%s from %s...", cleanVer, src)
		err := provider.Install(cleanVer, nvxHome)
		if err != nil {
			LogError("Failed to import Node.js v%s: %v", cleanVer, err)
		} else {
			LogSuccess("Successfully imported Node.js v%s from %s.", cleanVer, src)
			installedCount++
		}
	}

	LogSuccess("Import complete: %d imported, %d already present.", installedCount, alreadyInstalled)
}

func importNvm(discovered map[string]string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// 1. Standard Unix nvm: ~/.nvm/versions/node/
	nvmUnixDir := filepath.Join(home, ".nvm", "versions", "node")
	scanVersionDirs(nvmUnixDir, "nvm", discovered)

	// 2. Windows nvm-windows: %NVM_HOME% or %APPDATA%\nvm
	if runtime.GOOS == "windows" {
		nvmWin := os.Getenv("NVM_HOME")
		if nvmWin == "" {
			if appData := os.Getenv("APPDATA"); appData != "" {
				nvmWin = filepath.Join(appData, "nvm")
			}
		}
		if nvmWin != "" {
			scanVersionDirs(nvmWin, "nvm-windows", discovered)
		}
	}
}

func importFnm(discovered map[string]string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	paths := []string{
		filepath.Join(home, ".fnm", "current"),
		filepath.Join(home, ".local", "share", "fnm", "current"),
		filepath.Join(home, ".fnm"),
	}

	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			paths = append(paths, filepath.Join(localAppData, "fnm_multishells"))
			paths = append(paths, filepath.Join(localAppData, "fnm"))
		}
	}

	for _, p := range paths {
		scanVersionDirs(p, "fnm", discovered)
	}
}

func importVolta(discovered map[string]string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	voltaDir := filepath.Join(home, ".volta", "tools", "image", "node")
	scanVersionDirs(voltaDir, "volta", discovered)
}

func scanVersionDirs(dir string, sourceName string, discovered map[string]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Filter valid version directory names (e.g. v20.11.0, 20.11.0, node-v18.16.0)
		clean := strings.ToLower(name)
		clean = strings.TrimPrefix(clean, "node-")
		clean = strings.TrimPrefix(clean, "v")

		if len(clean) > 0 && (clean[0] >= '0' && clean[0] <= '9') {
			if _, exists := discovered[clean]; !exists {
				discovered[clean] = sourceName
			}
		}
	}
}
