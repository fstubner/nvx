//go:build windows

package main

// Contained npx needs C:\Users and the drive root to answer an lstat, and an
// AppContainer holding no drive-root grant gets EPERM on both. Measured
// 2026-09-03: `npm install` in a project on C: worked, `npx -y cowsay hi` from
// the same project failed with "EPERM: operation not permitted, lstat
// 'C:\Users'" out of npm's own realpath. The preload in sandbox_walkup_shim.js
// answers for the ancestors of the sandbox's working directory and home, and
// that is what lets npx run without an elevated `nvx setup`.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Two preloads both land in NODE_OPTIONS, and each lands once. The check that
// kept a preload from being added twice was keyed on the stdio shim's name, so
// a second preload was silently dropped whenever the first was present.
func TestBothPreloadsLandInNodeOptionsOnce(t *testing.T) {
	env := []string{"NODE_OPTIONS=--preserve-symlinks"}
	env = addNodeOptionsRequire(env, `C:\g\`+stdioShimName)
	env = addNodeOptionsRequire(env, `C:\g\`+walkupShimName)
	env = addNodeOptionsRequire(env, `C:\g\`+walkupShimName)
	env = addNodeOptionsRequire(env, `C:\g\`+stdioShimName)
	var opts string
	for _, e := range env {
		if strings.HasPrefix(strings.ToUpper(e), "NODE_OPTIONS=") {
			opts = e
		}
	}
	if strings.Count(opts, stdioShimName) != 1 || strings.Count(opts, walkupShimName) != 1 {
		t.Fatalf("NODE_OPTIONS should carry each preload exactly once, got %q", opts)
	}
}

// Opt-in verification (NVX_PROBE=1; creates its own AppContainer profile): a
// contained node process holding no drive-root grant can lstat C:\ and
// C:\Users with the preload, and cannot without it. The second half is the
// premise; without it a machine whose roots happen to be granted would pass
// this for the wrong reason.
func TestWalkUpShimAnswersForUnreadableAncestors(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}
	const probeProfile = "nvx.sandbox.walkup.probe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	nvxHome := GetHomeDir()
	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}
	rt := runtimeForShim("node")
	ver := getActiveShellVersionFor(nvxHome, rt.Name())
	if ver == "" {
		ver = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}
	nodePath := resolvePinnedCommandPath("node", nvxHome, ver, rt)
	if nodePath == "" {
		// Fails rather than skips, for the reason spelled out in
		// sandbox_unelevated_windows_test.go: NVX_PROBE=1 is a request to assert,
		// and a gate that skips itself reports success while checking nothing.
		t.Fatalf("no nvx-managed node runtime under %s. This gate cannot assert anything "+
			"without one, so it fails rather than passing quietly.\nRun both:\n"+
			"  nvx -y install 22\n  nvx -y default 22\n"+
			"`nvx use 22` alone is not enough -- it sets the shell version, not the global default.",
			nvxHome)
	}
	nodePath, err = ensureAppContainerCommand(nvxHome, nodePath)
	if err != nil {
		t.Fatalf("executable access: %v", err)
	}

	// The child reports through a file: the launch inherits this process's
	// stdio, which the test cannot read back.
	report := filepath.Join(workDir, "report.txt")
	script := filepath.Join(workDir, "probe.js")
	if err := os.WriteFile(script, []byte(`
const fs = require('fs');
const out = [];
for (const p of ['C:\\', 'C:\\Users']) {
  try { fs.lstatSync(p); out.push('ok ' + p); } catch (e) { out.push(e.code + ' ' + p); }
}
fs.writeFileSync(process.argv[2], out.join('\n'));
`), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(withShim bool) string {
		_ = os.Remove(report)
		env := scrubEnvironment(guestHome)
		env = prependPath(env, filepath.Dir(nodePath))
		env = setNodeOptionsPreserveSymlinks(env)
		if withShim {
			shim, err := writeWalkupShim(guestHome)
			if err != nil {
				t.Fatal(err)
			}
			env = addNodeOptionsRequire(env, shim)
		}
		_, launchArgs := rewriteWindowsNodeCommand(nodePath, []string{script, report}, nodePath)
		code, err := launchAppContainerProcess(nodePath, launchArgs, env, workDir, sid, 0,
			launchCapabilitySIDs(scopeCaps, nil))
		if err != nil {
			t.Skipf("this host cannot create AppContainer children: %v", err)
		}
		got, rerr := os.ReadFile(report)
		if rerr != nil {
			t.Fatalf("child exit %d and no report: %v", code, rerr)
		}
		return string(got)
	}

	without := run(false)
	if !strings.Contains(without, "EPERM C:\\Users") {
		t.Skipf("premise not met: this identity can already stat C:\\Users (a drive-root grant applies here), so the shim has nothing to answer for:\n%s", without)
	}
	with := run(true)
	if !strings.Contains(with, "ok C:\\") || !strings.Contains(with, "ok C:\\Users") {
		t.Fatalf("with the preload, an lstat on the drive root or C:\\Users still failed; contained npx would die in npm's realpath:\n%s", with)
	}
}
