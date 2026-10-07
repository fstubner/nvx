//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Uninstalling a version whose node.exe is running leaves it whole.
//
// It used to delete everything except the locked node.exe and report the raw
// OS error, leaving a version that was still listed and that `nvx install`
// called installed, with no npm in it. The running node.exe here is a copy of
// this test binary, waiting to be killed. A copy and not a link, because a link
// would be the file this test process is itself running from.
func TestUninstallRefusesAVersionThatIsRunning(t *testing.T) {
	if os.Getenv("NVX_TEST_RUN_UNTIL_KILLED") == "1" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}

	home := tempDir(t)
	const version = "v99.0.0"
	versionDir := filepath.Join(home, "versions", "node", version)
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	nodeExe := filepath.Join(versionDir, "node.exe")
	if err := copyFile(self, nodeExe, 0o700); err != nil {
		t.Fatal(err)
	}
	npmCmd := filepath.Join(versionDir, "npm.cmd")
	if err := os.WriteFile(npmCmd, []byte("@echo npm\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	running := exec.Command(nodeExe, "-test.run=^TestUninstallRefusesAVersionThatIsRunning$")
	running.Env = append(os.Environ(), "NVX_TEST_RUN_UNTIL_KILLED=1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = running.Process.Kill()
			_, _ = running.Process.Wait()
		}
	}
	defer stop()

	err = NodeProvider{}.Uninstall(version, home)
	if err == nil {
		t.Fatal("uninstalling a version whose node.exe is running succeeded")
	}
	if !strings.Contains(err.Error(), "node.exe") {
		t.Errorf("the refusal does not say what is running: %v", err)
	}
	if _, serr := os.Stat(npmCmd); serr != nil {
		t.Fatalf("the refused uninstall deleted files anyway (%v), leaving a version with node.exe and no npm", serr)
	}
	if listed, _ := (NodeProvider{}).ListLocal(home); !slices.Contains(listed, version) {
		t.Fatalf("the refused uninstall left %s unlisted: %v", version, listed)
	}

	// Once nothing runs from it, the uninstall goes through and leaves nothing.
	stop()
	if err := (NodeProvider{}).Uninstall(version, home); err != nil {
		t.Fatalf("uninstall after the process ended: %v", err)
	}
	if listed, _ := (NodeProvider{}).ListLocal(home); slices.Contains(listed, version) {
		t.Fatalf("%s is still listed after a successful uninstall: %v", version, listed)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "versions", "node")); len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the uninstall left %v behind", names)
	}
}
