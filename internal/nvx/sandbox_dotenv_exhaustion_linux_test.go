//go:build linux

package nvx

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// When the per-user inotify watch limit is exhausted the watcher cannot watch a
// project's subdirectories, so a .env created later in one raises no event. The
// periodic rescan (dotenvRescanInterval) is the fallback that still covers it.
//
// This lowers fs.inotify.max_user_watches so the walk at launch cannot watch
// every folder, creates a .env deep in an unwatched one during the run, and
// requires the contained process to be refused within a few rescan intervals.
// Without the fallback the file stays readable for the life of the run.
//
// Like the other dotenv probes it runs the real supervisor with the test binary
// as the target and skips where the mount namespace or the sysctl write is
// refused; the privileged CI step runs it again, where a skip fails the job.
func TestContainedProcessStillMasksDotenvAfterWatchExhaustion(t *testing.T) {
	const name = "TestContainedProcessStillMasksDotenvAfterWatchExhaustion"
	if runDotenvProbeRole(name, runDotenvExhaustionProbeTarget) {
		return
	}

	const watchesPath = "/proc/sys/fs/inotify/max_user_watches"
	prev, err := os.ReadFile(watchesPath)
	if err != nil {
		t.Skipf("cannot read %s: %v", watchesPath, err)
	}
	if err := os.WriteFile(watchesPath, []byte("1"), 0); err != nil {
		t.Skipf("cannot lower %s (needs privilege): %v", watchesPath, err)
	}
	t.Cleanup(func() { _ = os.WriteFile(watchesPath, bytes.TrimSpace(prev), 0) })

	base := tempDir(t)
	proj := filepath.Join(base, "proj")
	deep := filepath.Join(proj, "sub1", "sub2", "sub3")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := dotenvProbeCommand(t, name, proj)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	awaitProbeReady(t, proj, 1, exited, &out)
	writeOrKill(t, cmd, exited, filepath.Join(deep, ".env"), dotenvProbeSecret)
	writeOrKill(t, cmd, exited, filepath.Join(proj, "done-1"), "")

	awaitProbeExit(t, cmd, exited, &out)
	t.Logf("contained probe output:\n%s", out.String())

	got := parseProbeResults(out.String())
	if got["step1"] != "denied" {
		t.Errorf("step1 = %q, want %q: a .env created in an unwatched folder after watch exhaustion stayed readable", got["step1"], "denied")
	}
}

// runDotenvExhaustionProbeTarget reads the deep .env until it is refused or the
// deadline passes. The deadline is several rescan intervals so the fallback has
// time to cover a file inotify never reported.
func runDotenvExhaustionProbeTarget(work string) {
	deep := filepath.Join(work, "sub1", "sub2", "sub3", ".env")
	_ = os.WriteFile(filepath.Join(work, "ready-1"), nil, 0o644)
	awaitFileExists(filepath.Join(work, "done-1"), 30*time.Second)

	const marker = "DOTENV-SECRET"
	result, seen := "timeout", false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		b, err := os.ReadFile(deep)
		if errors.Is(err, fs.ErrPermission) {
			result = "denied"
			break
		}
		if err == nil && strings.Contains(string(b), marker) {
			seen = true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result == "timeout" && seen {
		result = "secret"
	}
	fmt.Printf("step1=%s\nstep1.seen=%t\n", result, seen)
}
