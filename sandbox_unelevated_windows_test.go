//go:build windows

package main

// Opt-in verification (NVX_PROBE=1; ~2 min, creates its own AppContainer profile)
// the AppContainer sandbox can run npm WITHOUT the elevated `nvx setup` grants.
// It uses a throwaway profile name, so its SID has no grants anywhere -- exactly
// the state of a machine where setup was never run -- and grants only what nvx
// itself can grant unelevated. Existing ACLs are untouched.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUnelevatedSandboxRunsPackageManager(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	const probeProfile = "nvx.sandbox.probe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	sidStr, _ := appContainerSidToString(sid)
	t.Logf("probe SID: %s", sidStr)

	// Confirm the premise: this SID must have no access to the system drive root.
	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		sysDrive = "C:"
	}
	if appContainerHasGrant(sidStr, sysDrive+`\`) {
		t.Fatalf("premise broken: probe SID already has access to %s\\", sysDrive)
	}
	t.Logf("confirmed: probe SID has NO grant on %s\\ (unelevated-equivalent state)", sysDrive)

	nvxHome := GetHomeDir()
	guestHome := tempDir(t)
	workDir := tempDir(t)

	if err := os.WriteFile(filepath.Join(workDir, "package.json"),
		[]byte(`{"name":"probe","version":"1.0.0","private":true,"scripts":{"hi":"node -e \"console.log('SCRIPT_OK')\""}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	rt := runtimeForShim("npm")
	ver := getActiveShellVersionFor(nvxHome, rt.Name())
	if ver == "" {
		ver = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}
	npmPath := resolvePinnedCommandPath("npm", nvxHome, ver, rt)
	if npmPath == "" {
		// A missing runtime is an environment gap, not a defect -- but this test
		// only runs because someone set NVX_PROBE=1, and the whole point of that
		// is to assert. Skipping here made the release gate report success while
		// verifying nothing about the claim this test exists for: that a sandbox
		// with no drive-root grant can run a package manager.
		//
		// It was worse than a silent skip, because the message named only half
		// the fix. `nvx use 22` leaves this nil -- the probe accepts an active
		// shell version OR a global default, and `use` writes the former into a
		// shell nothing here inherits. Measured 2026-09-03: a probe run skipped
		// 45 tests without `nvx default`, and 6 with it.
		t.Fatalf("no nvx-managed %s runtime staged under %s. This gate cannot assert anything "+
			"without one, so it fails rather than passing quietly.\nRun both:\n"+
			"  nvx -y install 22\n  nvx -y default 22\n"+
			"`nvx use 22` alone is not enough -- it sets the shell version, not the global default.",
			rt.Name(), nvxHome)
	}
	t.Logf("npm resolved to: %s", npmPath)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"npm -v", []string{"-v"}},
		{"npm run hi", []string{"run", "hi"}},
	} {
		cmdPath, launchArgs := rewriteWindowsNodeCommand(npmPath, tc.args, resolveSandboxNodeExe(nvxHome))
		usePath, err := ensureAppContainerCommand(nvxHome, cmdPath)
		if err != nil {
			t.Fatalf("%s: executable access: %v", tc.name, err)
		}
		env := scrubEnvironment(guestHome)
		env = prependPath(env, filepath.Dir(usePath))
		env = setNodeOptionsPreserveSymlinks(env)

		code, err := launchAppContainerProcess(usePath, launchArgs, env, workDir, sid, 0,
			launchCapabilitySIDs(scopeCaps, []string{capabilityInternetClientSID}))
		if err != nil {
			t.Errorf("%s: launch error: %v", tc.name, err)
			continue
		}
		if code != 0 {
			t.Errorf("%s: exit code %d (FAILED unelevated)", tc.name, code)
			continue
		}
		t.Logf("%s: exit 0 -- WORKS without elevated setup", tc.name)
	}
}
