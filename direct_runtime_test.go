package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// npm.cmd is a batch file, and Windows runs a batch file by starting cmd.exe
// for it. The uncontained shim path handed npm.cmd to exec.Command, so every
// `npm run` carried a cmd.exe whose only job was to start node.exe with
// npm-cli.js. The sandboxed path already launches node.exe directly; the
// uncontained one now does the same.
func TestUncontainedNpmLaunchesNodeExeDirectly(t *testing.T) {
	versionDir := tempDir(t)
	nodeExe := filepath.Join(versionDir, "node.exe")
	writeStubBinary(t, nodeExe)
	npmCli := filepath.Join(versionDir, "node_modules", "npm", "bin", "npm-cli.js")
	writeStubBinary(t, npmCli)
	npxCli := filepath.Join(versionDir, "node_modules", "npm", "bin", "npx-cli.js")
	writeStubBinary(t, npxCli)
	npmCmd := filepath.Join(versionDir, "npm.cmd")
	writeStubBinary(t, npmCmd)
	npxCmd := filepath.Join(versionDir, "npx.cmd")
	writeStubBinary(t, npxCmd)

	gotPath, gotArgs := directLaunchCommand(npmCmd, "", []string{"run", "dev"})
	if gotPath != nodeExe {
		t.Errorf("npm.cmd launched as %q, want node.exe %q (a .cmd goes through cmd.exe)", gotPath, nodeExe)
	}
	// The CLI script first, then npm's arguments, and nothing else: the
	// preserve-symlinks flags the sandbox adds change module resolution and are
	// not wanted for a command that runs as the user.
	if want := []string{npmCli, "run", "dev"}; strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q, want %q", gotArgs, want)
	}

	gotPath, gotArgs = directLaunchCommand(npxCmd, "", []string{"cowsay"})
	if gotPath != nodeExe || len(gotArgs) != 2 || gotArgs[0] != npxCli {
		t.Errorf("npx.cmd launched as %q %q, want node.exe with npx-cli.js", gotPath, gotArgs)
	}

	// node.exe itself, and anything that is not npm/npx, is launched as resolved.
	if p, a := directLaunchCommand(nodeExe, "", []string{"-e", "1"}); p != nodeExe || strings.Join(a, " ") != "-e 1" {
		t.Errorf("node.exe was rewritten: %q %q", p, a)
	}
	yarn := filepath.Join(versionDir, "yarn.cmd")
	writeStubBinary(t, yarn)
	if p, _ := directLaunchCommand(yarn, "", nil); p != yarn {
		t.Errorf("yarn.cmd was rewritten to %q; only npm and npx have a known CLI script", p)
	}
}

// A self-updated npm lives in the version's npm_global prefix, which has the
// npm-cli.js but no node.exe of its own. That npm must still be the one that
// runs (the override is the point of npmGlobalOverridePath), with the active
// version's node.exe as the interpreter -- and when even that is unknown, the
// .cmd is left alone rather than guessed at.
func TestUncontainedNpmFromNpmGlobalKeepsItsOwnCli(t *testing.T) {
	versionDir := tempDir(t)
	nodeExe := filepath.Join(versionDir, "node.exe")
	writeStubBinary(t, nodeExe)
	globalDir := filepath.Join(versionDir, "npm_global")
	cli := filepath.Join(globalDir, "node_modules", "npm", "bin", "npm-cli.js")
	writeStubBinary(t, cli)
	npmCmd := filepath.Join(globalDir, "npm.cmd")
	writeStubBinary(t, npmCmd)

	gotPath, gotArgs := directLaunchCommand(npmCmd, nodeExe, []string{"-v"})
	if gotPath != nodeExe {
		t.Errorf("launched %q, want the active version's node.exe %q", gotPath, nodeExe)
	}
	if len(gotArgs) == 0 || gotArgs[0] != cli {
		t.Errorf("args %q do not start with the self-updated npm's own CLI %q", gotArgs, cli)
	}

	if p, _ := directLaunchCommand(npmCmd, "", []string{"-v"}); p != npmCmd {
		t.Errorf("with no node.exe to run it, npm.cmd was rewritten to %q instead of left for cmd.exe", p)
	}
}

// Inside an uncontained `npm run`, a script's own `node` resolved back through
// nvx's shim: cmd.exe -> nvx -> node, two processes for one. The child gets a
// PATH entry holding the runtime executable and nothing else, so `node` is the
// real node while `npm` still reaches the shim -- a nested `npm install` inside
// a script must stay intercepted, which ruled out putting the whole version
// directory (npm.cmd included) on PATH.
func TestDirectRuntimeDirHoldsOnlyTheRuntimeExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the direct runtime dir exists to bypass the Windows exe shims")
	}
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0")
	nodeExe := filepath.Join(versionDir, "node.exe")
	writeStubBinary(t, nodeExe)
	writeStubBinary(t, filepath.Join(versionDir, "npm.cmd"))
	writeStubBinary(t, filepath.Join(versionDir, "npx.cmd"))

	dir := directRuntimeDir(nvxHome, Providers["node"], "v22.0.0")
	if dir == "" {
		t.Fatal("no direct runtime dir for an installed version")
	}
	if strings.EqualFold(dir, versionDir) {
		t.Fatal("the direct dir IS the version dir; npm.cmd on it would unshim nested npm calls")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.EqualFold(entries[0].Name(), "node.exe") {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("direct dir holds %v, want exactly node.exe", names)
	}
	linked, err := os.Stat(filepath.Join(dir, "node.exe"))
	if err != nil {
		t.Fatal(err)
	}
	real, err := os.Stat(nodeExe)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(linked, real) {
		t.Error("direct node.exe is not the version's node.exe (hard link expected on one volume)")
	}

	// Stable across calls: a second run must not error or churn the link.
	if again := directRuntimeDir(nvxHome, Providers["node"], "v22.0.0"); again != dir {
		t.Errorf("second call returned %q, first %q", again, dir)
	}

	// A version that is not installed has no direct dir to offer.
	if got := directRuntimeDir(nvxHome, Providers["node"], "v99.0.0"); got != "" {
		t.Errorf("direct dir %q for a version that is not installed", got)
	}

	// bun: same rule, bun.exe only. bunx stays a shim.
	bunDir := filepath.Join(nvxHome, "versions", "bun", "v1.2.0")
	writeStubBinary(t, filepath.Join(bunDir, "bun.exe"))
	writeStubBinary(t, filepath.Join(bunDir, "bunx.exe"))
	dir = directRuntimeDir(nvxHome, Providers["bun"], "v1.2.0")
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no direct dir for bun: %v", err)
	}
	if len(entries) != 1 || !strings.EqualFold(entries[0].Name(), "bun.exe") {
		t.Errorf("bun direct dir holds %d entries, want bun.exe only", len(entries))
	}
}

// Reinstalling a version replaces its node.exe; a link to the old file would
// keep running it.
func TestDirectRuntimeDirFollowsAReinstalledRuntime(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the direct runtime dir exists to bypass the Windows exe shims")
	}
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0")
	nodeExe := filepath.Join(versionDir, "node.exe")
	writeStubBinary(t, nodeExe)
	dir := directRuntimeDir(nvxHome, Providers["node"], "v22.0.0")
	if dir == "" {
		t.Fatal("no direct dir")
	}

	// The replacement has the same size and the same modification time as the
	// file it replaces. A reinstall from the same archive looks exactly like
	// this, and so did two writes inside one timestamp tick on a CI runner --
	// which is where a version that took size-and-time as "already linked"
	// skipped the relink and failed.
	fresh := nodeExe + ".new"
	writeStubBinary(t, fresh)
	old, err := os.Stat(nodeExe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, old.ModTime(), old.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fresh, nodeExe); err != nil {
		t.Fatal(err)
	}
	if again := directRuntimeDir(nvxHome, Providers["node"], "v22.0.0"); again != dir {
		t.Fatalf("dir moved: %q", again)
	}
	linked, _ := os.Stat(filepath.Join(dir, "node.exe"))
	real, _ := os.Stat(nodeExe)
	if linked == nil || real == nil || !os.SameFile(linked, real) {
		t.Error("direct node.exe still points at the node.exe that was replaced")
	}
}

// A hard link keeps node.exe alive after its version directory is deleted, so
// `nvx uninstall` has to take the direct dir with it -- and only that one.
func TestPruneDirectRuntimeDirsFollowsUninstall(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the direct runtime dir exists to bypass the Windows exe shims")
	}
	nvxHome := tempDir(t)
	for _, v := range []string{"v20.0.0", "v22.0.0"} {
		writeStubBinary(t, filepath.Join(nvxHome, "versions", "node", v, "node.exe"))
		if directRuntimeDir(nvxHome, Providers["node"], v) == "" {
			t.Fatalf("no direct dir for %s", v)
		}
	}
	if err := os.RemoveAll(filepath.Join(nvxHome, "versions", "node", "v20.0.0")); err != nil {
		t.Fatal(err)
	}

	pruneDirectRuntimeDirs(nvxHome)

	if _, err := os.Stat(filepath.Join(nvxHome, "direct", "node", "v20.0.0")); err == nil {
		t.Error("the uninstalled version's direct dir survived; its node.exe is kept alive by the link")
	}
	if _, err := os.Stat(filepath.Join(nvxHome, "direct", "node", "v22.0.0", "node.exe")); err != nil {
		t.Errorf("the installed version's direct dir was pruned too: %v", err)
	}
}

// The nested nvx (the npm shim run from inside a script) sees the direct dir
// ahead of the shim dir on PATH. That is nvx's own doing and must not trip the
// "a runtime dir is ahead of nvx's shim dir" warning -- which a real raw
// runtime dir in the same position must still trip.
func TestShadowCheckIgnoresNvxsOwnDirectDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the direct runtime dir exists to bypass the Windows exe shims")
	}
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0")
	writeStubBinary(t, filepath.Join(versionDir, "node.exe"))
	direct := directRuntimeDir(nvxHome, Providers["node"], "v22.0.0")
	if direct == "" {
		t.Fatal("no direct dir")
	}

	sep := string(os.PathListSeparator)
	if pathIsShadowed(direct+sep+shimDir, nvxHome) {
		t.Error("nvx's own direct dir ahead of the shim dir was reported as shadowing it; every nested shim would warn")
	}
	// The control: the raw version dir in the same position is a real shadow.
	if !pathIsShadowed(versionDir+sep+shimDir, nvxHome) {
		t.Error("the raw version dir ahead of the shim dir was not reported; the check above proves nothing")
	}
}
