//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Windows has two kinds of directory link and they do not behave alike in Go:
//
//	junction  Lstat reports ModeIrregular; filepath.EvalSymlinks returns the path
//	          UNCHANGED, with no error. Creatable without privilege.
//	symlink   Lstat reports ModeSymlink; EvalSymlinks resolves it. Needs Developer
//	          Mode or elevation to create.
//
// Both are followed by os.Stat/os.ReadDir/os.Open. Testing only one would leave
// half the behaviour unpinned, and the junction half specifically defeats the
// obvious "just call EvalSymlinks first" fix.
var dirLinkKinds = []struct {
	name   string
	create func(target, link string) error
}{
	{"junction", func(target, link string) error {
		out, err := runWinCmd(20*time.Second, "cmd", "/c", "mklink", "/J", link, target)
		if err != nil {
			return &linkError{kind: "junction", detail: strings.TrimSpace(string(out)), err: err}
		}
		return nil
	}},
	{"symlink", func(target, link string) error { return os.Symlink(target, link) }},
}

type linkError struct {
	kind   string
	detail string
	err    error
}

func (e *linkError) Error() string { return e.kind + ": " + e.err.Error() + " (" + e.detail + ")" }

// TestStageAppContainerExecutableThroughALinkedDirectory covers the layout nvm for
// Windows produces, which is how most Windows developers install node:
// C:\Program Files\nodejs is a LINK to the active version under
// %APPDATA%\nvm\v<version>, not a real directory.
//
// Staging copies the executable's containing directory, and the previous
// implementation walked it with filepath.Walk -- which reports every path via
// Lstat, so a linked root arrived with IsDir() false and was treated as a file to
// copy. The copy then tried to open the destination directory for writing and the
// whole sandbox launch died with
// "open <nvxHome>\sandbox-exec\<hash>: is a directory", naming neither node nor the
// link, and reading like an internal nvx fault rather than a path it could resolve.
//
// Anyone without an nvx-managed runtime hit this on every single sandboxed command.
func TestStageAppContainerExecutableThroughALinkedDirectory(t *testing.T) {
	for _, kind := range dirLinkKinds {
		t.Run(kind.name, func(t *testing.T) {
			root := tempDir(t)

			realDir := filepath.Join(root, "v24.14.1")
			if err := os.MkdirAll(filepath.Join(realDir, "node_modules", "npm", "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(realDir, "node.exe"), []byte("MZ-fake"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(realDir, "node_modules", "npm", "bin", "npm-cli.js"), []byte("// cli"), 0o600); err != nil {
				t.Fatal(err)
			}

			linked := filepath.Join(root, "nodejs")
			if err := kind.create(realDir, linked); err != nil {
				t.Skipf("this host cannot create a %s: %v", kind.name, err)
			}

			nvxHome := tempDir(t)
			staged, err := stageAppContainerExecutable(nvxHome, filepath.Join(linked, "node.exe"))
			if err != nil {
				t.Fatalf("staging an executable whose directory is a %s failed: %v\n"+
					"This is the nvm-for-Windows layout, so every sandboxed command fails for those users.", kind.name, err)
			}

			got, err := os.ReadFile(staged)
			if err != nil {
				t.Fatalf("staged executable is not readable at %s: %v", staged, err)
			}
			if string(got) != "MZ-fake" {
				t.Errorf("staged executable contents = %q, want the source's", string(got))
			}
			// The whole directory has to come across, not just the entry point:
			// node resolves npm-cli.js and its bundled node_modules relative to
			// itself, and a staged copy that is merely non-empty would still fail
			// later with a missing-module error nowhere near this cause.
			if _, err := os.Stat(filepath.Join(filepath.Dir(staged), "node_modules", "npm", "bin", "npm-cli.js")); err != nil {
				t.Errorf("the staged copy is missing the bundled npm; node would fail to resolve it: %v", err)
			}
		})
	}
}

// TestStageAppContainerExecutableFollowsANestedLinkedDirectory pins that the fix
// covers links BELOW the root too. filepath.Walk does not descend into one, so the
// old code would have created an empty directory in the staged copy and reported
// success -- a runtime missing half its files, failing later and elsewhere.
func TestStageAppContainerExecutableFollowsANestedLinkedDirectory(t *testing.T) {
	root := tempDir(t)
	srcDir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "tool.exe"), []byte("MZ-fake"), 0o700); err != nil {
		t.Fatal(err)
	}

	// A real directory elsewhere, linked to from inside the tree being staged.
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "lib.js"), []byte("// shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dirLinkKinds[0].create(shared, filepath.Join(srcDir, "node_modules")); err != nil {
		t.Skipf("this host cannot create a junction: %v", err)
	}

	nvxHome := tempDir(t)
	staged, err := stageAppContainerExecutable(nvxHome, filepath.Join(srcDir, "tool.exe"))
	if err != nil {
		t.Fatalf("staging with a nested linked directory failed: %v", err)
	}
	linkedFile := filepath.Join(filepath.Dir(staged), "node_modules", "lib.js")
	if _, err := os.Stat(linkedFile); err != nil {
		t.Errorf("contents behind a nested directory link were not staged: %v\n"+
			"The sandbox has no grant outside the staged copy, so a link left unfollowed is a missing file.", err)
	}
}

// TestStageAppContainerExecutableIsIdempotent guards the caching path: staging runs
// on every sandboxed launch, and re-copying a whole node distribution each time
// would be a per-command cost measured in tens of megabytes.
func TestStageAppContainerExecutableIsIdempotent(t *testing.T) {
	srcDir := tempDir(t)
	exe := filepath.Join(srcDir, "tool.exe")
	if err := os.WriteFile(exe, []byte("MZ-fake"), 0o700); err != nil {
		t.Fatal(err)
	}

	nvxHome := tempDir(t)
	first, err := stageAppContainerExecutable(nvxHome, exe)
	if err != nil {
		t.Fatalf("first stage: %v", err)
	}
	second, err := stageAppContainerExecutable(nvxHome, exe)
	if err != nil {
		t.Fatalf("second stage: %v", err)
	}
	if first != second {
		t.Errorf("staging is not stable: %q then %q", first, second)
	}
}
