package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The Windows shims are nvx itself, under the wrapped command's name.
//
// They used to be npm.cmd / npm.ps1 / an extensionless sh script, each of which
// costs a shell process to run the one line that starts nvx.exe: measured
// 2026-09-02, `npm run dev` under the .cmd shims was seven processes deep for a
// script that runs `node -e`, two of them cmd.exe instances that existed only to
// reach nvx. A hard link named npm.exe is found by cmd.exe, PowerShell and Git
// Bash alike, and it IS nvx, so nothing sits between the shell and the shim.
func TestWindowsShimsAreNvxUnderTheCommandsName(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("exe shims are a Windows-only layout; POSIX shims stay shell scripts")
	}
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// What an older nvx left behind. Every one of these has to go: bash picks a
	// bare `npm` over npm.exe, and a PATHEXT that lists .CMD first picks npm.cmd.
	for _, stale := range []string{"npm.cmd", "npm.ps1", "npm", "node.cmd", "node"} {
		if err := os.WriteFile(filepath.Join(shimDir, stale), []byte("@echo off\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := generateShims(nvxHome); err != nil {
		t.Fatalf("generateShims: %v", err)
	}

	nvxExe, err := os.Stat(filepath.Join(shimDir, "nvx.exe"))
	if err != nil {
		t.Fatalf("no nvx.exe beside the shims: %v", err)
	}
	for _, cmd := range allShimCommands() {
		shim, err := os.Stat(filepath.Join(shimDir, cmd+".exe"))
		if err != nil {
			t.Errorf("no %s.exe shim: %v", cmd, err)
			continue
		}
		if !os.SameFile(shim, nvxExe) {
			t.Errorf("%s.exe is not a hard link to nvx.exe (a copy would be %d bytes per command and go stale on upgrade)", cmd, shim.Size())
		}
	}
	for _, stale := range []string{"npm.cmd", "npm.ps1", "npm", "node.cmd", "node"} {
		if _, err := os.Stat(filepath.Join(shimDir, stale)); err == nil {
			t.Errorf("stale shim %s survived regeneration; a shell that prefers it would still run through it", stale)
		}
	}
}

// A hard link points at a file, not a name. installNvxCopy replaces nvx.exe by
// renaming a fresh copy over it, which leaves every link on the OLD file -- so
// after an upgrade `npm` would silently keep running the previous nvx forever.
// Regeneration has to notice and relink.
func TestExeShimsFollowAReplacedNvx(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("exe shims are a Windows-only layout")
	}
	nvxHome := tempDir(t)
	if err := generateShims(nvxHome); err != nil {
		t.Fatalf("generateShims: %v", err)
	}
	shimDir := filepath.Join(nvxHome, "bin")
	target := filepath.Join(shimDir, "nvx.exe")

	// An upgrade, done the way installNvxCopy does it: rename a new file over the
	// old one. The links stay attached to the old data.
	fresh := target + ".upgrade"
	if err := os.WriteFile(fresh, []byte("a different build"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fresh, target); err != nil {
		t.Fatal(err)
	}
	if err := generateShims(nvxHome); err != nil {
		t.Fatalf("generateShims after upgrade: %v", err)
	}

	nvxExe, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	npm, err := os.Stat(filepath.Join(shimDir, "npm.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(npm, nvxExe) {
		t.Error("npm.exe still links to the nvx that was replaced; every `npm` would run the old build")
	}
}

// Started under a shim's name, nvx must behave as `nvx shim <name> ...` --
// and must NOT read its own flags out of the wrapped command's arguments. The
// .cmd shims passed `shim npm %*`, so a `--no-sandbox` typed after `npm` was
// npm's to receive; the exe shim sees it in os.Args[1] and has to keep it there.
func TestInvokedUnderAShimNameDispatchesAsThatShim(t *testing.T) {
	cases := []struct {
		exe  string
		args []string
		want []string
	}{
		{`C:\Users\x\.nvx\bin\npm.exe`, []string{"npm.exe", "--no-sandbox", "install", "left-pad"},
			[]string{"npm.exe", "shim", "npm", "--no-sandbox", "install", "left-pad"}},
		{`C:\Users\x\.nvx\bin\NPX.EXE`, []string{"NPX.EXE", "cowsay"}, []string{"NPX.EXE", "shim", "npx", "cowsay"}},
		{`/home/x/.nvx/bin/node`, []string{"node", "-e", "1"}, []string{"node", "shim", "node", "-e", "1"}},
		{`C:\Users\x\.nvx\bin\nvx.exe`, []string{"nvx.exe", "--no-sandbox", "npm", "install"},
			[]string{"nvx.exe", "--no-sandbox", "npm", "install"}},
		{`/usr/local/bin/nvx`, []string{"nvx", "doctor"}, []string{"nvx", "doctor"}},
		// The staged sandbox supervisor is a copy of nvx under a per-build name.
		{`C:\x\.nvx\sandbox-exec\supervisor\nvx-10485760-1725000000.exe`, []string{"nvx-10485760-1725000000.exe", "supervisor-exec"},
			[]string{"nvx-10485760-1725000000.exe", "supervisor-exec"}},
	}
	for _, tc := range cases {
		got := shimInvocationArgs(tc.exe, tc.args)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("shimInvocationArgs(%q, %q) = %q, want %q", tc.exe, tc.args, got, tc.want)
		}
	}

	// And the startup flag parser, given the rewritten arguments, leaves npm's
	// --no-sandbox alone -- exactly as it did for `nvx shim npm --no-sandbox`.
	rewritten := shimInvocationArgs(`C:\x\bin\npm.exe`, []string{"npm.exe", "--no-sandbox", "install"})
	filtered, _, noSandbox, _, _ := parseStartupFlags(rewritten)
	if noSandbox {
		t.Error("--no-sandbox typed after `npm` was read as nvx's own flag; the shim let it disable the sandbox")
	}
	if strings.Join(filtered, " ") != "npm.exe shim npm --no-sandbox install" {
		t.Errorf("the wrapped command lost an argument: %q", filtered)
	}
}

// Anything that embeds "the nvx binary" into a file or a message must name
// nvx.exe, not whichever shim name this process happens to be running under:
// a project-bin shim generated by `npm install` would otherwise invoke
// `npm.exe shim vite`, which the shim rewrite turns into `npm shim vite`.
func TestNvxBinaryForAShimIsTheSiblingNvx(t *testing.T) {
	dir := tempDir(t)
	nvx := filepath.Join(dir, nvxExecutableName())
	if err := os.WriteFile(nvx, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "npm")
	if runtime.GOOS == "windows" {
		shim += ".exe"
	}
	if got := nvxBinaryFor(shim); got != nvx {
		t.Errorf("nvxBinaryFor(%q) = %q, want the sibling %q", shim, got, nvx)
	}
	if got := nvxBinaryFor(nvx); got != nvx {
		t.Errorf("nvxBinaryFor(%q) = %q, want it unchanged", nvx, got)
	}
	// No sibling nvx: nothing better to offer than the path we were given.
	lone := filepath.Join(tempDir(t), "npm.exe")
	if got := nvxBinaryFor(lone); got != lone {
		t.Errorf("nvxBinaryFor(%q) = %q, want it unchanged when no nvx sits beside it", lone, got)
	}
}

// The real binary, hard-linked under a shim name, dispatches as that shim.
//
// Every unit test above can pass while main() still reads os.Args[1] as an nvx
// subcommand -- in which case `node --version` through the shim prints nvx's
// own version and exits 0, which is exactly what an old nvx does when renamed.
func TestRealBinaryUnderAShimNameRunsTheShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("exe shims are a Windows-only layout")
	}
	if testing.Short() {
		t.Skip("builds the nvx binary; skipped under -short")
	}
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nvxExe := filepath.Join(shimDir, "nvx.exe")
	build := exec.Command("go", "build", "-o", nvxExe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build nvx for this test: %v\n%s", err, out)
	}
	nodeShim := filepath.Join(shimDir, "node.exe")
	if err := os.Link(nvxExe, nodeShim); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(nodeShim, "--version")
	// Only the shim dir on PATH: there is no runtime to find, so the shim must
	// fail to run node -- which is still proof that it went looking for node.
	cmd.Env = append(envWithout(os.Environ(), "PATH", "NVX_HOME"), "PATH="+shimDir, "NVX_HOME="+nvxHome, "NVX_QUIET=1")
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "nvx version") {
		t.Fatalf("node.exe answered as nvx itself:\n%s", out)
	}
	if err == nil {
		t.Fatalf("expected the shim to fail to find node with only the shim dir on PATH; output:\n%s", out)
	}
	if !strings.Contains(string(out), "node") {
		t.Errorf("the failure does not mention node, so it is not the shim path failing:\n%s", out)
	}
}

func envWithout(env []string, keys ...string) []string {
	var out []string
	for _, e := range env {
		drop := false
		for _, k := range keys {
			if strings.HasPrefix(strings.ToUpper(e), strings.ToUpper(k)+"=") {
				drop = true
			}
		}
		if !drop {
			out = append(out, e)
		}
	}
	return out
}
