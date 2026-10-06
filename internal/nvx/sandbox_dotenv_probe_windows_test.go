//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestLaunchHidesDotenvFromContainedProcess (NVX_PROBE=1) runs the launch's
// grant step, applyProjectGrants, on a throwaway project and then a contained
// child that tries to read each file. .env and .env.local must be closed to
// it, .env.example and package.json open, and the developer must still read
// and write .env. A second launch must not write .env's permissions again, and
// a .env replaced by rename must be closed again by the launch after.
func TestLaunchHidesDotenvFromContainedProcess(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	if os.Getenv("NVX_DOTENV_PROBE_CHILD") == "1" {
		var b strings.Builder
		for _, spec := range strings.Split(os.Getenv("NVX_PROBE_TARGETS"), "|") {
			key, path, _ := strings.Cut(spec, "=")
			if _, err := os.ReadFile(path); err != nil {
				fmt.Fprintf(&b, "%s=DENIED\n", key)
			} else {
				fmt.Fprintf(&b, "%s=READ\n", key)
			}
		}
		fmt.Print(b.String())
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.dotenvlaunch"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	nvxHome := tempDir(t)
	guestHome := tempDir(t)
	workDir := tempDir(t)
	files := map[string]string{
		"ENV":      filepath.Join(workDir, ".env"),
		"ENVLOCAL": filepath.Join(workDir, ".env.local"),
		"EXAMPLE":  filepath.Join(workDir, ".env.example"),
		"PKG":      filepath.Join(workDir, "package.json"),
	}
	for key, p := range files {
		if err := os.WriteFile(p, []byte(key+"=secret-value"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var targets []string
	for key, p := range files {
		targets = append(targets, key+"="+p)
	}
	childExe := stageProbeChild(t, guestHome, "dotenvlaunch.exe")
	config := SandboxConfig{NvxHome: nvxHome, WorkDir: workDir}
	scope := sandboxScopeForWorkDir(workDir)

	launch := func() string {
		t.Helper()
		caps, launchDir, err := applyProjectGrants(config, sid, scope, guestHome, workDir)
		if err != nil {
			t.Fatalf("applyProjectGrants: %v", err)
		}
		read, write := makeTestPipe(t)
		defer syscall.CloseHandle(read)
		prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
		const stdOutputHandle = uintptr(0xFFFFFFF5)
		procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))
		env := append(scrubEnvironment(guestHome),
			"NVX_PROBE=1", "NVX_DOTENV_PROBE_CHILD=1",
			"NVX_PROBE_TARGETS="+strings.Join(targets, "|"))
		_, launchErr := launchAppContainerProcess(childExe,
			[]string{"-test.run=TestLaunchHidesDotenvFromContainedProcess"},
			env, launchDir, sid, 0, caps)
		procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
		syscall.CloseHandle(write)
		got := readWithTimeout(t, read)
		requireAppContainerLaunch(t, launchErr)
		return got
	}
	expect := func(when, out string) {
		t.Helper()
		t.Logf("%s: %q", when, out)
		for key, want := range map[string]string{"ENV": "DENIED", "ENVLOCAL": "DENIED", "EXAMPLE": "READ", "PKG": "READ"} {
			if !strings.Contains(out, key+"="+want+"\n") {
				t.Errorf("%s: want %s=%s", when, key, want)
			}
		}
	}

	expect("first launch", launch())
	env := files["ENV"]
	if b, err := os.ReadFile(env); err != nil || !strings.HasPrefix(string(b), "ENV=") {
		t.Errorf("the developer cannot read .env after a launch: %v", err)
	}
	if err := os.WriteFile(env, []byte("ENV=edited-in-place"), 0o600); err != nil {
		t.Errorf("the developer cannot write .env after a launch: %v", err)
	}

	sddl, changed := dotenvTestSDDL(t, env), dotenvChangeTime(t, env)
	expect("second launch", launch())
	if got := dotenvTestSDDL(t, env); got != sddl {
		t.Errorf("the second launch changed .env's permissions:\n  %s\n  %s", sddl, got)
	}
	if dotenvChangeTime(t, env) != changed {
		t.Errorf("the second launch wrote .env's permissions again")
	}

	// Replace-on-save: a new file renamed over .env inherits the project grant.
	if err := os.WriteFile(env+".tmp", []byte("ENV=replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(env+".tmp", env); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(dotenvTestSDDL(t, env), "D:P") {
		t.Fatalf("the replaced .env does not inherit, so this checks nothing")
	}
	expect("launch after replace-on-save", launch())
}
