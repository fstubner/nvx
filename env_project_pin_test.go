package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// captureStderrHere swaps os.Stderr for a pipe and returns what was written.
//
// Its own helper rather than the one in sandbox_loopback_exemption_windows_test.go,
// which is behind a windows build tag; this behaviour is not platform-specific.
func captureStderrHere(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 64*1024)
		n, _ := r.Read(buf)
		done <- string(buf[:n])
	}()
	fn()
	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// The shim must not silently run a version the project pinned against.
//
// nvx switches versions through the shell integration; without it loaded the
// shim resolves the global default, or whatever `node` is on PATH. An
// acceptance pass found the consequence on 2026-09-03: in a project whose
// .nvmrc pinned 22, with only v22.23.2 installed, `nvx node -v` ran an ambient
// v24.14.1 and said nothing at all. `nvx use` already warns loudly when its
// output is not evaluated; the shim, which is what people run all day, did not.
func TestTheShimSaysWhenTheProjectAsksForAnotherVersion(t *testing.T) {
	nvxHome := tempDir(t)
	proj := tempDir(t)
	if err := os.WriteFile(filepath.Join(proj, ".nvmrc"), []byte("20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// os.Chdir with a restore, not t.Chdir: the module targets go1.23 and
	// t.Chdir needs 1.24. Tests in a package run sequentially unless they ask
	// otherwise, so moving the process cwd here is safe as long as it goes back.
	prevWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	// A binary under versions/node/v22.23.2 is how nvx names the version it is
	// about to run, without needing that version installed.
	running := filepath.Join(nvxHome, "versions", "node", "v22.23.2", "node.exe")
	if err := os.MkdirAll(filepath.Dir(running), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(running, []byte("stub"), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}

	node := runtimeForShim("node")
	got := captureStderrHere(t, func() {
		warnIfProjectPinsAnotherVersion(nvxHome, node, "v22.23.2", running)
	})
	if !strings.Contains(got, ".nvmrc") || !strings.Contains(got, "20") {
		t.Errorf("the project asked for 20 and v22.23.2 ran; nvx said nothing useful:\n%s", got)
	}
	if !strings.Contains(got, "v22.23.2") {
		t.Errorf("the warning does not name the version that actually ran:\n%s", got)
	}

	// Agreement is silent. A warning on every command in a correctly pinned
	// project would be worse than the silence it replaced.
	if err := os.WriteFile(filepath.Join(proj, ".nvmrc"), []byte("22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if quiet := captureStderrHere(t, func() {
		warnIfProjectPinsAnotherVersion(nvxHome, node, "v22.23.2", running)
	}); strings.TrimSpace(quiet) != "" {
		t.Errorf("a project pinned to 22 running v22.23.2 produced output:\n%s", quiet)
	}

	// No declaration, nothing to disagree with.
	if err := os.Remove(filepath.Join(proj, ".nvmrc")); err != nil {
		t.Fatal(err)
	}
	if quiet := captureStderrHere(t, func() {
		warnIfProjectPinsAnotherVersion(nvxHome, node, "v22.23.2", running)
	}); strings.TrimSpace(quiet) != "" {
		t.Errorf("a project declaring no version produced output:\n%s", quiet)
	}
}

// A range in .nvmrc or engines is satisfied the same way `nvx use` satisfies
// one, so a project asking for "^22" is not told it is running the wrong thing.
func TestVersionSatisfiesAcceptsTheRangesProjectsWrite(t *testing.T) {
	for _, tc := range []struct {
		running, want string
		ok            bool
	}{
		{"v22.23.2", "22", true},
		{"v22.23.2", "v22.23.2", true},
		{"v22.23.2", "^22", true},
		{"v22.23.2", ">=20 <23", true},
		{"v22.23.2", "22.23", true},
		{"v22.23.2", "20", false},
		{"v22.23.2", "^20", false},
		{"v22.23.2", ">=23", false},
	} {
		if got := versionSatisfies(tc.running, tc.want); got != tc.ok {
			t.Errorf("versionSatisfies(%q, %q) = %v, want %v", tc.running, tc.want, got, tc.ok)
		}
	}
}

// The warning is actually WIRED IN, checked through the built binary.
//
// The test above proves warnIfProjectPinsAnotherVersion says the right thing;
// it passed with the call site deleted, because it calls the function directly.
// That is the shape of test this project has been caught by twice in one day,
// so this runs the real shim and reads its stderr.
//
// No runtime is installed: with an empty NVX_HOME the shim falls back to
// whatever `node` is on PATH, which is exactly the unintegrated setup the
// warning exists for, and it costs no download.
func TestTheShimWarningIsWiredIntoTheRealBinary(t *testing.T) {
	realNode, err := lookPathSkippingNvxShims("node", tempDir(t))
	if err != nil {
		t.Skipf("no ambient node to fall back to: %v", err)
	}
	t.Logf("ambient node: %s", realNode)

	dir := tempDir(t)
	exe := filepath.Join(dir, "nvx"+exeSuffixForTest())
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}

	proj := tempDir(t)
	if err := os.WriteFile(filepath.Join(proj, "package.json"), []byte(`{"name":"p","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A version nothing on this machine is likely to be running.
	if err := os.WriteFile(filepath.Join(proj, ".nvmrc"), []byte("14\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := execCommandForTest(exe, "--no-sandbox", "shim", "node", "-v")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "NVX_HOME="+tempDir(t), "NVX_TRACE=")
	out, _ := cmd.CombinedOutput()
	got := string(out)
	if !strings.Contains(got, ".nvmrc") {
		t.Errorf("a project pinned to node 14, running something else, produced no mention of .nvmrc.\n"+
			"The warning is not reached on the real shim path -- which is what a deleted call site "+
			"looks like, and what the direct test above cannot see.\nnvx said:\n%s", got)
	}
}

func exeSuffixForTest() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func runGoBuild(out string) ([]byte, error) {
	return exec.Command("go", "build", "-o", out, ".").CombinedOutput()
}

func execCommandForTest(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
