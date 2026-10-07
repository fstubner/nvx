//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The batch files npm writes run inside the sandbox.
//
// pnpm and yarn installed with `npm install -g`, and a project's own bins, reach
// Windows as batch files. Every one of them failed contained with a bare
// "Access is denied." from cmd.exe, and a project bin under strict isolation
// was refused before that with "is not in a Node or Bun install". See
// windowsBatchLaunch and commandRunsInPlace.
//
// Through the built binary and a real AppContainer, with the shims npm 10
// writes. The node behind them is a copy of the one on PATH, in a version
// directory of a scratch NVX_HOME, and pnpm and tsc are stand-ins that print
// what they were given, so nothing here needs the network.

// npmCmdShim is the batch file npm 10.9.9 writes for a bin, as written into
// npm_global for `npm install -g pnpm` on 2026-10-07. target is the script,
// relative to the shim's own folder.
func npmCmdShim(target string) string {
	return "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n" +
		"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n" +
		"  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\n" +
		"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\" + target + "\" %*\r\n"
}

type batchProbeFixture struct {
	nvx, home, project string
	env                []string
}

// newBatchProbeFixture builds nvx and lays out a scratch NVX_HOME
// holding one Node version with pnpm in its npm_global, and a project with a
// tsc bin. env is the environment to run the built nvx with. It has the
// version's npm_global and its own folder on PATH, which is what `nvx use` puts
// there.
func newBatchProbeFixture(t *testing.T) batchProbeFixture {
	t.Helper()
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	// Asked of node itself. `node` on PATH can be nvx's own shim, and a copy of
	// that standing in for node.exe runs the shim again, for ever.
	execPath, err := exec.Command("node", "-p", "process.execPath").Output()
	if err != nil {
		t.Skip("node is not installed; the contained programs need it")
	}
	hostNode := strings.TrimSpace(string(execPath))

	nvxExe := filepath.Join(tempDir(t), "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, "github.com/fstubner/nvx/cmd/nvx").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}

	home := tempDir(t)
	const version = "v22.0.0"
	versionDir := filepath.Join(home, "versions", "node", version)
	npmGlobal := filepath.Join(versionDir, "npm_global")
	project := tempDir(t)

	files := map[string]string{
		filepath.Join(npmGlobal, "pnpm.cmd"):                                npmCmdShim(`node_modules\pnpm\bin\pnpm.cjs`),
		filepath.Join(npmGlobal, "node_modules", "pnpm", "bin", "pnpm.cjs"): `console.log("PROBE pnpm ran " + process.argv.slice(2).join(" "));`,
		filepath.Join(project, "package.json"):                              `{"name":"batch-probe","version":"1.0.0"}`,
		filepath.Join(project, "node_modules", ".bin", "tsc.cmd"):           npmCmdShim(`..\typescript\bin\tsc`),
		filepath.Join(project, "node_modules", "typescript", "bin", "tsc"):  `console.log("PROBE tsc ran " + process.argv.slice(2).join(" "));`,
	}
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A copy and not a link. The sandbox's grant on the version is written to
	// the file, and a link would carry it onto the machine's own node.exe.
	if err := copyFile(hostNode, filepath.Join(versionDir, "node.exe"), 0o700); err != nil {
		t.Fatal(err)
	}
	// What `nvx install` does for a version it has just put on disk.
	pregrantRuntimeForSandbox(home, "node", version)

	sys, err := systemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	path := strings.Join([]string{npmGlobal, versionDir, sys, filepath.Dir(sys)}, string(os.PathListSeparator))
	env := []string{"NVX_HOME=" + home, "PATH=" + path}
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if !strings.EqualFold(k, "PATH") && !strings.EqualFold(k, "NVX_HOME") && !strings.HasPrefix(strings.ToUpper(k), "NPM_CONFIG_") {
			env = append(env, e)
		}
	}
	return batchProbeFixture{nvx: nvxExe, home: home, project: project, env: env}
}

func (f batchProbeFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(f.nvx, args...)
	cmd.Dir = f.project
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	got := string(out)
	requireContainedRunLaunched(t, got)
	return got, err
}

// pnpm installed with `npm install -g` runs contained. --strict contains a
// command that is not an install, so the stand-in needs no network.
func TestProbePnpmInstalledWithNpmRunsContained(t *testing.T) {
	f := newBatchProbeFixture(t)
	out, err := f.run(t, "--strict", "shim", "pnpm", "hello", "a b")
	if !strings.Contains(out, "PROBE pnpm ran hello a b") {
		t.Fatalf("contained pnpm from npm_global did not run (%v):\n%s", err, out)
	}
	if err != nil {
		t.Errorf("contained pnpm printed its result but exited with %v:\n%s", err, out)
	}
}

// A project's own bin runs contained under strict isolation.
func TestProbeStrictProjectBinShimRunsContained(t *testing.T) {
	f := newBatchProbeFixture(t)
	out, err := f.run(t, "--strict", "shim", "tsc", "--noEmit")
	if !strings.Contains(out, "PROBE tsc ran --noEmit") {
		t.Fatalf("the project's tsc did not run under strict isolation (%v):\n%s", err, out)
	}
	if err != nil {
		t.Errorf("tsc printed its result but exited with %v:\n%s", err, out)
	}
}

// nvx doctor runs a batch file in the sandbox, and pnpm when it is installed.
// It reported a healthy sandbox while every contained pnpm and yarn failed.
// The scratch home has no shims, so doctor's overall verdict is not the point
// here, only these lines.
func TestProbeDoctorChecksBatchFilesAndPnpm(t *testing.T) {
	f := newBatchProbeFixture(t)
	out, _ := f.run(t, "doctor")
	for _, want := range []string{
		"[OK]   the sandbox runs batch files",
		"[OK]   pnpm runs in the sandbox",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor did not report %q:\n%s", want, out)
		}
	}
}

// A contained node starts when its own file has lost the sandbox's entry and
// its folder still has it.
//
// That is the state an icacls listing recorded on a real NVX_HOME where every
// contained command failed with "fork/exec ...\node.exe: Access is denied."
// The grant check read the folder, found its entry and went no further.
func TestProbeContainedNodeStartsWhenItsFileLacksTheSandboxEntry(t *testing.T) {
	f := newBatchProbeFixture(t)
	runtimeCap, err := runtimeCapabilitySID()
	if err != nil {
		t.Fatal(err)
	}
	icacls, err := systemToolPath("icacls.exe")
	if err != nil {
		t.Fatal(err)
	}
	nodeExe := filepath.Join(f.home, "versions", "node", "v22.0.0", "node.exe")
	for _, args := range [][]string{{nodeExe, "/inheritance:d"}, {nodeExe, "/remove:g", "*" + runtimeCap}} {
		if out, err := exec.Command(icacls, args...).CombinedOutput(); err != nil {
			t.Fatalf("icacls %v: %v\n%s", args, err, out)
		}
	}
	if appContainerHasGrantFor(runtimeCap, nodeExe, grantReadExec) {
		t.Fatal("node.exe still grants the sandbox read and execute; the state under test was not made")
	}

	// Built at run time, so nvx echoing the command line cannot satisfy the check.
	out, err := f.run(t, "--strict", "shim", "node", "-e", `console.log("PROBE node " + "ran")`)
	if !strings.Contains(out, "PROBE node ran") {
		t.Fatalf("contained node did not start (%v):\n%s", err, out)
	}
	// Started from this node.exe, not from a staged copy of its folder. A copy
	// starts too, which is how this passed with the temp folder spelled in 8.3
	// names while the launch failed to see the runtime as nvx's.
	if !appContainerHasGrantFor(runtimeCap, nodeExe, grantReadExec) {
		t.Error("node ran, but node.exe still lacks the sandbox's read and execute; the launch went around it")
	}
}

// A project's batch shim gets batchArgCases as they were given, run contained
// under strict isolation and run uncontained, through the built binary. None
// of them runs a command of its own.
func TestProbeBatchShimArgumentsArriveIntact(t *testing.T) {
	f := newBatchProbeFixture(t)
	// A test before this one can leave this process marked as a shim's, and the
	// built nvx would then not say which way it ran.
	f.env = slices.DeleteFunc(slices.Clone(f.env), func(e string) bool {
		return strings.HasPrefix(strings.ToUpper(e), nvxActiveEnvVar+"=")
	})
	script := filepath.Join(f.project, "node_modules", "typescript", "bin", "tsc")
	if err := os.WriteFile(script, []byte(`console.log("ARGV" + JSON.stringify(process.argv.slice(2)))`), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(f.project, "injected")
	want := append(batchArgCases(marker), "last")
	for _, mode := range []struct {
		name, says string
		flags      []string
	}{
		{"contained", "Running in native sandbox", []string{"--strict", "--verbose"}},
		{"uncontained", "Running directly (not sandboxed)", []string{"--verbose"}},
	} {
		args := append(append(mode.flags, "shim", "tsc"), want...)
		out, err := f.run(t, args...)
		if !strings.Contains(out, mode.says) {
			t.Errorf("%s: the run does not say %q, so it may not be the launch under test (%v):\n%s", mode.name, mode.says, err, out)
		}
		_, argv, found := strings.Cut(out, "ARGV")
		if !found {
			t.Errorf("%s: tsc printed no argument list (%v):\n%s", mode.name, err, out)
			continue
		}
		argv, _, _ = strings.Cut(argv, "\n")
		requireArgsRoundTrip(t, mode.name, want, []byte(strings.TrimSpace(argv)))
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("%s: an argument ran a command of its own, which wrote %s", mode.name, marker)
			os.Remove(marker)
		}
	}
}
