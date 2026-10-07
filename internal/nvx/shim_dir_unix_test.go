//go:build !windows

package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What only a POSIX shim directory has: corepack's links are symlinks there, and
// nvx's shims are scripts it can replace whole. Windows has its own layout, which
// the tests in shim_dir_test.go and corepack_launch_windows_test.go cover.

// The state `corepack enable` used to leave, regenerated over.
func TestRegeneratingShimsLeavesCorepacksFilesAlone(t *testing.T) {
	notQuiet(t)
	nvxHome := tempDir(t)
	held := corepackDist(t, nvxHome, "yarn", "pnpm", "yarnpkg", "pnpx")
	corepackLeftovers(t, nvxHome, "yarn", "pnpm", "yarnpkg", "pnpx")

	stderr := captureStderrHere(t, func() {
		if err := generateShims(nvxHome); err != nil {
			t.Fatalf("generateShims: %v", err)
		}
	})

	dist := filepath.Join(nvxHome, "versions", "node", "v22.0.0", "lib", "node_modules", "corepack", "dist")
	for name, want := range held {
		if got, _ := os.ReadFile(filepath.Join(dist, name+".js")); string(got) != want {
			t.Errorf("corepack's %s.js was overwritten and now holds %q", name, got)
		}
	}
	shimDir := filepath.Join(nvxHome, "bin")
	for _, name := range []string{"yarn", "pnpm"} {
		content, err := os.ReadFile(filepath.Join(shimDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := shimScriptTarget(string(content), name); !ok {
			t.Errorf("%s in the shim directory is not nvx's shim after regeneration:\n%s", name, content)
		}
		if info, _ := os.Lstat(filepath.Join(shimDir, name)); info == nil || !info.Mode().IsRegular() {
			t.Errorf("%s in the shim directory is still a link", name)
		}
	}
	for _, name := range []string{"yarnpkg", "pnpx"} {
		if _, err := os.Lstat(filepath.Join(shimDir, name)); err == nil {
			t.Errorf("%s, a link into corepack, is still in the shim directory and runs outside nvx", name)
		}
	}
	if !strings.Contains(stderr, "yarn") || !strings.Contains(stderr, "corepack enable") {
		t.Errorf("nvx replaced those without saying what or how to get yarn and pnpm back:\n%s", stderr)
	}
}

// A shim that is already right is not written again, and a wrong one is replaced
// whole. `nvx env` writes every shim at every shell start, and two shells starting
// together must never read a half-written one.
func TestWritingAShimTwiceLeavesTheFileAloneAndReplacesAWrongOneWhole(t *testing.T) {
	dir := tempDir(t)
	shim := filepath.Join(dir, "node")
	if err := writeExecutableFile(shim, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(shim)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mode().Perm() != 0o700 {
		t.Errorf("the shim has mode %v, want 0700", first.Mode().Perm())
	}
	if err := writeExecutableFile(shim, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	again, _ := os.Stat(shim)
	if !os.SameFile(first, again) {
		t.Error("an identical shim was written again")
	}

	if err := writeExecutableFile(shim, []byte("two\n")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(shim); string(got) != "two\n" {
		t.Errorf("the shim holds %q after being replaced", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the write left %d entries behind, want only the shim", len(entries))
	}
}
