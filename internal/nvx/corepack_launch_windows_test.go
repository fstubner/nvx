//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Inside the sandbox corepack's pnpm.cmd and yarn.cmd become node.exe on the
// script, with the flags every contained node gets.
func TestTheSandboxStartsCorepacksLaunchersAsNode(t *testing.T) {
	dir := tempDir(t)
	writeStubBinary(t, filepath.Join(dir, "node.exe"))
	for _, name := range []string{"yarn", "pnpm"} {
		writeStubBinary(t, filepath.Join(dir, "node_modules", "corepack", "dist", name+".js"))
		launcher := `"%~dp0\node_modules\corepack\dist\` + name + `.js" %*` + "\r\n"
		if err := os.WriteFile(filepath.Join(dir, name+".CMD"), []byte(launcher), 0o600); err != nil {
			t.Fatal(err)
		}

		path, args := rewriteWindowsNodeCommand(filepath.Join(dir, name+".CMD"), []string{"add", "react@^18.2.0"}, "")
		script := filepath.Join(dir, "node_modules", "corepack", "dist", name+".js")
		want := append(append([]string{}, nodeSandboxPreserveFlags...), script, "add", "react@^18.2.0")
		if path != filepath.Join(dir, "node.exe") || len(args) != len(want) {
			t.Fatalf("%s.CMD became %q %q, want node.exe %q", name, path, args, want)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Errorf("%s.CMD argument %d is %q, want %q", name, i, args[i], want[i])
			}
		}
	}
}

// On Windows corepack writes launchers, and `nvx init-shims` used to delete
// them without a word, leaving `pnpm` to fail with "Could not find real
// executable". The launchers are not nvx's, so they still go, and now it says so.
func TestRegeneratingShimsOnWindowsNamesCorepacksLaunchers(t *testing.T) {
	notQuiet(t)
	nvxHome := tempDir(t)
	corepackLeftovers(t, nvxHome, "yarn", "pnpm", "yarnpkg")
	shimDir := filepath.Join(nvxHome, "bin")
	// An older nvx's own wrapper is replaced without comment.
	if err := os.WriteFile(filepath.Join(shimDir, "npm.cmd"), []byte("@echo off\r\n\"C:\\x\\nvx.exe\" shim npm %*\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderrHere(t, func() {
		if err := generateShims(nvxHome); err != nil {
			t.Fatalf("generateShims: %v", err)
		}
	})

	for _, name := range []string{"yarn", "yarn.CMD", "yarn.ps1", "pnpm", "yarnpkg", "yarnpkg.ps1", "npm.cmd"} {
		if _, err := os.Stat(filepath.Join(shimDir, name)); err == nil {
			t.Errorf("%s is still in the shim directory", name)
		}
	}
	for _, name := range []string{"yarn.exe", "pnpm.exe"} {
		if _, err := os.Stat(filepath.Join(shimDir, name)); err != nil {
			t.Errorf("no %s shim after regeneration: %v", name, err)
		}
	}
	if !strings.Contains(stderr, "yarn") || !strings.Contains(stderr, "corepack enable") {
		t.Errorf("nvx removed corepack's launchers without saying so:\n%s", stderr)
	}
	if strings.Contains(stderr, "npm.cmd") {
		t.Errorf("an older nvx's own wrapper was reported as foreign:\n%s", stderr)
	}
}
