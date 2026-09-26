package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// The working directory is a writable root, so it must never be the home
// directory, anything above it, or anything at or inside ~/.nvx. Measured before
// this guard on Linux and a macOS runner: a contained run started in ~ wrote
// ~/.nvx/grants, ~/.nvx itself and a file in ~.
func TestWorkDirThatReachesHomeOrNvxIsNotGranted(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	nvxHome := filepath.Join(home, ".nvx")
	root := filepath.VolumeName(home) + string(filepath.Separator)

	for _, dir := range []string{home, filepath.Dir(home), root, nvxHome, filepath.Join(nvxHome, "grants")} {
		if !workDirReachesControlPlane(nvxHome, dir) {
			t.Errorf("%s would be granted as the working directory; it reaches ~ or ~/.nvx", dir)
		}
	}
	for _, dir := range []string{filepath.Join(home, "projects", "app"), filepath.Join(home, ".nvx-other")} {
		if workDirReachesControlPlane(nvxHome, dir) {
			t.Errorf("%s was refused; a project folder must stay writable", dir)
		}
	}

	guest := filepath.Join(nvxHome, "sandbox_home", "s1")
	if got := containedWorkDir(nvxHome, guest, home); got != guest {
		t.Errorf("a run from ~ starts in %s, want the guest home", got)
	}
	project := filepath.Join(home, "projects", "app")
	if got := containedWorkDir(nvxHome, guest, project); got != project {
		t.Errorf("a run from a project starts in %s, want the project", got)
	}
}
