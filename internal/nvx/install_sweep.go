package nvx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sweepAbandonedInstalls removes what an install killed part-way leaves behind:
// staging directories, partial downloads and install locks whose owning process
// is gone. It returns how many things it removed.
//
// A Ctrl-C, a closed laptop or a cancelled CI job leaves "<version>.tmp.<pid>"
// next to the real versions, the half-written download, and a lock. Nothing
// removed them: `nvx cleanup` did not look, and a retry runs under a new pid, so
// it never overwrote the old staging tree either.
//
// Only an owner that is provably gone counts. Pids are reused, so a leftover
// whose pid now belongs to an unrelated live process is left in place and found
// by a later sweep. That costs a directory. The other mistake, taking a live
// install's staging tree, breaks that install. Stale locks go through
// clearAbandonedInstallLock, which has the same bias and which acquiring a lock
// already uses.
func sweepAbandonedInstalls(nvxHome string) int {
	removed := 0
	sweepStaging := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			pid, ok := stagingOwnerPid(e.Name())
			if !ok || pid == os.Getpid() || processIsRunning(pid) {
				continue
			}
			if os.RemoveAll(filepath.Join(dir, e.Name())) == nil {
				removed++
			}
		}
	}

	versionsDir := filepath.Join(nvxHome, "versions")
	runtimes, _ := os.ReadDir(versionsDir)
	for _, rt := range runtimes {
		if !rt.IsDir() {
			continue
		}
		dir := filepath.Join(versionsDir, rt.Name())
		sweepStaging(dir)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
				continue
			}
			lockPath := filepath.Join(dir, e.Name())
			cleared := false
			underStealGuard(dir, func() { cleared = clearAbandonedInstallLock(lockPath) })
			if cleared {
				removed++
			}
		}
	}
	sweepStaging(filepath.Join(nvxHome, "downloads"))
	return removed
}

// stagingOwnerPid reads the pid out of a name an install gave its scratch
// space: "<name>.tmp.<pid>" for a staging directory or download, and Bun's
// "bun-extract-<pid>".
func stagingOwnerPid(name string) (int, bool) {
	digits := ""
	if i := strings.LastIndex(name, ".tmp."); i >= 0 {
		digits = name[i+len(".tmp."):]
	} else if strings.HasPrefix(name, "bun-extract-") {
		digits = strings.TrimPrefix(name, "bun-extract-")
	}
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}
