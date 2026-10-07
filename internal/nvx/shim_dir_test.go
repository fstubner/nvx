package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// inEmptyDir runs the rest of the test in a directory that declares no version,
// so a stray .nvmrc above the repository cannot decide what a shim resolves.
func inEmptyDir(t *testing.T) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tempDir(t)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// corepackDist makes the dist directory of a corepack inside an nvx-managed Node
// and returns what each script held, to compare against afterwards.
func corepackDist(t *testing.T, nvxHome string, names ...string) map[string]string {
	t.Helper()
	dist := filepath.Join(nvxHome, "versions", "node", "v22.0.0", "lib", "node_modules", "corepack", "dist")
	if err := os.MkdirAll(dist, 0o700); err != nil {
		t.Fatal(err)
	}
	held := map[string]string{}
	for _, name := range names {
		body := "#!/usr/bin/env node\nrequire('./lib/corepack.cjs').runMain(['" + name + "'])\n"
		if err := os.WriteFile(filepath.Join(dist, name+".js"), []byte(body), 0o700); err != nil { // #nosec G306 -- fixture
			t.Fatal(err)
		}
		held[name] = body
	}
	return held
}

// corepackLeftovers puts in the shim directory what `corepack enable` left there
// when it ran with nvx's shim directory first on PATH. On POSIX that is a link
// per name into its dist directory, and on Windows the sh, cmd and PowerShell
// launchers corepack's cmd-shim writes.
func corepackLeftovers(t *testing.T, nvxHome string, names ...string) {
	t.Helper()
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if runtime.GOOS != "windows" {
			target := filepath.Join("..", "versions", "node", "v22.0.0", "lib", "node_modules", "corepack", "dist", name+".js")
			// Corepack unlinks what is there and links its own.
			_ = os.Remove(filepath.Join(shimDir, name))
			if err := os.Symlink(target, filepath.Join(shimDir, name)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		script := `"%dp0%\..\versions\node\v22.0.0\node_modules\corepack\dist\` + name + `.js" %*` + "\r\n"
		for _, ext := range []string{"", ".CMD", ".ps1"} {
			if err := os.WriteFile(filepath.Join(shimDir, name+ext), []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A link at the shim's path is replaced and what it pointed at is left alone.
//
// os.WriteFile follows a link. `corepack enable` had replaced nvx's yarn and pnpm
// shims with links to its own dist/yarn.js and dist/pnpm.js, and the next `nvx
// env` wrote the shim text through them. Corepack's scripts, inside the Node
// install, came out 57 bytes long where they had been 186. Measured 2026-10-07 on
// Linux.
func TestWritingAShimReplacesALinkAndLeavesItsTarget(t *testing.T) {
	dir := tempDir(t)
	victim := filepath.Join(dir, "yarn.js")
	original := "#!/usr/bin/env node\nrequire('./lib/corepack.cjs').runMain(['yarn'])\n"
	if err := os.WriteFile(victim, []byte(original), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "yarn")
	if err := os.Symlink("yarn.js", shim); err != nil {
		t.Skipf("creating symlinks on Windows needs privilege or Developer Mode: %v", err)
	}

	if err := writeExecutableFile(shim, []byte("#!/bin/sh\nexit 0\n")); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(victim); string(got) != original {
		t.Errorf("the file the link pointed at was written through, and now holds %q", got)
	}
	info, err := os.Lstat(shim)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("the shim is still %v, not a file nvx wrote", info.Mode())
	}
}

// Doctor reads the shim directory and does not touch it.
func TestInspectingTheShimDirectoryNamesWhatIsNotNvxs(t *testing.T) {
	nvxHome := tempDir(t)
	if err := generateShims(nvxHome); err != nil {
		t.Fatal(err)
	}
	if rep := inspectShimDir(nvxHome); !rep.healthy() {
		t.Fatalf("freshly written shims are reported unhealthy: %+v", rep)
	}

	if runtime.GOOS != "windows" {
		corepackDist(t, nvxHome, "yarn", "pnpx")
	}
	corepackLeftovers(t, nvxHome, "yarn", "pnpx")
	rep := inspectShimDir(nvxHome)

	found := map[string]foreignShim{}
	for _, f := range rep.foreign {
		found[f.name] = f
	}
	yarn := "yarn"
	pnpx := "pnpx"
	if runtime.GOOS == "windows" {
		yarn, pnpx = "yarn.CMD", "pnpx.ps1"
	}
	if f, ok := found[yarn]; !ok || !f.corepack || !f.atShimName {
		t.Errorf("%s replaced an nvx shim and is corepack's, but was reported as %+v", yarn, f)
	}
	if f, ok := found[pnpx]; !ok || !f.corepack || f.atShimName {
		t.Errorf("%s is one of corepack's extra names, but was reported as %+v", pnpx, f)
	}
	if rep.healthy() {
		t.Error("the report calls a shim directory with corepack's links in it healthy")
	}
	if _, err := os.Lstat(filepath.Join(nvxHome, "bin", yarn)); err != nil {
		t.Errorf("inspecting removed %s; doctor must not change anything: %v", yarn, err)
	}

	text := formatDoctorReport(doctorReport{shimDir: filepath.Join(nvxHome, "bin"), shimDirOnPath: true, shimFiles: rep})
	for _, want := range []string{"[FAIL]", yarn, "nvx init-shims"} {
		if !strings.Contains(text, want) {
			t.Errorf("doctor's report does not contain %q:\n%s", want, text)
		}
	}
}

// Shims from an older nvx are reported, not passed as healthy.
//
// On Windows an upgrade moves a new nvx.exe over the old one and every shim, being
// a hard link, stays on the old file. `nvx doctor` printed "intercepting commands
// correctly" meanwhile. Measured 2026-10-07, the shims were 12,467,712 bytes beside
// an nvx.exe of 12,492,288. On POSIX the case is a shim script naming an nvx that
// is gone.
func TestDoctorReportsShimsThatRunAnOlderNvx(t *testing.T) {
	nvxHome := tempDir(t)
	if err := generateShims(nvxHome); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(nvxHome, "bin")
	if runtime.GOOS == "windows" {
		fresh := filepath.Join(shimDir, "nvx.exe.upgrade")
		if err := os.WriteFile(fresh, []byte("a newer build"), 0o700); err != nil { // #nosec G306 -- fixture
			t.Fatal(err)
		}
		if err := os.Rename(fresh, filepath.Join(shimDir, "nvx.exe")); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(filepath.Join(shimDir, "node"), []byte(shimScript(filepath.Join(nvxHome, "gone", "nvx"), "node")), 0o700); err != nil { // #nosec G306 -- fixture
			t.Fatal(err)
		}
	}

	rep := inspectShimDir(nvxHome)
	if len(rep.stale) == 0 {
		t.Fatalf("shims running an older nvx were not noticed: %+v", rep)
	}
	if text := formatDoctorReport(doctorReport{shimDir: shimDir, shimDirOnPath: true, shimFiles: rep}); !strings.Contains(text, "older nvx") || !strings.Contains(text, "nvx init-shims") {
		t.Errorf("the report does not say what is stale or how to fix it:\n%s", text)
	}

	if err := generateShims(nvxHome); err != nil {
		t.Fatal(err)
	}
	if rep := inspectShimDir(nvxHome); !rep.healthy() {
		t.Errorf("init-shims left the shim directory unhealthy: %+v", rep)
	}
}

// Doctor's verdict, and not only its text. A damaged shim directory is not healthy.
func TestDoctorIsUnhealthyWithCorepacksLinksInTheShimDir(t *testing.T) {
	home := t.TempDir()
	seedDoctorShims(t, home)
	restore := reportSandboxLaunchFn
	t.Cleanup(func() { reportSandboxLaunchFn = restore })
	reportSandboxLaunchFn = func(string) bool { return true }

	if code := runDoctorQuietly(t, home); code != 0 {
		t.Skipf("no healthy baseline in this environment (runDoctor = %d)", code)
	}
	if runtime.GOOS != "windows" {
		corepackDist(t, home, "yarn", "pnpm")
	}
	corepackLeftovers(t, home, "yarn", "pnpm")

	if code := runDoctorQuietly(t, home); code == 0 {
		t.Fatal("corepack's links replace nvx's yarn and pnpm shims and doctor still said nvx is intercepting commands correctly")
	}
}

// --fix is the repair, and it says what it did.
func TestDoctorFixRepairsCorepacksLinksInTheShimDir(t *testing.T) {
	stubMachineWideWrites(t)
	home := t.TempDir()
	seedDoctorShims(t, home)
	restore := reportSandboxLaunchFn
	t.Cleanup(func() { reportSandboxLaunchFn = restore })
	reportSandboxLaunchFn = func(string) bool { return true }
	if code := runDoctorQuietly(t, home); code != 0 {
		t.Skipf("no healthy baseline in this environment (runDoctor = %d)", code)
	}
	if runtime.GOOS != "windows" {
		corepackDist(t, home, "yarn", "pnpm")
	}
	corepackLeftovers(t, home, "yarn", "pnpm")

	var code int
	_ = captureStdout(t, func() {
		code = runDoctor(home, true)
	})
	if code != 0 {
		t.Errorf("doctor --fix exited %d after repairing the shim directory", code)
	}
	if rep := inspectShimDir(home); !rep.healthy() {
		t.Errorf("the shim directory is still damaged after doctor --fix: %+v", rep)
	}
}

// `corepack enable` run through the shim is pointed at the Node it belongs to.
func TestCorepackEnableIsPointedAtItsOwnNodesDirectory(t *testing.T) {
	inEmptyDir(t)
	t.Setenv("PATH", "")
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0")
	corepackBin := filepath.Join(GetVersionBinDir(versionDir), "corepack")
	if runtime.GOOS == "windows" {
		corepackBin = filepath.Join(versionDir, "corepack.cmd")
	}
	writeStubBinary(t, corepackBin)
	if err := CreateLink(runtimeCurrentLinkPath(nvxHome, "node"), versionDir); err != nil {
		t.Fatal(err)
	}
	want := filepath.Dir(corepackBin)

	for _, verb := range []string{"enable", "disable"} {
		got := corepackInstallDirArgs([]string{verb, "yarn"}, nvxHome)
		wantArgs := []string{verb, "--install-directory", want, "yarn"}
		if strings.Join(got, "\x00") != strings.Join(wantArgs, "\x00") {
			t.Errorf("corepack %s yarn became %q, want %q", verb, got, wantArgs)
		}
	}

	// What the user chose, and everything that is not enable or disable, is theirs.
	for _, args := range [][]string{
		{"enable", "--install-directory", "/somewhere"},
		{"enable", "--install-directory=/somewhere"},
		{"yarn", "install"},
		{"prepare", "yarn@4", "--activate"},
		{},
	} {
		if got := corepackInstallDirArgs(args, nvxHome); strings.Join(got, "\x00") != strings.Join(args, "\x00") {
			t.Errorf("%q was rewritten to %q", args, got)
		}
	}
}

// With no Node of nvx's own and no corepack anywhere, there is nothing to point at.
func TestCorepackEnableIsLeftAloneWhenThereIsNoCorepack(t *testing.T) {
	inEmptyDir(t)
	t.Setenv("PATH", "")
	args := []string{"enable"}
	if got := corepackInstallDirArgs(args, tempDir(t)); strings.Join(got, " ") != "enable" {
		t.Errorf("enable became %q with no corepack to find", got)
	}
}

// yarn and pnpm are found where corepack was told to put them, so the shims can
// contain them without PATH having to lead there.
func TestYarnAndPnpmResolveToTheLinksCorepackMade(t *testing.T) {
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0")
	provider := NodeProvider{}
	for _, cmd := range []string{"yarn", "pnpm"} {
		if got := provider.ResolveBinary(cmd, nvxHome, "v22.0.0"); got != "" {
			t.Errorf("%s resolved to %q before corepack linked it", cmd, got)
		}
		link := filepath.Join(GetVersionBinDir(versionDir), cmd)
		if runtime.GOOS == "windows" {
			link = filepath.Join(versionDir, cmd+".cmd")
		}
		writeStubBinary(t, link)
		if got := provider.ResolveBinary(cmd, nvxHome, "v22.0.0"); got != link {
			t.Errorf("%s resolved to %q, want the link %q", cmd, got, link)
		}
	}
}

// A home that got its nvx binary some other way has no shims, and the commands
// that set a version up are the first thing anyone runs.
func TestInstallUseAndDefaultWriteMissingShims(t *testing.T) {
	notQuiet(t)
	for name, run := range map[string]func(t *testing.T, nvxHome string){
		"install": func(t *testing.T, nvxHome string) { installWith(t, nvxHome, "v22.1.0") },
		"use": func(t *testing.T, nvxHome string) {
			installWithoutShims(t, nvxHome)
			_ = captureStdout(t, func() { _ = captureStderrHere(t, func() { runUse("22", nvxHome, "bash", true) }) })
		},
		"default": func(t *testing.T, nvxHome string) {
			installWithoutShims(t, nvxHome)
			_ = captureStderrHere(t, func() { runDefault("22", nvxHome) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			nvxHome := tempDir(t)
			run(t, nvxHome)
			if rep := inspectShimDir(nvxHome); !rep.healthy() {
				t.Errorf("nvx %s left the shims missing or wrong: %+v", name, rep)
			}
		})
	}
}

// installWithoutShims puts a stub Node 22.1.0 on disk and nothing else.
func installWithoutShims(t *testing.T, nvxHome string) {
	t.Helper()
	bin := nodeBinaryPath(filepath.Join(nvxHome, "versions", "node", "v22.1.0"))
	writeStubBinary(t, bin)
}

// ensureShims says so when it writes them, and is silent when it has nothing to do.
func TestEnsureShimsIsSilentWhenTheShimsAreThere(t *testing.T) {
	notQuiet(t)
	nvxHome := tempDir(t)
	first := captureStderrHere(t, func() { ensureShims(nvxHome) })
	if !strings.Contains(first, "Wrote nvx's shims") {
		t.Errorf("writing the shims was not mentioned:\n%s", first)
	}
	if second := captureStderrHere(t, func() { ensureShims(nvxHome) }); second != "" {
		t.Errorf("a second call, with the shims in place, printed:\n%s", second)
	}
}

// A command that resolves ahead of the shims is not "[OK] ... with no raw-runtime
// dir ahead of it", and the report says what to do about it.
//
// Doctor printed that line and, under it, "[FAIL] node -> ...\fakenode\node.exe
// (bypasses nvx)" with no word on a remedy. Measured 2026-10-07 with a copy of
// node.exe ahead of the shim directory.
func TestDoctorSaysWhatToDoAboutACommandAheadOfTheShims(t *testing.T) {
	shimDir := filepath.FromSlash("/home/u/.nvx/bin")
	out := formatDoctorReport(doctorReport{
		shimDir: shimDir, shimDirOnPath: true, shimDirIndex: 1,
		commands: []commandResolution{
			{name: "node", resolved: filepath.FromSlash("/opt/fakenode/node"), viaShim: false},
			{name: "npm", resolved: filepath.Join(shimDir, "npm"), viaShim: true},
		},
	})
	if strings.Contains(out, "[OK]   shim dir is on PATH") {
		t.Errorf("a shim dir with a bypassing command ahead of it is reported OK:\n%s", out)
	}
	for _, want := range []string{"[FAIL] shim dir is on PATH at position 1", filepath.FromSlash("/opt/fakenode/node"), "Put nvx's shim dir before"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "- npm ->") {
		t.Errorf("a command that does go through the shim is listed as bypassing:\n%s", out)
	}
}

// And the run offers the one-line fix for this shell, as it does for the rest.
func TestDoctorOffersTheShellFixWhenACommandBypassesTheShims(t *testing.T) {
	notQuiet(t)
	home := t.TempDir()
	seedDoctorShims(t, home)
	restore := reportSandboxLaunchFn
	t.Cleanup(func() { reportSandboxLaunchFn = restore })
	reportSandboxLaunchFn = func(string) bool { return true }
	if code := runDoctorQuietly(t, home); code != 0 {
		t.Skipf("no healthy baseline in this environment (runDoctor = %d)", code)
	}
	system := t.TempDir()
	name := "npm"
	if runtime.GOOS == "windows" {
		name = "npm.exe"
	}
	if err := os.WriteFile(filepath.Join(system, name), []byte("real npm"), 0o755); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", system+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout string
	stderr := captureStderrHere(t, func() {
		stdout = captureStdout(t, func() { runDoctor(home, false) })
	})
	if !strings.Contains(stdout, "[FAIL] shim dir is on PATH") || !strings.Contains(stderr, "To fix the current shell now") {
		t.Errorf("no remedy for a bypassing npm.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// The wiring, end to end. A run of the corepack shim reaches corepack with the
// directory added, and a stand-in corepack prints the arguments it was started with.
func TestTheCorepackShimStartsCorepackWithTheInstallDirectory(t *testing.T) {
	inEmptyDir(t)
	t.Setenv("PATH", "")
	unsetEnv(t, nvxActiveEnvVar)
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0")
	stub := filepath.Join(GetVersionBinDir(versionDir), "corepack")
	body := "#!/bin/sh\necho \"ARGS: $@\"\n"
	if runtime.GOOS == "windows" {
		// Each argument as the batch file reads it, without the quotes nvx puts
		// around one that holds anything but letters, digits and #$*+-./:?@\_,
		// such as the ~ of an 8.3 name.
		stub = filepath.Join(versionDir, "corepack.cmd")
		body = "@echo ARGS: %~1 %~2 %~3 %~4\r\n"
	}
	if err := os.MkdirAll(filepath.Dir(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub, []byte(body), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	if err := CreateLink(runtimeCurrentLinkPath(nvxHome, "node"), versionDir); err != nil {
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() {
		_ = captureStderrHere(t, func() { code = runShim("corepack", []string{"enable", "yarn"}, nvxHome) })
	})
	if code != 0 {
		t.Fatalf("the stand-in corepack exited %d:\n%s", code, out)
	}
	want := "ARGS: enable --install-directory " + filepath.Dir(stub) + " yarn"
	if !strings.Contains(out, want) {
		t.Errorf("corepack was started with %q, want it to contain %q", strings.TrimSpace(out), want)
	}
}

// The directory handed to corepack is the one beside the corepack that runs.
//
// A project can carry its own (a devDependency), and the shim runs that one when
// the active Node.js has none. Handing corepack nothing in that case left its own
// `which corepack` to find the shim directory, which is the original takeover.
func TestCorepackEnableFollowsTheProjectsOwnCorepack(t *testing.T) {
	inEmptyDir(t)
	t.Setenv("PATH", "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("package.json", []byte(`{"name":"p"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "corepack"
	if runtime.GOOS == "windows" {
		name = "corepack.cmd"
	}
	bin := filepath.Join(cwd, "node_modules", ".bin")
	writeStubBinary(t, filepath.Join(bin, name))

	// No Node.js of nvx's own, so the project's corepack is the one that runs.
	got := corepackInstallDirArgs([]string{"enable"}, tempDir(t))
	want := []string{"enable", "--install-directory", bin}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("corepack enable became %q, want %q", got, want)
	}
}

// Only the names corepack uses are taken out. A link or launcher with another name
// that happens to point into corepack is the person's own.
func TestACorepackLinkWithAnotherNameIsLeftAlone(t *testing.T) {
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(shimDir, "pnpm9")
	if runtime.GOOS == "windows" {
		own += ".cmd"
		script := `"%dp0%\..\versions\node\v22.0.0\node_modules\corepack\dist\pnpm.js" %*` + "\r\n"
		if err := os.WriteFile(own, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Symlink(filepath.Join("..", "versions", "node", "v22.0.0", "lib", "node_modules", "corepack", "dist", "pnpm.js"), own); err != nil {
		t.Fatal(err)
	}

	if err := generateShims(nvxHome); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(own); err != nil {
		t.Errorf("%s, which is not one of corepack's names, was removed: %v", filepath.Base(own), err)
	}
	for _, f := range inspectShimDir(nvxHome).foreign {
		t.Errorf("%s was reported as foreign", f.name)
	}
}

// A corepack, yarn or pnpm installed with `npm install -g` is found without
// PATH leading to it. Node.js releases without a bundled corepack get theirs that
// way, and the hint says so, so the shim has to find it in a plain shell.
func TestGloballyInstalledCorepackYarnAndPnpmAreFound(t *testing.T) {
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v22.0.0")
	provider := NodeProvider{}
	for _, cmd := range []string{"corepack", "yarn", "pnpm"} {
		bundled := filepath.Join(GetVersionBinDir(versionDir), cmd)
		if runtime.GOOS == "windows" {
			bundled = filepath.Join(versionDir, cmd+".cmd")
		}
		writeStubBinary(t, bundled)
		if got := provider.ResolveBinary(cmd, nvxHome, "v22.0.0"); got != bundled {
			t.Fatalf("%s resolved to %q before a global install, want %q", cmd, got, bundled)
		}

		global := filepath.Join(GetNpmPrefixBinDir(filepath.Join(versionDir, "npm_global")), npmBinName(cmd))
		writeStubBinary(t, global)
		if got := provider.ResolveBinary(cmd, nvxHome, "v22.0.0"); got != global {
			t.Errorf("%s resolved to %q after a global install, want %q", cmd, got, global)
		}
	}
}

// A launcher corepack wrote starts node on its script, as npm.cmd does, and one
// that came from `npm install -g` is left to start itself.
//
// A .cmd started from Go goes through cmd.exe, which reads `^`, `&` and `>` in its
// arguments as its own. Measured 2026-10-07 on Windows 11 with Go 1.26.6 and a
// .cmd that echoes its arguments, `add react@^18.2.0` arrived as `add
// react@18.2.0`, `add foo@>=1` was read as a redirect and wrote a file named 1,
// and `add a&b` ran b as a second command. Inside the sandbox a .cmd cannot be
// started at all (`Access is denied.`), which stopped a contained
// corepack-managed pnpm on Windows.
func TestCorepackLaunchersStartNodeOnTheirScript(t *testing.T) {
	dir := tempDir(t)
	for _, name := range []string{"node.exe", filepath.Join("node_modules", "corepack", "dist", "pnpm.js"), filepath.Join("node_modules", "corepack", "dist", "yarn.js")} {
		writeStubBinary(t, filepath.Join(dir, name))
	}
	corepackLauncher := `@IF EXIST "%~dp0\node.exe" (` + "\r\n" + `  "%~dp0\node.exe"  "%~dp0\node_modules\corepack\dist\pnpm.js" %*` + "\r\n" + `)` + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, "pnpm.CMD"), []byte(corepackLauncher), 0o600); err != nil {
		t.Fatal(err)
	}
	node, script, ok := windowsNpmCliLaunch(filepath.Join(dir, "pnpm.CMD"), "")
	if !ok || node != filepath.Join(dir, "node.exe") || script != filepath.Join(dir, "node_modules", "corepack", "dist", "pnpm.js") {
		t.Errorf("corepack's pnpm.CMD resolved to %q, %q, %v", node, script, ok)
	}

	// The same name, written by npm for a global install of pnpm itself.
	npmGlobal := `@ECHO off` + "\r\n" + `"%dp0%\node.exe" "%dp0%\node_modules\pnpm\bin\pnpm.cjs" %*` + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, "yarn.cmd"), []byte(npmGlobal), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := windowsNpmCliLaunch(filepath.Join(dir, "yarn.cmd"), ""); ok {
		t.Error("a yarn.cmd that does not name corepack's script was rewritten to start corepack's")
	}
	// And one that names the script when the script is not there.
	if err := os.Remove(filepath.Join(dir, "node_modules", "corepack", "dist", "pnpm.js")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := windowsNpmCliLaunch(filepath.Join(dir, "pnpm.CMD"), ""); ok {
		t.Error("a launcher whose script is missing was rewritten")
	}
}

// Every form of nvx shim is recognised as nvx's, including the one the first
// releases wrote with the command unquoted, and nothing else is.
func TestShimScriptTargetReadsEveryFormNvxWrote(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		cmd     string
		wantExe string
		wantOK  bool
	}{
		{"current", shimScript("/home/u/.nvx/bin/nvx", "node"), "node", "/home/u/.nvx/bin/nvx", true},
		{"a path with a quote", shimScript("/home/o'neil/nvx", "node"), "node", "/home/o'neil/nvx", true},
		{"a path with a space", shimScript("/home/a b/nvx", "npm"), "npm", "/home/a b/nvx", true},
		{"the first releases", "#!/bin/sh\nexec '/home/u/.nvx/bin/nvx' shim node \"$@\"\n", "node", "/home/u/.nvx/bin/nvx", true},
		{"another command's shim", shimScript("/x/nvx", "npm"), "node", "", false},
		{"corepack's launcher", "#!/usr/bin/env node\nrequire('./lib/corepack.cjs').runMain(['yarn'])\n", "yarn", "", false},
		{"nothing", "", "node", "", false},
		{"an unquoted path", "#!/bin/sh\nexec /x/nvx shim 'node' \"$@\"\n", "node", "", false},
	} {
		exe, ok := shimScriptTarget(tc.content, tc.cmd)
		if ok != tc.wantOK || exe != tc.wantExe {
			t.Errorf("%s: shimScriptTarget = %q, %v; want %q, %v", tc.name, exe, ok, tc.wantExe, tc.wantOK)
		}
	}
}

// A missing yarn, pnpm or corepack shim is named on every platform. On Windows
// only the commands people run directly were checked, so those three could go
// missing and doctor said nothing.
func TestDoctorNamesAMissingYarnShim(t *testing.T) {
	nvxHome := tempDir(t)
	if err := generateShims(nvxHome); err != nil {
		t.Fatal(err)
	}
	name := "yarn"
	if runtime.GOOS == "windows" {
		name = "yarn.exe"
	}
	if err := os.Remove(filepath.Join(nvxHome, "bin", name)); err != nil {
		t.Fatal(err)
	}

	rep := diagnosePath(filepath.Join(nvxHome, "bin"), nvxHome, coreShimCommands())
	rep.shimFiles = inspectShimDir(nvxHome)
	if !rep.shimFilesBroken() {
		t.Fatal("a shim directory with no yarn shim is reported healthy")
	}
	out := formatDoctorReport(rep)
	if !strings.Contains(out, "no shim for: yarn") || !strings.Contains(out, "nvx init-shims") {
		t.Errorf("the report does not name the missing shim or the fix:\n%s", out)
	}
}
