package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeVersionComponentAcceptsRealVersions(t *testing.T) {
	for _, v := range []string{
		"v20.0.0", "20.0.0", "1.2.19", "v22.11.0",
		"1.0.0-rc.1", "1.0.0-beta", "1.0.0+build.5",
		"bun-v1.1.0", "v1.2.3_alpha",
	} {
		if err := safeVersionComponent(v); err != nil {
			t.Errorf("safeVersionComponent(%q) rejected a legitimate version: %v", v, err)
		}
	}
}

// TestSafeVersionComponentRejectsTraversal is the point of the check: these are the
// strings that would make an install directory resolve outside ~/.nvx/versions, and
// the install path calls os.RemoveAll on that directory.
func TestSafeVersionComponentRejectsTraversal(t *testing.T) {
	for _, v := range []string{
		"..",
		".",
		"../../../etc",
		"../..",
		"v20/../../..",
		"/etc/passwd",
		`..\..\Windows`,
		`C:\Windows`,
		"v20.0.0/../../..",
		"foo/bar",
		`foo\bar`,
		"",
	} {
		if err := safeVersionComponent(v); err == nil {
			t.Errorf("safeVersionComponent(%q) accepted a version that escapes the versions directory", v)
		}
	}
}

func TestSafeVersionComponentRejectsOddInput(t *testing.T) {
	for _, v := range []string{
		"v20\x00",                // NUL truncation
		"v20\n1.0",               // newline
		"v 20",                   // space
		"v20;rm -rf",             // shell metacharacters
		strings.Repeat("9", 129), // over-long
	} {
		if err := safeVersionComponent(v); err == nil {
			t.Errorf("safeVersionComponent(%q) accepted implausible input", v)
		}
	}
}

// TestSafeVersionComponentActuallyKeepsPathsInsideVersionsDir ties the character
// check to the property it exists to guarantee, rather than trusting that the
// allowlist implies containment.
func TestSafeVersionComponentActuallyKeepsPathsInsideVersionsDir(t *testing.T) {
	nvxHome := tempDir(t)
	versionsRoot := filepath.Clean(filepath.Join(nvxHome, "versions", "node"))

	for _, v := range []string{"v20.0.0", "1.0.0-rc.1", "1.0.0+build.5", "v22.11.0"} {
		if err := safeVersionComponent(v); err != nil {
			t.Fatalf("%q should be accepted: %v", v, err)
		}
		got := filepath.Clean(filepath.Join(versionsRoot, v))
		if !strings.HasPrefix(got, versionsRoot+string(os.PathSeparator)) {
			t.Errorf("accepted version %q produced %q, which is outside %q", v, got, versionsRoot)
		}
	}
}

// TestUnguardedVersionEscapesToADeletableDirectory documents why the guard exists.
// It builds the same path the install and uninstall paths build, with a version
// string that was previously accepted, and shows it resolves onto a directory
// outside the versions tree -- the directory os.RemoveAll is then called on.
func TestUnguardedVersionEscapesToADeletableDirectory(t *testing.T) {
	nvxHome := tempDir(t)

	// A sentinel standing in for whatever the traversal would land on.
	sentinel := filepath.Join(nvxHome, "grants")
	if err := os.MkdirAll(sentinel, 0o700); err != nil {
		t.Fatal(err)
	}

	hostile := filepath.Join("..", "..", "grants")
	escaped := filepath.Clean(filepath.Join(nvxHome, "versions", "node", hostile))

	if escaped != filepath.Clean(sentinel) {
		t.Fatalf("expected the traversal to land on %q, got %q", sentinel, escaped)
	}
	// So an unguarded os.RemoveAll(escaped) would delete the grants store.
	if err := safeVersionComponent(hostile); err == nil {
		t.Error("the guard must reject this version; without it, installing or uninstalling it deletes the directory it resolves onto")
	}
}
