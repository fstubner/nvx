package nvx

import (
	"os"
	"path/filepath"
	"strings"
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

	guest := tempDir(t)
	if got := containedWorkDir(nvxHome, guest, home); filepath.Dir(got) != guest {
		t.Errorf("a run from ~ starts in %s, want a folder inside the guest home", got)
	}
	project := filepath.Join(home, "projects", "app")
	if got := containedWorkDir(nvxHome, guest, project); got != project {
		t.Errorf("a run from a project starts in %s, want the project", got)
	}
}

// What a command writes to the folder it was moved to cannot be kept, so the
// run names it and does not report success. Measured 2026-10-07 on Windows
// from C:\Users: a contained scaffold printed "Done", exited 0, and its folder
// was deleted with the sandbox's home.
func TestARelocatedRunThatWroteToItsWorkingFolderDoesNotSucceed(t *testing.T) {
	guest := tempDir(t)

	dir := relocatedWorkDir(guest)
	if filepath.Dir(dir) != guest {
		t.Fatalf("the stand-in folder %s is not inside the guest home %s", dir, guest)
	}
	if code := reportRelocatedWrites("npm", dir, 0); code != 0 {
		t.Errorf("a run that wrote nothing to its working folder exited %d, want 0", code)
	}

	dir = relocatedWorkDir(guest)
	if err := os.Mkdir(filepath.Join(dir, "myvite"), 0o700); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStderrHere(t, func() { code = reportRelocatedWrites("npm", dir, 0) })
	if code != exitRefused {
		t.Errorf("a run whose output was deleted exited %d, want %d", code, exitRefused)
	}
	if !strings.Contains(out, "myvite") {
		t.Errorf("the run did not name what it deleted:\n%s", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the stand-in folder is still there after the run said it was deleted: %v", err)
	}

	// A command that failed keeps its own code.
	dir = relocatedWorkDir(guest)
	if err := os.WriteFile(filepath.Join(dir, "partial"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := reportRelocatedWrites("npm", dir, 3); code != 3 {
		t.Errorf("a failed run exited %d, want its own 3", code)
	}

	// A persistent profile keeps its home between runs, so the folder starts
	// empty every time rather than reporting an earlier run's files.
	dir = relocatedWorkDir(guest)
	if err := os.WriteFile(filepath.Join(dir, "earlier"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir = relocatedWorkDir(guest)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the stand-in folder kept %d entries from an earlier run", len(entries))
	}
}
