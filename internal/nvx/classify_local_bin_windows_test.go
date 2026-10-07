//go:build windows

package nvx

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realNpxHome is an NVX_HOME whose default node is a copy of the machine's real
// one, with that install's npm, and with isolation left on.
//
// realNodeHome turns isolation off, which is no use here: the question is
// whether a run that would be contained is not. npm is linked in rather than
// copied, so this does not hold a second copy of it.
func realNpxHome(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("node", "-p", "process.execPath + '|' + process.version").Output()
	if err != nil {
		t.Skipf("needs a real node on PATH: %v", err)
	}
	execPath, version, ok := strings.Cut(strings.TrimSpace(string(out)), "|")
	if !ok {
		t.Skipf("unexpected node output %q", out)
	}
	realDir := filepath.Dir(execPath)
	npmDir := filepath.Join(realDir, "node_modules")
	if !regularFileExists(filepath.Join(realDir, "npx.cmd")) || !regularFileExists(filepath.Join(npmDir, "npm", "bin", "npx-cli.js")) {
		t.Skipf("needs npm beside node.exe in %s", realDir)
	}

	home := tempDir(t)
	dir := filepath.Join(home, "versions", "node", version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node.exe", "npx.cmd"} {
		src, dst := filepath.Join(realDir, name), filepath.Join(dir, name)
		// A link where there is one: a fresh copy of node.exe is scanned again by
		// the antivirus the first time it runs, which took seconds.
		if os.Link(src, dst) == nil {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o700); err != nil { // #nosec G306 -- fixture
			t.Fatal(err)
		}
	}
	if err := CreateLink(filepath.Join(dir, "node_modules"), npmDir); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(currentLinkPath(home), dir); err != nil {
		t.Fatal(err)
	}
	return home
}

// A bin as npm writes it on Windows: the script, the .cmd that cmd.exe runs, and
// the .ps1.
const (
	npmBinScript = "#!/bin/sh\nexec node \"$(dirname \"$0\")/../hello-pkg/cli.js\" \"$@\"\n"
	npmBinCmd    = "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n" +
		"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\n" +
		"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\..\\hello-pkg\\cli.js\" %*\r\n"
	// What the tool leaves behind: its arguments, and the marker a sandbox sets.
	helloCLI = `const fs = require('fs');
fs.writeFileSync(process.argv[2], JSON.stringify({
  args: process.argv.slice(3),
  sandbox: process.env.NVX_SANDBOX || '',
}));
`
)

// `npx <a tool that is already in node_modules/.bin>` ran inside the sandbox
// and asked the install-script question, though nothing is fetched. On Windows
// that is where tsx, vitest, vite and next fail. It is now your own code, as it
// is through `npm run`.
//
// This runs the real npx on a small bin npm laid out in a project, through the
// path a shim takes. It tests the decision and the uncontained launch of npx and
// its .cmd bin, and not any one tool. The registry is a port nothing listens on,
// so a fetch would fail the run, and every lookup the checks make is counted.
func TestNpxOfAProjectToolRunsOutsideTheSandboxOnWindows(t *testing.T) {
	home := realNpxHome(t)

	lookups := 0
	orig := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
		lookups++
		// An install script, which is what the question was asked about.
		return "1.0.0", time.Now().Add(-30 * 24 * time.Hour), true, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = orig })
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")
	prevYes, prevAgent := yesFlag, agentModeFlag
	yesFlag, agentModeFlag = false, false
	t.Cleanup(func() { yesFlag, agentModeFlag = prevYes, prevAgent })

	project := tempDir(t)
	writeTestFile(t, filepath.Join(project, "package.json"), `{"name":"fixture","private":true}`, 0o644)
	writeTestFile(t, filepath.Join(project, "node_modules", "hello-pkg", "package.json"), `{"name":"hello-pkg","version":"1.0.0","bin":{"hello":"cli.js"}}`, 0o644)
	writeTestFile(t, filepath.Join(project, "node_modules", "hello-pkg", "cli.js"), helloCLI, 0o644)
	bin := filepath.Join(project, "node_modules", ".bin")
	writeTestFile(t, filepath.Join(bin, "hello"), npmBinScript, 0o755)
	writeTestFile(t, filepath.Join(bin, "hello.cmd"), npmBinCmd, 0o644)
	writeTestFile(t, filepath.Join(bin, "hello.ps1"), "exit 0\r\n", 0o644)
	marker := filepath.Join(tempDir(t), "marker.json")
	inProjectDir(t, project)

	sys := os.Getenv("SystemRoot")
	t.Setenv("PATH", filepath.Join(sys, "System32"))
	t.Setenv(nvxActiveEnvVar, "")
	t.Setenv("npm_config_registry", "http://127.0.0.1:9/")
	t.Setenv("npm_config_cache", tempDir(t))
	t.Setenv("npm_config_update_notifier", "false")
	// Neither this machine's user .npmrc nor its npm_config_package may make npx
	// fetch.
	t.Setenv("npm_config_userconfig", filepath.Join(project, "no-user.npmrc"))
	t.Setenv("npm_config_package", "")

	args := []string{"hello", marker, "a", "b"}
	policy, err := LoadPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	// Stopped here, before anything starts: a contained run would build an
	// AppContainer and grant it access to this project's folders.
	if shouldSandbox("npx", args, policy, parseShimOptions(args)) {
		t.Fatalf("`npx hello` is run inside the sandbox. hello is in node_modules/.bin, so nothing is fetched, " +
			"and the same tool through `npm run` is not contained at the default level")
	}

	trace := &runTrace{nvxHome: home}
	if code := runShimTraced(trace, "npx", args, home); code != 0 {
		t.Fatalf("`npx hello` exited %d, want 0", code)
	}
	if trace.mode != runModeDirect {
		t.Errorf("`npx hello` was recorded as %q, want %q", trace.mode, runModeDirect)
	}
	if lookups != 0 {
		t.Errorf("%d registry lookups were made for a tool that is already installed", lookups)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the tool did not run: %v", err)
	}
	var got struct {
		Args    []string
		Sandbox string
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the tool wrote %q: %v", data, err)
	}
	if strings.Join(got.Args, " ") != "a b" || got.Sandbox != "" {
		t.Errorf("the tool ran with %+v, want the arguments `a b` and no sandbox marker", got)
	}
}

// A bun install on Windows leaves .exe and .bunx, which npx does not look for.
func TestNpxDoesNotRunWhatBunInstalledOnWindows(t *testing.T) {
	root := binProject(t, "")
	for _, ext := range []string{".exe", ".bunx"} {
		writeTestFile(t, filepath.Join(root, "node_modules", ".bin", "vitest"+ext), "x", 0o644)
	}
	checkClasses(t, []classRow{
		{"npx", "npx", []string{"vitest"}, classAdHocTool},
		{"bunx", "bunx", []string{"vitest"}, classYourCode},
	})
}
