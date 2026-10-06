//go:build windows

package nvx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nvx setup` removes what older versions left and adds nothing.
//
// It used to grant the sandbox read and list access to every fixed volume's
// root and Users folder. The walk-up preload answers the stats that grant was
// for, so setup is now the way to take the entries back. Setup needs an
// Administrator terminal to remove anything, so these inject the machine-touching
// operations, as the tests for the old grant did.

const (
	cleanupCapSID    = "S-1-15-3-1024-1-2-3-4-5-6-7-8"
	cleanupLegacySID = "S-1-15-2-1-2-3-4-5-6-7"
)

// fakeSetupOps records what a setup run does. Every operation succeeds unless a
// test replaces it.
type fakeSetupOps struct {
	ops      setupCleanupOps
	revoked  []setupEntry
	exempt   []string
	cleared  int
	restored []string
}

func newFakeSetupOps(elevated bool, entryOn func(sid, path string) bool) *fakeSetupOps {
	f := &fakeSetupOps{}
	f.ops = setupCleanupOps{
		elevated: func() bool { return elevated },
		hasEntry: entryOn,
		revoke: func(sid, path string) error {
			f.revoked = append(f.revoked, setupEntry{Path: path, SIDs: []string{sid}})
			return nil
		},
		setExempt: func(add bool, sid string) error {
			if add {
				return errors.New("setup must never register an exemption")
			}
			f.exempt = append(f.exempt, sid)
			return nil
		},
		listExempt: func() ([]string, error) { return nil, nil },
		clearState: func(string) error { f.cleared++; return nil },
		// Every profile folder is protected unless a test says otherwise, so the
		// state of the machine running the tests cannot decide a verdict.
		unprotected: func() []unprotectedDir { return nil },
		restoreProtection: func(dir string) error {
			f.restored = append(f.restored, dir)
			return nil
		},
	}
	return f
}

func nothingLeft(string, string) bool { return false }

func systemDriveRoot() string {
	d := os.Getenv("SystemDrive")
	if d == "" {
		d = "C:"
	}
	return filepath.Clean(d + `\`)
}

func runCleanup(t *testing.T, nvxHome string, ops setupCleanupOps) (code int, out string) {
	t.Helper()
	quietFlag = false
	out = captureStderr(t, func() {
		code = runWindowsSetupCleanup(nvxHome, tempDir(t), cleanupCapSID, cleanupLegacySID, ops)
	})
	return code, out
}

// On a machine with nothing to remove, setup says so and succeeds, from a
// terminal that is not elevated: finding entries needs no Administrator rights,
// so there is no reason to refuse.
func TestSetupWithNothingToRemoveSaysSoAndSucceeds(t *testing.T) {
	f := newFakeSetupOps(false, nothingLeft)
	code, out := runCleanup(t, tempDir(t), f.ops)
	if code != 0 {
		t.Fatalf("exit %d on a machine with nothing to remove:\n%s", code, out)
	}
	if !strings.Contains(out, "Nothing to remove") {
		t.Errorf("setup did not say there was nothing to remove:\n%s", out)
	}
	if len(f.revoked)+len(f.exempt)+f.cleared != 0 {
		t.Errorf("setup changed something on a machine with nothing to remove: %+v", f)
	}
}

// Setup performs removals only. Every ACL write it makes carries mask 0, which
// takes an entry away, and none is a grant. The write is stubbed where the real
// one sits, so this holds for the real revoke path too, and it covers every path
// setup looks at, including the drive roots a grant used to go to.
func TestSetupOnlyRemovesAndNeverGrants(t *testing.T) {
	var writes []uint32
	var written []string
	orig := setupACLWrite
	setupACLWrite = func(path, sidStr string, mask uint32) error {
		writes = append(writes, mask)
		written = append(written, path)
		return nil
	}
	t.Cleanup(func() { setupACLWrite = orig })

	f := newFakeSetupOps(true, func(string, string) bool { return true })
	f.ops.revoke = revokeSidGrant
	code, out := runCleanup(t, tempDir(t), f.ops)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(writes) == 0 {
		t.Fatalf("setup found entries everywhere and wrote nothing:\n%s", out)
	}
	for i, m := range writes {
		if m != 0 {
			t.Errorf("setup wrote mask %#x on %s; it must only remove entries", m, written[i])
		}
	}
	for _, r := range fixedDriveRoots() {
		if !setupPathsContain(written, r) {
			t.Errorf("setup left the entry on fixed drive root %s", r)
		}
	}
}

func setupPathsContain(paths []string, want string) bool {
	for _, p := range paths {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(want)) {
			return true
		}
	}
	return false
}

// With entries to remove and no Administrator rights, setup names them and
// stops. It does not claim success and does not try the writes.
func TestSetupNamesWhatItCannotRemoveWithoutElevation(t *testing.T) {
	root := systemDriveRoot()
	f := newFakeSetupOps(false, func(sid, path string) bool {
		return sid == cleanupCapSID && strings.EqualFold(filepath.Clean(path), root)
	})
	code, out := runCleanup(t, tempDir(t), f.ops)
	if code == 0 {
		t.Fatalf("exit 0 with an entry left on %s and no elevation:\n%s", root, out)
	}
	if !strings.Contains(out, root) || !strings.Contains(out, "Administrator") {
		t.Errorf("the output did not name the path and the need for an Administrator terminal:\n%s", out)
	}
	if len(f.revoked) != 0 {
		t.Errorf("setup tried to revoke without elevation: %+v", f.revoked)
	}
}

// Both identities an older setup granted are cleared: the capability, and the
// shared package from before packages were per project.
func TestSetupRemovesBothOlderIdentitiesAndTheRecord(t *testing.T) {
	nvxHome := tempDir(t)
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{AppContainerSID: cleanupLegacySID}); err != nil {
		t.Fatal(err)
	}
	root := systemDriveRoot()
	f := newFakeSetupOps(true, func(sid, path string) bool {
		return strings.EqualFold(filepath.Clean(path), root)
	})
	code, out := runCleanup(t, nvxHome, f.ops)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	got := map[string]bool{}
	for _, r := range f.revoked {
		if strings.EqualFold(r.Path, root) {
			got[r.SIDs[0]] = true
		}
	}
	if !got[cleanupCapSID] || !got[cleanupLegacySID] {
		t.Errorf("setup removed %v on %s; want both identities", got, root)
	}
	if f.cleared != 1 {
		t.Errorf("the record of the earlier setup was cleared %d times, want once", f.cleared)
	}
}

// The paths an older setup recorded are cleaned too, so an entry it made on a
// path the fixed list does not cover does not outlive setup.
func TestSetupRemovesTheRecordedPaths(t *testing.T) {
	nvxHome := tempDir(t)
	recorded := tempDir(t)
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{GrantedPaths: []string{recorded}}); err != nil {
		t.Fatal(err)
	}
	f := newFakeSetupOps(true, func(sid, path string) bool {
		return sid == cleanupCapSID && strings.EqualFold(filepath.Clean(path), filepath.Clean(recorded))
	})
	if code, out := runCleanup(t, nvxHome, f.ops); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(f.revoked) != 1 || !strings.EqualFold(f.revoked[0].Path, filepath.Clean(recorded)) {
		t.Fatalf("setup never removed %s, which an older setup recorded; removed %v", recorded, f.revoked)
	}
}

// Setup must not report success when it could not remove something. A loopback
// exemption left registered is the worst: while it is, the egress allowlist can
// be bypassed through any reachable loopback service.
func TestSetupFailsWhenSomethingCouldNotBeRemoved(t *testing.T) {
	boom := errors.New("access denied")
	somethingThere := func(string, string) bool { return true }

	t.Run("an entry could not be revoked", func(t *testing.T) {
		f := newFakeSetupOps(true, somethingThere)
		f.ops.revoke = func(string, string) error { return boom }
		if code, _ := runCleanup(t, tempDir(t), f.ops); code == 0 {
			t.Fatal("exit 0 while a drive-root entry is still in place")
		}
	})

	t.Run("the loopback exemption stays registered", func(t *testing.T) {
		f := newFakeSetupOps(true, nothingLeft)
		f.ops.listExempt = func() ([]string, error) { return []string{cleanupLegacySID}, nil }
		f.ops.setExempt = func(bool, string) error { return boom }
		if code, _ := runCleanup(t, tempDir(t), f.ops); code == 0 {
			t.Fatal("exit 0 while the loopback exemption is still registered")
		}
	})

	t.Run("the record could not be cleared", func(t *testing.T) {
		nvxHome := tempDir(t)
		if err := writeWindowsSetupState(nvxHome, windowsSetupState{}); err != nil {
			t.Fatal(err)
		}
		f := newFakeSetupOps(true, nothingLeft)
		f.ops.clearState = func(string) error { return boom }
		if code, _ := runCleanup(t, nvxHome, f.ops); code == 0 {
			t.Fatal("exit 0 while nvx still records a setup it says it removed")
		}
	})

	t.Run("the exemption list cannot be read", func(t *testing.T) {
		f := newFakeSetupOps(true, nothingLeft)
		f.ops.listExempt = func() ([]string, error) { return nil, boom }
		code, out := runCleanup(t, tempDir(t), f.ops)
		if code == 0 || strings.Contains(out, "Nothing to remove") {
			t.Fatalf("setup claimed a clean machine without being able to check the exemption list (exit %d):\n%s", code, out)
		}
	})
}

// A registered exemption is removed, never added, and only when one is there.
func TestSetupRemovesALoopbackExemptionOnlyWhenOneIsRegistered(t *testing.T) {
	f := newFakeSetupOps(true, nothingLeft)
	registered := true
	f.ops.listExempt = func() ([]string, error) {
		if registered {
			return []string{cleanupLegacySID}, nil
		}
		return nil, nil
	}
	f.ops.setExempt = func(add bool, sid string) error {
		if add {
			t.Error("setup registered a loopback exemption")
		}
		registered = false
		f.exempt = append(f.exempt, sid)
		return nil
	}
	if code, out := runCleanup(t, tempDir(t), f.ops); code != 0 || len(f.exempt) != 1 {
		t.Fatalf("exit %d, %d removals:\n%s", code, len(f.exempt), out)
	}

	clean := newFakeSetupOps(true, nothingLeft)
	if code, _ := runCleanup(t, tempDir(t), clean.ops); code != 0 || len(clean.exempt) != 0 {
		t.Fatalf("exit %d with %d exemption removals on a machine that has none", code, len(clean.exempt))
	}
}

// Setup must not report a loopback exemption as gone when removing it failed
// and it is still registered. It treated every failed delete, a timeout
// included, as "nothing to remove".
func TestSetupNoticesALoopbackExemptionThatWasNotRemoved(t *testing.T) {
	const legacy = "S-1-15-2-1-2-3-4-5-6-7"
	failDelete := func(bool, string) error { return errors.New("timed out") }
	stillThere := func() ([]string, error) { return []string{legacy}, nil }
	gone := func() ([]string, error) { return nil, nil }

	if removeLegacyLoopbackExemption(legacy, failDelete, stillThere) {
		t.Error("reported the exemption removed while the list still carries it")
	}
	if !removeLegacyLoopbackExemption(legacy, failDelete, gone) {
		t.Error("a failed delete on a machine with no exemption is not a failure")
	}
	if removeLegacyLoopbackExemption(legacy, failDelete, func() ([]string, error) { return nil, errors.New("no tool") }) {
		t.Error("neither removed nor checked, and reported as gone")
	}
}

// The paths setup looks at cover every fixed volume's root, and the volumes the
// profile, the nvx home and the working directory are on, without repeats.
func TestSetupLooksOnEveryFixedVolume(t *testing.T) {
	roots := fixedDriveRoots()
	if len(roots) == 0 {
		t.Skip("no fixed drives reported")
	}
	paths, _ := setupLeftoverPaths(`C:\Users\someone\.nvx`, `C:\Users\someone\project`)
	for _, r := range roots {
		if !setupPathsContain(paths, r) {
			t.Errorf("fixed drive root %q is not among the paths setup looks at: %v", r, paths)
		}
	}
	seen := map[string]int{}
	for _, p := range paths {
		seen[strings.ToLower(p)]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("path %q listed %d times", p, n)
		}
	}
}

// A recorded path that is not present now is named and left out rather than
// failing the run.
func TestSetupNamesARecordedPathThatIsNotPresent(t *testing.T) {
	nvxHome := tempDir(t)
	gone := filepath.Join(tempDir(t), "no-such-volume")
	if err := writeWindowsSetupState(nvxHome, windowsSetupState{GrantedPaths: []string{gone}}); err != nil {
		t.Fatal(err)
	}
	paths, absent := setupLeftoverPaths(nvxHome, tempDir(t))
	if setupPathsContain(paths, gone) {
		t.Errorf("setup would write to %s, which is not there", gone)
	}
	if len(absent) != 1 || absent[0] != gone {
		t.Errorf("absent = %v, want [%s]", absent, gone)
	}
}
