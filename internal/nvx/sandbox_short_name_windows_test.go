//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A path spelled with 8.3 short names is the same path.
//
// On GitHub's Windows runners TEMP is C:\Users\RUNNER~1\..., so an NVX_HOME
// made there is spelled short, while filepath.EvalSymlinks hands back the long
// spelling. ensureAppContainerCommand resolves the command that way and then
// asked whether it lay under the home's versions folder. The two spellings
// never matched, so pnpm from a version's npm_global was sent to staging and
// refused with "is not in a Node or Bun install", and every runtime was copied
// for the sandbox instead of granted where it is. Anyone whose profile folder
// has a short alias spells paths both ways.

// shortAndLongSpellings makes a folder whose name has an 8.3 alias and returns
// it spelled short and spelled long, skipping when the volume makes no aliases.
func shortAndLongSpellings(t *testing.T) (short, long string) {
	t.Helper()
	long = filepath.Join(tempDir(t), "a-folder-with-a-long-name")
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	in, err := syscall.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("no short name for %s: %v", long, err)
	}
	short = syscall.UTF16ToString(buf[:n])
	if strings.EqualFold(short, long) {
		t.Skipf("this volume makes no 8.3 names, so %s has no short spelling", long)
	}
	return short, long
}

func TestARuntimeUnderAShortNameHomeIsStillNvxManaged(t *testing.T) {
	short, long := shortAndLongSpellings(t)
	cmd := filepath.Join(long, "versions", "node", "v22.0.0", "npm_global", "pnpm.cmd")
	if err := os.MkdirAll(filepath.Dir(cmd), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmd, []byte("@exit /b 0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shortCmd := filepath.Join(short, "versions", "node", "v22.0.0", "npm_global", "pnpm.cmd")

	for _, c := range []struct{ home, cmd string }{
		{short, cmd},      // the CI case: home spelled short, command resolved long
		{long, shortCmd},  // and the other way round
		{short, shortCmd}, // and both short, which always worked
	} {
		if !isNvxManagedRuntimePath(c.home, c.cmd) {
			t.Errorf("%s is not seen as under the nvx home %s; it would be staged, and a pnpm.cmd refused", c.cmd, c.home)
		}
	}
	if isNvxManagedRuntimePath(short, filepath.Join(filepath.Dir(long), "elsewhere", "pnpm.cmd")) {
		t.Error("a path beside the nvx home was taken for one inside it")
	}
}

// A junction under versions is compared by where it leads. A file it reaches
// in another folder is not nvx's, and neither is a path that cannot be
// resolved. filepath.EvalSymlinks fails on a junction, and the path as given
// was compared instead, so the outside file counted as a runtime nvx manages.
func TestAJunctionUnderVersionsDoesNotMakeAnOutsideFileNvxManaged(t *testing.T) {
	home := tempDir(t)
	outside := tempDir(t)
	nodeDir := filepath.Join(home, "versions", "node")
	runtimeExe := filepath.Join(nodeDir, "v22.0.0", "node.exe")
	for _, p := range []string{runtimeExe, filepath.Join(outside, "evil.exe")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("MZ"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(nodeDir, "sneaky"), outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, out)
	}

	if !isNvxManagedRuntimePath(home, runtimeExe) {
		t.Errorf("%s is not seen as under the nvx home", runtimeExe)
	}
	if p := filepath.Join(nodeDir, "sneaky", "evil.exe"); isNvxManagedRuntimePath(home, p) {
		t.Errorf("%s, reached through a junction to %s, is taken for a runtime nvx manages", p, outside)
	}
	if p := filepath.Join(nodeDir, "v23.0.0", "node.exe"); isNvxManagedRuntimePath(home, p) {
		t.Errorf("%s does not exist and is taken for a runtime nvx manages", p)
	}
}

// The uninstall refusal finds a process started from either spelling.
func TestProcessesRunningFromMatchesEitherSpelling(t *testing.T) {
	short, long := shortAndLongSpellings(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(long, "node.exe")
	if err := copyFile(self, exe, 0o700); err != nil {
		t.Fatal(err)
	}
	// Started by its short spelling, asked about by both. The stand-in waits to
	// be killed, the way TestUninstallRefusesAVersionThatIsRunning uses it.
	running := exec.Command(filepath.Join(short, "node.exe"), "-test.run=^TestUninstallRefusesAVersionThatIsRunning$")
	running.Env = append(os.Environ(), "NVX_TEST_RUN_UNTIL_KILLED=1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = running.Process.Kill()
		_, _ = running.Process.Wait()
	}()
	for _, dir := range []string{short, long} {
		found := false
		for _, p := range processesRunningFrom(dir) {
			if int(p.PID) == running.Process.Pid {
				found = true
			}
		}
		if !found {
			t.Errorf("the process running %s was not found running from %s", exe, dir)
		}
	}
}
