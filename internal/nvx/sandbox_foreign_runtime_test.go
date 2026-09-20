package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The root is computed from where the command sits on PATH, never from what a
// symlink points at.
//
// This is the bug the spike actually hit. `npm` in a Node install is a link
// into <version>/lib/node_modules/npm/bin/npm-cli.js. Following it first and
// climbing from there produced <version>/lib/node_modules/npm -- npm's own
// JavaScript, granted, and node.exe left out -- so the contained install got
// as far as "/usr/bin/env: 'node': Permission denied". Measured on Linux
// 2026-09-20 before this rule existed.
func TestForeignRootComesFromThePathEntryNotTheSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs Developer Mode on Windows; the rule is platform-independent")
	}
	home := t.TempDir()
	version := filepath.Join(home, "foreign", "node", "v22.0.0")
	binDir := filepath.Join(version, "bin")
	deep := filepath.Join(version, "lib", "node_modules", "npm", "bin")
	for _, d := range []string{binDir, deep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(deep, "npm-cli.js")
	if err := os.WriteFile(target, []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "npm")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	got := foreignRuntimeReadExecRoot(filepath.Join(home, "nvx"), link)
	if got != version {
		t.Fatalf("root for %s = %q, want the version root %q. Granting the link's target instead "+
			"leaves node itself unreadable and the contained command cannot start.", link, got, version)
	}
}

// A runtime nvx installed needs nothing added: its tree is already granted.
func TestNvxManagedRuntimeAddsNoRoot(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, "versions", "node", "v22.0.0", "bin", "node")
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := foreignRuntimeReadExecRoot(home, managed); got != "" {
		t.Fatalf("an nvx-managed runtime asked for an extra root (%q); its versions/ tree is granted already", got)
	}
}

// A runtime installed into a shared system prefix must not hand the sandbox
// that prefix. Granting /usr because node lives in /usr/bin would let a
// contained install read every program on the machine, which is the opposite
// of what the sandbox is for.
func TestASharedSystemPrefixIsNeverGrantedWhole(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Unix prefixes under test are not the Windows ones")
	}
	for _, bin := range []string{"/usr/bin/node", "/usr/local/bin/node", "/bin/node"} {
		got := foreignRuntimeReadExecRoot(t.TempDir(), bin)
		for _, forbidden := range []string{"/usr", "/usr/local", "/", "/bin"} {
			if got == forbidden {
				t.Errorf("a runtime at %s granted %q, a shared prefix holding every program on the machine", bin, got)
			}
		}
	}
}

// The climb is one level and only from a directory named bin. Windows layouts
// put the binary at the version root, where there is nothing to climb.
func TestTheClimbStopsAtTheVersionRoot(t *testing.T) {
	home := t.TempDir()
	nvxHome := filepath.Join(home, "nvx")

	flat := filepath.Join(home, "tools", "node-v22", "node.exe")
	if err := os.MkdirAll(filepath.Dir(flat), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := foreignRuntimeReadExecRoot(nvxHome, flat), filepath.Dir(flat); got != want {
		t.Errorf("a binary at the version root granted %q, want %q", got, want)
	}

	nested := filepath.Join(home, "tools", "node-v23", "bin", "node")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "tools", "node-v23")
	if got := foreignRuntimeReadExecRoot(nvxHome, nested); got != want {
		t.Errorf("a binary in bin/ granted %q, want the version root %q", got, want)
	}
}
