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
		return launchDotenvProbe(t, sid, config, scope, guestHome, childExe, targets)
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

// launchDotenvProbe runs the launch's grant step, applyProjectGrants, and then
// childExe contained, in TestLaunchHidesDotenvFromContainedProcess's child
// mode. It returns what the child printed: KEY=READ or KEY=DENIED for each
// KEY=path in targets.
func launchDotenvProbe(t *testing.T, sid uintptr, config SandboxConfig, scope, guestHome, childExe string, targets []string) string {
	t.Helper()
	caps, launchDir, err := applyProjectGrants(config, sid, scope, guestHome, config.WorkDir, nil)
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

// TestLaunchHidesDotenvPastTheRecordCap (NVX_PROBE=1) puts more files named
// like .env.aaa000 than the record holds ahead of a real .env.local, launches,
// and has a contained child read .env.local and the last decoy. The launch hid
// only the first files up to the cap in name order, so the child read
// .env.local. Every file present at launch must be closed to it.
func TestLaunchHidesDotenvPastTheRecordCap(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	const probeProfile = "nvx.sandbox.dotenvcap"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	nvxHome := tempDir(t)
	guestHome := tempDir(t)
	workDir := tempDir(t)
	secret := filepath.Join(workDir, ".env.local")
	if err := os.WriteFile(secret, []byte("DB_PASSWORD=supersecret123"), 0o600); err != nil {
		t.Fatal(err)
	}
	lastDecoy := ""
	for i := 0; i < maxRecordedDotenv+20; i++ {
		lastDecoy = filepath.Join(workDir, fmt.Sprintf(".env.aaa%03d", i))
		if err := os.WriteFile(lastDecoy, []byte("X=1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	childExe := stageProbeChild(t, guestHome, "dotenvcap.exe")
	config := SandboxConfig{NvxHome: nvxHome, WorkDir: workDir}
	out := launchDotenvProbe(t, sid, config, sandboxScopeForWorkDir(workDir), guestHome, childExe,
		[]string{"ENVLOCAL=" + secret, "LASTDECOY=" + lastDecoy})
	t.Logf("%d decoys and .env.local: %q", maxRecordedDotenv+20, out)
	for _, key := range []string{"ENVLOCAL", "LASTDECOY"} {
		if !strings.Contains(out, key+"=DENIED\n") {
			t.Errorf("want %s=DENIED", key)
		}
	}
}

// TestContainedProcessCannotLinkDotenvToAFileItCannotWrite (NVX_PROBE=1) has a
// contained node process try fs.linkSync onto a new dotenv name in the project,
// from files it can write and from files it can only read. openDotenvWithin
// hides a file with several names through all of them. That would let a
// contained process aim nvx at a file outside the project if it could link one
// it may not write. Measured 2026-10-07 on Windows 11 26300 it could not. It
// linked a file it wrote in the project and one in its own home, and was
// refused with EPERM for the node runtime's LICENSE, .git\config and System32's
// hosts file, which it can only read.
func TestContainedProcessCannotLinkDotenvToAFileItCannotWrite(t *testing.T) {
	run, workDir := walkupProbeIn(t, "nvx.sandbox.dotenvlink.probe", `
const fs = require('fs'), path = require('path');
const dir = process.cwd(), home = process.env.USERPROFILE, out = [];
const link = (label, src, dst) => {
  try { fs.linkSync(src, dst); out.push(label + ' LINKED'); }
  catch (e) { out.push(label + ' ' + e.code); }
};
fs.writeFileSync(path.join(dir, 'own-file'), 'x');
fs.writeFileSync(path.join(home, 'own-home-file'), 'x');
link('PROJECT_FILE', path.join(dir, 'own-file'), path.join(dir, '.env.project'));
link('HOME_FILE', path.join(home, 'own-home-file'), path.join(dir, '.env.home'));
link('RUNTIME_FILE', path.join(path.dirname(process.execPath), 'LICENSE'), path.join(dir, '.env'));
link('GIT_CONFIG', path.join(dir, '.git', 'config'), path.join(dir, '.env.git'));
link('SYSTEM_FILE', path.join(process.env.SystemRoot || 'C:\\Windows', 'System32', 'drivers', 'etc', 'hosts'), path.join(dir, '.env.system'));
fs.writeFileSync(process.argv[2], out.join('\n'));
`)
	// The contained process reads .git\config but may not write it, as for any
	// repository nvx launches in.
	if err := os.MkdirAll(filepath.Join(workDir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".git", "config"), []byte("[core]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capSID, err := scopeCapabilitySID(sandboxScopeForWorkDir(workDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := restrictGitMetadataToReadOnly(capSID, workDir); err != nil {
		t.Fatal(err)
	}

	report := run(true)
	t.Logf("%q", report)
	if !strings.Contains(report, "PROJECT_FILE LINKED") {
		t.Fatalf("the contained process could not link a file it wrote, so a refused link below says nothing about permissions: %q", report)
	}
	for _, label := range []string{"RUNTIME_FILE", "GIT_CONFIG", "SYSTEM_FILE"} {
		switch {
		case strings.Contains(report, label+" EXDEV"):
			t.Skipf("%s is on another volume than the project, so its refusal says nothing about permissions: %q", label, report)
		case !strings.Contains(report, label+" EPERM") && !strings.Contains(report, label+" EACCES"):
			t.Errorf("the contained process linked a file it can only read (%s): %q", label, report)
		}
	}
	if _, err := os.Lstat(filepath.Join(workDir, ".env")); err == nil {
		t.Errorf("a .env linked to the runtime's file exists in the project")
	}
}
