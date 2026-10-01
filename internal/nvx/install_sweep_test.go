package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func mkdirT(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func existsT(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// An install killed part-way leaves a staging directory, its download and a
// lock. `nvx cleanup` removed none of them, and a retry runs under a new pid so
// it never overwrote the old staging tree.
//
// Run as the real binary: the sweep has to be reachable from `nvx cleanup`.
func TestCleanupRemovesWhatAKilledInstallLeft(t *testing.T) {
	nvxHome := tempDir(t)
	dead := deadPID(t)
	nodeDir := filepath.Join(nvxHome, "versions", "node")
	staging := filepath.Join(nodeDir, fmt.Sprintf("v22.11.0.tmp.%d", dead))
	download := filepath.Join(nvxHome, "downloads", fmt.Sprintf("node-v22.11.0-win-x64.zip.tmp.%d", dead))
	lock := filepath.Join(nodeDir, "v22.11.0.lock")
	installed := filepath.Join(nodeDir, "v20.18.0")
	mkdirT(t, staging)
	mkdirT(t, installed)
	mkdirT(t, filepath.Dir(download))
	writeFileT(t, filepath.Join(staging, "node.exe"), "half")
	writeFileT(t, download, "partial")
	writeFileT(t, lock, fmt.Sprintf("%d\n", dead))

	exe := filepath.Join(tempDir(t), "nvx"+exeSuffixForTest())
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}
	cmd := execCommandForTest(exe, "cleanup")
	cmd.Env = append(os.Environ(), "NVX_HOME="+nvxHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nvx cleanup failed: %v\n%s", err, out)
	}

	for _, p := range []string{staging, download, lock} {
		if existsT(p) {
			t.Errorf("nvx cleanup left %s", p)
		}
	}
	if !existsT(installed) {
		t.Error("nvx cleanup removed an installed version")
	}
}

// Only a leftover whose owner is gone is removed.
func TestTheSweepLeavesLiveAndUnreadableOwnersAlone(t *testing.T) {
	nvxHome := tempDir(t)
	nodeDir := filepath.Join(nvxHome, "versions", "node")
	mkdirT(t, filepath.Join(nvxHome, "downloads"))
	me := os.Getpid()

	liveStaging := filepath.Join(nodeDir, fmt.Sprintf("v22.11.0.tmp.%d", me))
	liveDownload := filepath.Join(nvxHome, "downloads", fmt.Sprintf("a.zip.tmp.%d", me))
	liveLock := filepath.Join(nodeDir, "v22.11.0.lock")
	garbageLock := filepath.Join(nodeDir, "v24.1.0.lock")
	noPid := filepath.Join(nodeDir, "v20.1.0.tmp.notapid")
	mkdirT(t, liveStaging)
	writeFileT(t, liveDownload, "x")
	writeFileT(t, liveLock, fmt.Sprintf("%d\n", me))
	writeFileT(t, garbageLock, "who knows\n")
	mkdirT(t, noPid)

	if n := sweepAbandonedInstalls(nvxHome); n != 0 {
		t.Errorf("swept %d things with every owner live or unknown", n)
	}
	for _, p := range []string{liveStaging, liveDownload, liveLock, garbageLock, noPid} {
		if !existsT(p) {
			t.Errorf("the sweep removed %s", p)
		}
	}

	dead := deadPID(t)
	deadBun := filepath.Join(nvxHome, "downloads", fmt.Sprintf("bun-extract-%d", dead))
	mkdirT(t, deadBun)
	if n := sweepAbandonedInstalls(nvxHome); n != 1 || existsT(deadBun) {
		t.Errorf("a bun extraction directory of a dead process: swept %d, still there=%v", n, existsT(deadBun))
	}
}

// The pid an install's scratch space names is what the sweep trusts.
func TestStagingOwnerPid(t *testing.T) {
	for name, want := range map[string]int{
		"v22.11.0.tmp.4242":                    4242,
		"node-v22.11.0-linux-x64.tar.gz.tmp.7": 7,
		"bun-extract-99":                       99,
	} {
		if got, ok := stagingOwnerPid(name); !ok || got != want {
			t.Errorf("%s -> %d, %v; want %d", name, got, ok, want)
		}
	}
	for _, name := range []string{"v22.11.0", "v22.11.0.tmp.", "v22.tmp.x1", "bun-extract-", "v22.11.0.lock"} {
		if _, ok := stagingOwnerPid(name); ok {
			t.Errorf("%q read as a staging name", name)
		}
	}
}
