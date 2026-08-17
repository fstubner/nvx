package main

import (
	"path"
	"strings"
	"testing"
)

// macOS paths are POSIX. Building them with filepath on Windows yields backslashes,
// which the Seatbelt writer then escapes, so the assertions would compare against a
// path shape that never occurs on the target platform.
func macPath(elem ...string) string { return path.Join(elem...) }

// F65: content-hash policy pinning is sound in isolation, but it binds only while
// the pin store is beyond the contained process's reach. The store is
// ~/.nvx/grants/<key>.json, so any containment profile that makes ~/.nvx writable
// lets a contained process forge its own pin -- drop a .nvx-policy.json setting
// network.mode: open or typosquatting.enabled: false, write that file's SHA256
// into the grants file, and it is "trusted" on the next run. On macOS that was
// exactly the case (F22).
//
// The live hole is fixed on all three platforms. These tests guard the class: the
// control plane must not be writable, and it must stay that way as the write scope
// is edited in future.

// nvxControlPlanePaths are the parts of ~/.nvx whose integrity the trust model
// depends on. Writable means a contained process can arrange its own trust.
var nvxControlPlanePaths = []struct {
	rel string
	why string
}{
	{"", "the control-plane root; granting it grants everything below"},
	{"policy.json", "the trust baseline every project policy is compared against"},
	{"grants", "policy pins, approved egress hosts and trusted tools"},
	{"cache", "maps command names to absolute paths nvx later executes"},
	{"tool_home", "other tools' persisted credentials"},
	{"versions", "the runtime binaries; writable means the interpreter can be trojaned"},
	{"bin", "the shims PATH resolves inside the sandbox"},
}

func TestSandboxWritableRootsExcludesControlPlane(t *testing.T) {
	nvxHome := macPath("/Users/testuser", ".nvx")
	// The realistic layout: the guest home is nested INSIDE the control plane.
	guestHome := macPath(nvxHome, "sandbox_home", "session1")
	workDir := "/Users/testuser/projects/app"

	roots := sandboxWritableRoots(guestHome, workDir)

	for _, cp := range nvxControlPlanePaths {
		forbidden := macPath(nvxHome, cp.rel)
		for _, root := range roots {
			if path.Clean(root) == path.Clean(forbidden) {
				t.Errorf("%q is a writable root but must not be: %s", forbidden, cp.why)
			}
		}
	}
}

// TestSandboxWritableRootsIsExactlyGuestHomeAndWorkDir pins the set. A future
// change that adds a third root fails here, which is the assertion F22 lacked.
func TestSandboxWritableRootsIsExactlyGuestHomeAndWorkDir(t *testing.T) {
	guestHome := "/Users/testuser/.nvx/sandbox_home/session1"
	workDir := "/Users/testuser/projects/app"

	roots := sandboxWritableRoots(guestHome, workDir)
	if len(roots) != 2 || roots[0] != guestHome || roots[1] != workDir {
		t.Fatalf("writable roots = %v, want exactly [%s %s]. Adding a root is a write-containment decision -- if deliberate, update this test and say why.", roots, guestHome, workDir)
	}
}

func TestSandboxWritableRootsSkipsEmptyPaths(t *testing.T) {
	// An empty path would render as a rule covering an unintended location rather
	// than being ignored, so neither may be emitted.
	if roots := sandboxWritableRoots("", ""); len(roots) != 0 {
		t.Errorf("empty inputs produced roots %v, want none", roots)
	}
	if roots := sandboxWritableRoots("/guest", ""); len(roots) != 1 || roots[0] != "/guest" {
		t.Errorf("roots = %v, want just [/guest]", roots)
	}
}

// TestSeatbeltProfileNeverGrantsWriteToControlPlane checks the same invariant
// through the real macOS profile text, since that is the platform where it failed.
func TestSeatbeltProfileNeverGrantsWriteToControlPlane(t *testing.T) {
	nvxHome := "/Users/testuser/.nvx"
	guestHome := macPath(nvxHome, "sandbox_home", "session1")

	profile := buildSeatbeltProfile(
		NetworkLaunchContext{Mode: "proxy"},
		guestHome,
		"/Users/testuser/projects/app",
	)
	writes := seatbeltWriteSection(t, profile)

	for _, cp := range nvxControlPlanePaths {
		target := macPath(nvxHome, cp.rel)
		for _, form := range []string{
			`(subpath "` + target + `")`,
			`(literal "` + target + `")`,
		} {
			if strings.Contains(writes, form) {
				t.Errorf("Seatbelt grants write to %s via %s: %s", target, form, cp.why)
			}
		}
	}

	// The guest home must still be writable, or nothing can run.
	if !strings.Contains(writes, guestHome) {
		t.Errorf("guest home %q is not writable; the sandbox would have nowhere to write:\n%s", guestHome, writes)
	}
}
