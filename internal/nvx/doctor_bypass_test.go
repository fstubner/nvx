package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A wrapped command that resolves outside the shim dir makes doctor unhealthy.
//
// Doctor listed such a command as "[FAIL] npm -> ... (bypasses nvx)" and then
// said "nvx is intercepting commands correctly" and exited 0, because only
// nvx's own runtime dirs counted as shadowing. A system Node ahead of the shim
// dir -- Machine PATH before User PATH on Windows -- is the common case.
func TestACommandThatBypassesNvxMakesDoctorUnhealthy(t *testing.T) {
	home := t.TempDir()
	seedDoctorShims(t, home)
	restore := reportSandboxLaunchFn
	t.Cleanup(func() { reportSandboxLaunchFn = restore })
	reportSandboxLaunchFn = func(string) bool { return true }

	if code := runDoctorQuietly(t, home); code != 0 {
		t.Skipf("no healthy baseline in this environment (runDoctor = %d)", code)
	}

	// A real npm, earlier on PATH than the shim dir.
	system := t.TempDir()
	name := "npm"
	if runtime.GOOS == "windows" {
		name = "npm.exe"
	}
	if err := os.WriteFile(filepath.Join(system, name), []byte("real npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", system+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code := runDoctorQuietly(t, home); code == 0 {
		t.Fatal("npm resolves outside the shim dir and doctor still exited 0; it would say nvx is intercepting commands correctly while npm runs unwrapped")
	}
}
