package nvx

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeNodeInstall puts a stand-in node binary under versions/node/<version>.
// The stand-in is this test binary, which prints the path it was started from
// when NVX_TEST_FAKE_RUNTIME=1 (see TestMain), so the output names the version
// the shim chose.
func fakeNodeInstall(t *testing.T, nvxHome, version string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(nvxHome, "versions", "node", version, "bin", "node")
	if runtime.GOOS == "windows" {
		bin = filepath.Join(nvxHome, "versions", "node", version, "node.exe")
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	// A copy, not a hard link. Windows will not delete any link to the image of
	// a running process, so a linked stand-in outlived the test in the temp dir.
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, data, 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	return bin
}

// pinTestHome is an NVX_HOME with node v20.11.1 and v22.23.3 installed as
// stand-ins, v22.23.3 the global default, and isolation off so `node` takes
// the uncontained path.
func pinTestHome(t *testing.T) string {
	t.Helper()
	home := tempDir(t)
	fakeNodeInstall(t, home, "v20.11.1")
	fakeNodeInstall(t, home, "v22.23.3")
	if err := CreateLink(currentLinkPath(home), filepath.Join(home, "versions", "node", "v22.23.3")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(`{"isolation":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NVX_TEST_FAKE_RUNTIME", "1")
	// No nvx version directory on PATH, as in an IDE task or a terminal
	// without the shell integration.
	t.Setenv("PATH", tempDir(t))
	// runShim marks the process as the top-level invocation through this
	// variable. Restored so the next test starts unmarked.
	t.Setenv(nvxActiveEnvVar, "")
	return home
}

// runNodeThroughShim runs `node --version` through the shim entry point and
// returns what the runtime printed and what nvx printed.
func runNodeThroughShim(t *testing.T, home string) (stdout, stderr string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	done := make(chan string, 1)
	go func() {
		all, _ := io.ReadAll(r)
		done <- string(all)
	}()
	var code int
	stderr = captureStderrHere(t, func() { code = runShim("node", []string{"--version"}, home) })
	_ = w.Close()
	stdout = <-done
	_ = r.Close()
	if code != 0 {
		t.Fatalf("the shim exited %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	return stdout, stderr
}

func writeNvmrc(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte(version+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The shim runs the installed version the project pins, from the project
// directory or any directory below it, and the default where nothing is pinned.
//
// Before this the shim always ran the default unless the shell integration had
// switched PATH. Measured 2026-10-01: with .nvmrc at 20.11 and the default at
// 22, `node --version` through the shim printed v22.23.3 and a warning. An IDE
// task, a git hook, cron or CI never loads the integration.
func TestTheShimRunsTheVersionTheProjectPins(t *testing.T) {
	home := pinTestHome(t)
	proj := tempDir(t)
	writeNvmrc(t, proj, "20.11")
	nested := filepath.Join(proj, "src", "deep")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	unpinned := tempDir(t)
	// A version this shell already has on PATH, from `nvx use` or the shell
	// integration, is what a bare `node` runs in that shell without the shim.
	// The shim agrees with it, and says the file asks for something else.
	shellActive := filepath.Join(home, "versions", "node", "v22.23.3")
	if runtime.GOOS != "windows" {
		shellActive = filepath.Join(shellActive, "bin")
	}

	for _, tc := range []struct {
		name, dir, path, want string
		warns                 bool
	}{
		{"project directory", proj, "", "v20.11.1", false},
		{"directory below the project", nested, "", "v20.11.1", false},
		{"directory with no version file", unpinned, "", "v22.23.3", false},
		{"shell with another version active", proj, shellActive, "v22.23.3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path != "" {
				t.Setenv("PATH", tc.path)
			}
			inProjectDir(t, tc.dir)
			out, errOut := runNodeThroughShim(t, home)
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %s to run, got:\n%s\nnvx said:\n%s", tc.want, out, errOut)
			}
			if warned := strings.Contains(errOut, "asks for"); warned != tc.warns {
				t.Errorf("warned=%v, want %v:\n%s", warned, tc.warns, errOut)
			}
		})
	}
}

// The search for a version file ends at the user's home directory. Above it
// is no project of this user's, and the shim searches on every run.
func TestTheVersionFileSearchStopsAtHome(t *testing.T) {
	outer := tempDir(t)
	writeNvmrc(t, outer, "20")
	home := filepath.Join(outer, "home")
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got, src, _ := DetectVersionConfig(proj); got != "" {
		t.Errorf("read %q from %s, above the home directory", got, src)
	}
	writeNvmrc(t, home, "22")
	if got, _, _ := DetectVersionConfig(proj); got != "22" {
		t.Errorf("want the home directory's own .nvmrc (22), got %q", got)
	}
}

// A pin that is not installed runs the default, as before, and the warning
// names the command that fixes it. The shim never installs anything itself.
func TestTheShimRunsTheDefaultWhenThePinIsNotInstalled(t *testing.T) {
	home := pinTestHome(t)
	proj := tempDir(t)
	writeNvmrc(t, proj, "18")
	inProjectDir(t, proj)

	out, errOut := runNodeThroughShim(t, home)
	if !strings.Contains(out, "v22.23.3") {
		t.Errorf("want the default v22.23.3 to run, got:\n%s", out)
	}
	if !strings.Contains(errOut, "nvx install 18") {
		t.Errorf("the warning does not say `nvx install 18` fixes it:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(home, "versions", "node", "v18")); err == nil {
		t.Error("the shim installed something")
	}
}

// A contained run resolves its runtime the same way, with the policy's
// runtime.versions pin ahead of the version file.
//
// resolveSandboxCommand is what every contained launch takes its command path
// from, and the sandbox grants follow that path.
func TestTheSandboxRunsThePolicyPinThenTheVersionFile(t *testing.T) {
	home := pinTestHome(t)
	proj := tempDir(t)
	writeNvmrc(t, proj, "20.11")
	inProjectDir(t, proj)
	config := SandboxConfig{NvxHome: home, Command: "node"}

	var policy Policy
	if got := resolveSandboxCommand(config, policy); !strings.Contains(got, "v20.11.1") {
		t.Errorf("with no policy pin, want the .nvmrc version v20.11.1 in the sandbox, got %q", got)
	}

	policy.Runtime.Versions = map[string]string{"node": "22"}
	if got := resolveSandboxCommand(config, policy); !strings.Contains(got, "v22.23.3") {
		t.Errorf("the policy pins 22, want v22.23.3 in the sandbox, got %q", got)
	}
}
