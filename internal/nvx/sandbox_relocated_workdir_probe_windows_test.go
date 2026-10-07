//go:build windows

package nvx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A scaffolder never reports success while what it wrote is deleted.
//
// Measured 2026-10-07: from a folder that is not a project and holds 20,200
// entries, a contained scaffold printed "Done" and exited 0, and its new folder
// was gone afterwards. The grant for the folder overran its 1.5 s bound, so the
// command ran in the sandbox's home, which is deleted at exit. From C:\Users,
// which the sandbox may never write, the same happened by design, with a warning
// before the command started and exit 0 after it.
//
// Both tests run the real binary with a scratch NVX_HOME. The scaffold is a
// node one-liner that makes a folder and a file in it, which is all `npm create`
// does to the working directory.

const scaffoldScript = `const fs = require("fs"), path = require("path");
const name = process.argv[1];
fs.mkdirSync(name);
fs.writeFileSync(path.join(name, "index.txt"), "scaffolded");
console.log("Done. Now run: cd " + name);`

// relocationProbe is a built nvx, a scratch NVX_HOME and an environment that
// finds node without going through any nvx shim.
type relocationProbe struct {
	nvx  string
	home string
	env  []string
}

func newRelocationProbe(t *testing.T) relocationProbe {
	t.Helper()
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	node, err := lookPathSkippingNvxShimsUncached("node", "")
	if err != nil {
		t.Skip("node is not installed; the contained scaffold needs it")
	}
	dir := tempDir(t)
	nvxExe := filepath.Join(dir, "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, "github.com/fstubner/nvx/cmd/nvx").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}
	// node.exe on its own, so the scratch home stages one file rather than
	// whatever global packages sit beside the machine's node.
	nodeDir := filepath.Join(dir, "node")
	if err := os.MkdirAll(nodeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(node, filepath.Join(nodeDir, "node.exe"), 0o700); err != nil {
		t.Fatalf("copy node: %v", err)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The AppContainer profiles this home registered outlive it otherwise.
		entries, _ := os.ReadDir(filepath.Join(home, "packages"))
		for _, e := range entries {
			deleteAppContainerProfile(e.Name())
		}
	})
	env := []string{"NVX_HOME=" + home, "PATH=" + nodeDir + string(os.PathListSeparator) +
		filepath.Join(os.Getenv("SystemRoot"), "System32")}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !strings.EqualFold(k, "PATH") && !strings.EqualFold(k, "NVX_HOME") {
			env = append(env, kv)
		}
	}
	return relocationProbe{nvx: nvxExe, home: home, env: env}
}

// scaffold runs the scaffold contained from dir and returns what nvx printed
// and its exit code.
func (p relocationProbe) scaffold(t *testing.T, dir string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.nvx, "--strict", "node", "-e", scaffoldScript, "myapp")
	cmd.Dir = dir
	cmd.Env = p.env
	cmd.WaitDelay = 10 * time.Second
	outBytes, err := cmd.CombinedOutput()
	out := string(outBytes)
	if ctx.Err() != nil {
		t.Fatalf("the contained scaffold did not finish within 120s:\n%s", out)
	}
	requireContainedRunLaunched(t, out)
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run nvx: %v\n%s", err, out)
	}
	return out, code
}

// A folder too large to grant in full is where the scaffold lands.
//
// Recorded as too slow to grant, which is what every run after the first sees
// for such a folder, so the test does not depend on how fast this machine
// propagates a permission over twenty thousand files.
func TestAScaffoldFromAFolderTooLargeToGrantStaysWhereItWasRun(t *testing.T) {
	p := newRelocationProbe(t)
	work := tempDir(t)
	if findProjectRoot(work) != "" {
		t.Skipf("%s sits under a package.json; the test needs a folder that is not a project", work)
	}
	saveAncestorSkips(p.home, map[string]time.Time{normalizeAncestorKey(work): time.Now()})

	out, code := p.scaffold(t, work)
	if code != 0 {
		t.Fatalf("the scaffold exited %d:\n%s", code, out)
	}
	got, err := os.ReadFile(filepath.Join(work, "myapp", "index.txt"))
	if err != nil || string(got) != "scaffolded" {
		t.Fatalf("the scaffold said Done and its project is not in %s (%v). It went to the sandbox's "+
			"home and was deleted with it:\n%s", work, err, out)
	}
}

// From a folder the sandbox may never write, the scaffold's project cannot be
// kept, and the run says so and fails.
func TestAScaffoldThatCannotRunWhereItWasStartedDoesNotReportSuccess(t *testing.T) {
	p := newRelocationProbe(t)
	// Inside NVX_HOME, which a contained run may not write.
	inside := filepath.Join(p.home, "inside")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}

	out, code := p.scaffold(t, inside)
	if _, err := os.Stat(filepath.Join(inside, "myapp")); err == nil {
		t.Fatalf("the sandbox wrote inside NVX_HOME:\n%s", out)
	}
	if code == 0 {
		t.Fatalf("the scaffold exited 0 although its project was deleted:\n%s", out)
	}
	if code != exitRefused {
		t.Errorf("exit %d, want %d", code, exitRefused)
	}
	if !strings.Contains(out, "wrote myapp") {
		t.Errorf("the run did not say what it deleted:\n%s", out)
	}
}
