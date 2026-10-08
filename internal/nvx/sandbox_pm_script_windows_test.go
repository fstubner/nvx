//go:build windows

package nvx

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A package manager's script that node is asked to run, outside what the sandbox
// can read, is refused with the way to run it. See unreadablePackageManagerScript.

func TestScriptPackageNamesTheFolderToAllow(t *testing.T) {
	cases := []struct{ script, name, dir string }{
		{`C:\g\node_modules\pnpm\bin\pnpm.cjs`, "pnpm", `C:\g\node_modules\pnpm`},
		{`C:\g\node_modules\@yarnpkg\cli-dist\bin\yarn.js`, "@yarnpkg/cli-dist", `C:\g\node_modules\@yarnpkg\cli-dist`},
		{`C:\g\node_modules\a\node_modules\pnpm\bin\pnpm.cjs`, "pnpm", `C:\g\node_modules\a\node_modules\pnpm`},
		{`C:\tools\pnpm.cjs`, "pnpm", `C:\tools`},
	}
	for _, c := range cases {
		if name, dir := scriptPackage(c.script); name != c.name || dir != c.dir {
			t.Errorf("scriptPackage(%s) = %q, %q; want %q, %q", c.script, name, dir, c.name, c.dir)
		}
	}
}

// The decision reads the script's own permissions: unreadable when nobody the
// launch carries is named, readable once one is, and left to node when the
// script is not there or is the project's.
func TestAScriptIsUnreadableUntilSomeoneTheLaunchCarriesIsNamed(t *testing.T) {
	dir := filepath.Join(tempDir(t), "node_modules", "pnpm", "bin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "pnpm.cjs")
	if err := os.WriteFile(script, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	sid, err := scopeCapabilitySID(dir)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	readers := []string{sid}
	args := []string{script, "install"}

	if got := unreadablePackageManagerScript("node", args, readers); got != script {
		t.Fatalf("a script nobody the launch carries can read was not found unreadable: %q", got)
	}
	pkgDir := filepath.Dir(dir)
	t.Cleanup(func() { _ = revokeSandboxReadExec(sid, pkgDir) })
	if _, err := grantSandboxReadExec(sid, pkgDir); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if got := unreadablePackageManagerScript("node", args, readers); got != "" {
		t.Errorf("a script the project's capability can read was found unreadable: %q", got)
	}

	// ALL APPLICATION PACKAGES is how Program Files is readable to every
	// AppContainer, and is among the readers a launch has.
	aap := "S-1-15-2-1"
	aapDir := filepath.Join(tempDir(t), "node_modules", "pnpm", "bin")
	if err := os.MkdirAll(aapDir, 0o700); err != nil {
		t.Fatal(err)
	}
	otherScript := filepath.Join(aapDir, "pnpm.cjs")
	if err := os.WriteFile(otherScript, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	launchReaders := sandboxReaderSIDs("S-1-15-2-9-9", []string{sid})
	if got := unreadablePackageManagerScript("node", []string{otherScript}, launchReaders); got != otherScript {
		t.Fatalf("a script in a folder only the user can read was not found unreadable: %q", got)
	}
	t.Cleanup(func() { _ = revokeSandboxReadExec(aap, filepath.Dir(aapDir)) })
	if _, err := grantSandboxReadExec(aap, filepath.Dir(aapDir)); err != nil {
		t.Skipf("cannot write an ACL in the test environment: %v", err)
	}
	if got := unreadablePackageManagerScript("node", []string{otherScript}, launchReaders); got != "" {
		t.Errorf("a script every AppContainer can read was found unreadable: %q", got)
	}

	// Not node, a relative path, a script that is not there, a script that is not a
	// package manager's. None of them is this check's to refuse.
	other := filepath.Join(tempDir(t), "tool.js")
	_ = os.WriteFile(other, []byte("0"), 0o600)
	for name, c := range map[string]struct {
		command string
		args    []string
	}{
		"not node":        {"npm", args},
		"relative":        {"node", []string{`node_modules\pnpm\bin\pnpm.cjs`, "install"}},
		"missing":         {"node", []string{filepath.Join(dir, "gone", "pnpm.cjs"), "install"}},
		"not a manager's": {"node", []string{other}},
	} {
		if got := unreadablePackageManagerScript(c.command, c.args, []string{"S-1-5-32-545"}); got != "" {
			t.Errorf("%s: %q was refused", name, got)
		}
	}
}

func TestTheRefusalNamesBothWaysToRunIt(t *testing.T) {
	msg := notReadableScriptError(`C:\g\node_modules\pnpm\bin\pnpm.cjs`).Error()
	for _, want := range []string{"allow_read_exec", `C:\g\node_modules\pnpm `, "nvx --no-sandbox npm install -g pnpm", "nvx --no-sandbox'"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, msg)
		}
	}
}

// Through the built binary and a real AppContainer. A pnpm kept outside nvx's
// folders, started as `node <its script>`, died with node's MODULE_NOT_FOUND,
// exit 1, where the docs promise a refusal that says what to do. Its folder in
// allow_read_exec, as the refusal says, runs it.
func TestProbePackageManagerOutsideNvxIsRefusedWithTheFix(t *testing.T) {
	f := newBatchProbeFixture(t)
	pkg := filepath.Join(tempDir(t), "gprefix", "node_modules", "pnpm")
	script := filepath.Join(pkg, "bin", "pnpm.cjs")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(`console.log("PROBE pnpm ran " + process.argv.slice(2).join(" "))`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := f.run(t, "--strict", "shim", "node", script, "--version")
	if strings.Contains(out, "PROBE pnpm ran") {
		t.Fatalf("the script ran, so the sandbox could read it and the case under test was not made:\n%s", out)
	}
	for _, want := range []string{"allow_read_exec", pkg, "nvx --no-sandbox npm install -g pnpm"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q (%v):\n%s", want, err, out)
		}
	}
	if strings.Contains(out, "MODULE_NOT_FOUND") {
		t.Errorf("node's own error reached the person:\n%s", out)
	}
	if code := exitCodeOf(err); code != exitRefused {
		t.Errorf("exit %d, want %d for a refusal", code, exitRefused)
	}

	// The remedy it names.
	policy := `{"isolation":{"filesystem":{"allow_read_exec":[` + jsonString(pkg) + `]}}}`
	if err := os.WriteFile(filepath.Join(f.home, "policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = f.run(t, "--strict", "shim", "node", script, "--version")
	if !strings.Contains(out, "PROBE pnpm ran --version") || err != nil {
		t.Errorf("with the folder in allow_read_exec the script did not run (%v):\n%s", err, out)
	}
}

// exitCodeOf is the exit code a finished command reported, 0 for a nil error.
func exitCodeOf(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return 0
}

// jsonString renders s as a JSON string, with its backslashes escaped.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
