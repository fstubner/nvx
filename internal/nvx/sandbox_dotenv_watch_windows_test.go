//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dotenvIsProtected reports whether path's permissions keep the sandbox out,
// the check the launch makes before it changes anything.
func dotenvIsProtected(t *testing.T, root, path string) bool {
	t.Helper()
	h, _, err := openDotenvWithin(root, path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer syscall.CloseHandle(h)
	acl, err := readDotenvACL(h)
	if err != nil {
		t.Fatal(err)
	}
	return !dotenvNeedsProtection(acl.protected, acl.aces)
}

// TestWatchDotenvFilesProtectsFilesThatAppear runs the watch a contained launch
// starts against a real project, granted the way a launch grants it. Dotenv
// files created, replaced or moved in while it runs are protected. A package's
// .env under node_modules and a template are left as they are.
func TestWatchDotenvFilesProtectsFilesThatAppear(t *testing.T) {
	nvxHome := tempDir(t)
	project := tempDir(t)
	outside := tempDir(t)
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatal(err)
	}
	root, err := finalPathOf(project)
	if err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	waitProtected := func(what, p string, start time.Time) {
		t.Helper()
		for deadline := start.Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			if dotenvIsProtected(t, root, p) {
				t.Logf("%s: protected %v after the change", what, time.Since(start).Round(time.Millisecond))
				return
			}
		}
		t.Fatalf("%s: %s was not protected within 5s", what, p)
	}

	w := watchDotenvFiles(nvxHome, project)
	defer w.stop()

	env := filepath.Join(project, ".env")
	start := time.Now()
	write(env, "API_KEY=1")
	waitProtected("create .env", env, start)

	// Replace-on-save, through a temporary name that is a dotenv name and one
	// that is not. The second reaches .env only by the rename.
	for _, tmp := range []string{".env.tmp", "env.new"} {
		write(filepath.Join(project, tmp), "API_KEY=replaced")
		start = time.Now()
		if err := os.Rename(filepath.Join(project, tmp), env); err != nil {
			t.Fatal(err)
		}
		waitProtected("rename "+tmp+" over .env", env, start)
	}

	// A directory moved in from outside the project.
	write(filepath.Join(outside, "app", ".env.local"), "API_KEY=2")
	start = time.Now()
	if err := os.Rename(filepath.Join(outside, "app"), filepath.Join(project, "app")); err != nil {
		t.Fatal(err)
	}
	waitProtected("move in a directory holding .env.local", filepath.Join(project, "app", ".env.local"), start)

	// Changes are reported in order, so once a file created after these is
	// protected the watch has seen them too.
	dep := filepath.Join(project, "node_modules", "p", ".env")
	example := filepath.Join(project, ".env.example")
	write(dep, "X=1")
	write(example, "API_KEY=")
	last := filepath.Join(project, ".env.last")
	start = time.Now()
	write(last, "API_KEY=3")
	waitProtected("create .env.last", last, start)
	for _, p := range []string{dep, example} {
		if dotenvIsProtected(t, root, p) {
			t.Errorf("%s was protected; it should stay as it is", p)
		}
	}
}

// TestWatchHidesNewDotenvFromRunningProcess (NVX_PROBE=1) starts a contained
// node process, then creates .env and replaces it by rename while the process
// runs, and has the process read it after each. With the watch it gets "access
// denied" both times. Without it, it reads the secret. That half is the
// premise, a file that appears after the launch scan being open to the sandbox.
func TestWatchHidesNewDotenvFromRunningProcess(t *testing.T) {
	run, workDir := walkupProbeIn(t, "nvx.sandbox.dotenvwatch.probe", `
const fs = require('fs'), path = require('path');
const dir = process.cwd(), out = [];
const pause = new Int32Array(new SharedArrayBuffer(4));
const waitFor = (f) => {
  for (const end = Date.now() + 30000; Date.now() < end; Atomics.wait(pause, 0, 0, 5)) {
    if (fs.existsSync(path.join(dir, f))) return true;
  }
  return false;
};
fs.writeFileSync(path.join(dir, 'ready'), '');
for (const step of ['created', 'replaced']) {
  if (!waitFor('go-' + step)) { out.push(step + ' TIMEOUT'); break; }
  try { out.push(step + ' READ ' + fs.readFileSync(path.join(dir, '.env'), 'utf8')); }
  catch (e) { out.push(step + ' ' + e.code); }
  fs.writeFileSync(path.join(dir, 'done-' + step), '');
}
fs.writeFileSync(process.argv[2], out.join('\n'));
`)
	root, err := finalPathOf(workDir)
	if err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(workDir, ".env")
	waitFor := func(name string) bool {
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
				return true
			}
		}
		return false
	}
	// protectedWithin polls .env's permissions for up to limit and says how
	// long protection took. It runs off the test goroutine, so it cannot fail
	// the test itself.
	protectedWithin := func(limit time.Duration, start time.Time) string {
		for time.Since(start) < limit {
			if h, _, err := openDotenvWithin(root, env); err == nil {
				acl, err := readDotenvACL(h)
				syscall.CloseHandle(h)
				if err == nil && !dotenvNeedsProtection(acl.protected, acl.aces) {
					return "protected after " + time.Since(start).Round(time.Millisecond).String()
				}
			}
			time.Sleep(time.Millisecond)
		}
		return "not protected after " + limit.String()
	}

	// phase runs the contained process while another goroutine changes .env
	// under it, and returns the process's report and that goroutine's notes.
	phase := func(watch bool) (report string, notes []string) {
		t.Helper()
		for _, f := range []string{".env", "env.new", "ready", "go-created", "done-created", "go-replaced", "done-replaced"} {
			_ = os.Remove(filepath.Join(workDir, f))
		}
		if watch {
			w := watchDotenvFiles(tempDir(t), workDir)
			defer w.stop()
		}
		driven := make(chan []string, 1)
		go func() {
			var notes []string
			defer func() { driven <- notes }()
			change := func(what string, do func() error) bool {
				if !waitFor("ready") {
					notes = append(notes, "the contained process never started")
					return false
				}
				start := time.Now()
				if err := do(); err != nil {
					notes = append(notes, err.Error())
					return false
				}
				notes = append(notes, what+": "+protectedWithin(2*time.Second, start))
				return os.WriteFile(filepath.Join(workDir, "go-"+what), nil, 0o600) == nil && waitFor("done-"+what)
			}
			if !change("created", func() error { return os.WriteFile(env, []byte("ENV=created-secret"), 0o600) }) {
				return
			}
			replacement := filepath.Join(workDir, "env.new")
			if err := os.WriteFile(replacement, []byte("ENV=replaced-secret"), 0o600); err != nil {
				notes = append(notes, err.Error())
				return
			}
			change("replaced", func() error { return os.Rename(replacement, env) })
		}()
		report = run(true)
		return report, <-driven
	}

	report, notes := phase(false)
	t.Logf("without the watch: %q %q", report, notes)
	if !strings.Contains(report, "created READ ENV=created-secret") || !strings.Contains(report, "replaced READ ENV=replaced-secret") {
		t.Fatalf("without the watch the contained process could not read the new .env, so this checks nothing: %q", report)
	}

	report, notes = phase(true)
	t.Logf("with the watch: %q %q", report, notes)
	for _, step := range []string{"created", "replaced"} {
		if !strings.Contains(report, step+" EPERM") && !strings.Contains(report, step+" EACCES") {
			t.Errorf("with the watch, the %s .env was not refused: %q", step, report)
		}
	}
	if b, err := os.ReadFile(env); err != nil || string(b) != "ENV=replaced-secret" {
		t.Errorf("the developer cannot read .env after the watch protected it: %q %v", b, err)
	}
}

// TestWatchDotenvFilesStopsAtTheCap floods the project with dotenv files while
// the watch runs. Only the cap's worth are protected, nvx says so once, and a
// file past the cap is left alone until the next launch. A file with a record
// still gets its protection back after it is replaced.
func TestWatchDotenvFilesStopsAtTheCap(t *testing.T) {
	lowerDotenvCap(t, 5)
	nvxHome := tempDir(t)
	project := tempDir(t)
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatal(err)
	}
	root, err := finalPathOf(project)
	if err != nil {
		t.Fatal(err)
	}
	write := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("API_KEY=1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var files []string
	for i := 0; i < maxRecordedDotenv+3; i++ {
		files = append(files, filepath.Join(project, fmt.Sprintf(".env.%02d", i)))
	}
	protected := func() (n int) {
		for _, p := range files {
			if dotenvIsProtected(t, root, p) {
				n++
			}
		}
		return n
	}

	warnings := captureStderr(t, func() {
		w := watchDotenvFiles(nvxHome, project)
		defer w.stop()
		for _, p := range files {
			write(p)
		}
		deadline := time.Now().Add(10 * time.Second)
		for protected() < maxRecordedDotenv && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}

		// A file with a record, replaced, then a file with none. The watch reports
		// changes in order, so once the first is protected again it has seen the
		// second too.
		records := loadProjectGrants(nvxHome, project).ProtectedDotenv
		if len(records) != maxRecordedDotenv {
			t.Fatalf("the record holds %d files, want the cap, %d", len(records), maxRecordedDotenv)
		}
		replaced := records[0].Path
		late := filepath.Join(project, ".env.late")
		write(late)
		write(replaced + ".tmp")
		if err := os.Rename(replaced+".tmp", replaced); err != nil {
			t.Fatal(err)
		}
		deadline = time.Now().Add(10 * time.Second)
		for !dotenvIsProtected(t, root, replaced) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !dotenvIsProtected(t, root, replaced) {
			t.Errorf("the replaced file with a record was not protected again")
		}
		if dotenvIsProtected(t, root, late) {
			t.Errorf("a file past the cap was protected")
		}
	})
	if n := protected(); n != maxRecordedDotenv {
		t.Errorf("%d files are protected, want the cap, %d", n, maxRecordedDotenv)
	}
	if n := len(loadProjectGrants(nvxHome, project).ProtectedDotenv); n != maxRecordedDotenv {
		t.Errorf("the record holds %d files, want the cap, %d", n, maxRecordedDotenv)
	}
	if n := strings.Count(warnings, "is full"); n != 1 {
		t.Errorf("nvx said it had stopped %d times, want once:\n%s", n, warnings)
	}
}

// Past the cap the watch still hides a file the launch found when an editor
// replaces it, record or not, as the launch hid it. A file created during the
// run stays readable.
func TestWatchHidesAReplacedLaunchFileWithoutARecord(t *testing.T) {
	lowerDotenvCap(t, 5)
	nvxHome := tempDir(t)
	project := tempDir(t)
	capSID, err := scopeCapabilitySID(project)
	if err != nil {
		t.Fatal(err)
	}
	write := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("API_KEY=1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var atLaunch []string
	for i := 0; i < maxRecordedDotenv+2; i++ {
		p := filepath.Join(project, fmt.Sprintf(".env.%02d", i))
		write(p)
		atLaunch = append(atLaunch, p)
	}
	if err := grantSandboxModify(capSID, project); err != nil {
		t.Fatal(err)
	}
	root, err := finalPathOf(project)
	if err != nil {
		t.Fatal(err)
	}

	// As platformLaunchNative does it: the watch, then the launch scan.
	w := watchDotenvFiles(nvxHome, project)
	defer w.stop()
	w.presentAtLaunch(hideDotenvFromSandbox(nvxHome, project))

	records := loadProjectGrants(nvxHome, project).ProtectedDotenv
	unrecorded := ""
	for _, p := range atLaunch {
		if !slices.ContainsFunc(records, func(r protectedDotenv) bool { return sameGrantPath(r.Path, p) }) {
			unrecorded = p
		}
	}
	if unrecorded == "" {
		t.Fatalf("every file has a record, so this checks nothing: %+v", records)
	}

	// The watch reports changes in order, so once the replaced file is
	// protected again it has seen the new one too.
	late := filepath.Join(project, ".env.late")
	write(late)
	write(unrecorded + ".tmp")
	if err := os.Rename(unrecorded+".tmp", unrecorded); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !dotenvIsProtected(t, root, unrecorded) && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if !dotenvIsProtected(t, root, unrecorded) {
		t.Errorf("%s, found at launch and replaced, was not protected again", filepath.Base(unrecorded))
	}
	if dotenvIsProtected(t, root, late) {
		t.Errorf("a file created during the run past the cap was protected")
	}
}
