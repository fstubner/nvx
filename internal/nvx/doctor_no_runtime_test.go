package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Doctor is unhealthy when a wrapped command has no runtime behind it.
//
// Measured 2026-10-01 on a home with shims, no default version and no node on
// PATH: doctor printed "[OK] intercepting" and exited 0 while `node` itself
// failed with "Could not find real executable for node".
func TestDoctorIsUnhealthyWhenNoRuntimeResolves(t *testing.T) {
	home := t.TempDir()
	seedDoctorShims(t, home)
	restore := reportSandboxLaunchFn
	t.Cleanup(func() { reportSandboxLaunchFn = restore })
	reportSandboxLaunchFn = func(string) bool { return true }

	if code := runDoctorQuietly(t, home); code != 0 {
		t.Skipf("no healthy baseline in this environment (runDoctor = %d)", code)
	}

	// Only the shim dir on PATH: nothing for the shims to hand off to.
	t.Setenv("PATH", filepath.Join(home, "bin"))
	if code := runDoctorQuietly(t, home); code == 0 {
		t.Fatal("no runtime resolves for node, npm or npx and doctor still exited 0")
	}

	// A default version is enough, with nothing else on PATH.
	versionDir := filepath.Join(home, "versions", "node", "v22.1.0")
	bin := nodeBinaryPath(versionDir)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"node", "npm", "npx"} {
		name := filepath.Join(filepath.Dir(bin), cmd)
		switch {
		case cmd == "node" && runtime.GOOS == "windows":
			name += ".exe"
		case runtime.GOOS == "windows":
			// ResolveBinary looks for npm.cmd beside node.exe, not under bin.
			name = filepath.Join(versionDir, cmd+".cmd")
		}
		if err := os.WriteFile(name, []byte("real"), 0o755); err != nil { // #nosec G306 -- fixture
			t.Fatal(err)
		}
	}
	if err := CreateLink(runtimeCurrentLinkPath(home, "node"), versionDir); err != nil {
		t.Fatal(err)
	}
	if code := runDoctorQuietly(t, home); code != 0 {
		t.Errorf("a default version resolves every command and doctor exited %d", code)
	}
}
