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
	guestHome := t.TempDir()
	workDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(workDir, "package.json"),
		[]byte(`{"name":"probe","version":"1.0.0","private":true,"scripts":{"hi":"node -e \"console.log('SCRIPT_OK')\""}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	scopeCaps, err := prepareAppContainerFilesystem(sid, guestHome, workDir)
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
		// An nvx-managed runtime under ~/.nvx/versions is a precondition, not the
		// thing under test -- a machine that has node on PATH but has never run
		// `nvx install` resolves to "". Failing here would report an environment gap
		// as a defect. scripts/sandbox-smoke.ps1 stages a runtime itself and covers
		// this same launch path where that matters.
		t.Skipf("no nvx-managed %s runtime staged under %s; run `nvx install` or see scripts/sandbox-smoke.ps1", rt.Name(), nvxHome)
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
		usePath, err := ensureAppContainerCommand(sid, nvxHome, cmdPath)
		if err != nil {
			t.Fatalf("%s: executable access: %v", tc.name, err)
		}
		env := scrubEnvironment(guestHome)
		env = prependPath(env, filepath.Dir(usePath))
		env = setNodeOptionsPreserveSymlinks(env)

		code, err := launchAppContainerProcess(usePath, launchArgs, env, workDir, sid, 0,
			append(scopeCaps, capabilityInternetClientSID))
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
