package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const projectBinShimDirName = "project-bin"

// findProjectRoot walks upward from startDir looking for package.json.
func findProjectRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		dir = startDir
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func projectBinDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".nvx", projectBinShimDirName)
}

func projectNodeModulesBin(projectRoot string) string {
	return filepath.Join(projectRoot, "node_modules", ".bin")
}

// generateProjectBinShims creates nvx wrappers for executables in node_modules/.bin.
func generateProjectBinShims(projectRoot, nvxHome string) error {
	binDir := projectNodeModulesBin(projectRoot)
	entries, err := os.ReadDir(binDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	shimDir := projectBinDir(projectRoot)
	if err := os.MkdirAll(shimDir, 0700); err != nil {
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		exePath = filepath.Join(nvxHome, "nvx")
	}

	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if base == "" || seen[base] {
			continue
		}
		seen[base] = true
		if err := writeProjectBinShim(shimDir, exePath, base); err != nil {
			return err
		}
	}
	return nil
}

func writeProjectBinShim(shimDir, exePath, cmd string) error {
	if runtime.GOOS == "windows" {
		content := fmt.Sprintf("@echo off\r\n%s shim %s %%*\r\n", quoteWindowsBatchArg(exePath), quoteWindowsBatchArg(cmd))
		return writeExecutableFile(filepath.Join(shimDir, cmd+".cmd"), []byte(content))
	}
	content := fmt.Sprintf("#!/bin/sh\nexec %s shim %s \"$@\"\n", quotePOSIXShell(exePath), quotePOSIXShell(cmd))
	return writeExecutableFile(filepath.Join(shimDir, cmd), []byte(content))
}

func isProjectBinCommand(cmdName string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	root := findProjectRoot(cwd)
	if root == "" {
		return false
	}
	binPath := filepath.Join(projectNodeModulesBin(root), cmdName)
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(binPath + ".cmd"); err == nil {
			return true
		}
		if _, err := os.Stat(binPath + ".ps1"); err == nil {
			return true
		}
	}
	if _, err := os.Stat(binPath); err == nil {
		return true
	}
	return false
}

func resolveProjectBinCommand(cmdName string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	root := findProjectRoot(cwd)
	if root == "" {
		return ""
	}
	binDir := projectNodeModulesBin(root)
	if runtime.GOOS == "windows" {
		for _, ext := range []string{".cmd", ".ps1", ""} {
			p := binDir + string(os.PathSeparator) + cmdName + ext
			if ext == "" {
				p = filepath.Join(binDir, cmdName)
			} else {
				p = filepath.Join(binDir, cmdName+ext)
			}
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		return ""
	}
	p := filepath.Join(binDir, cmdName)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}
