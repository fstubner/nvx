package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Node version that cannot be migrated is reported, not dropped in silence.
//
// Versions installed by an old nvx live in ~/.nvx/versions/<v>; the current
// layout is ~/.nvx/versions/node/<v>, and MigrateLegacyNodeVersions moves them
// on startup. The rename's error was discarded. When it fails -- a directory of
// the same name already under node/ (which is what a partly-completed migration
// leaves), a file open by another process, a permission problem -- the version
// stays where nothing looks for it. `nvx list` then omits a runtime that is
// installed and on disk, and nothing anywhere says why.
func TestAFailedVersionMigrationIsReported(t *testing.T) {
	nvxHome := tempDir(t)
	legacy := filepath.Join(nvxHome, "versions", "v20.0.0")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	// The destination is already taken, so the rename cannot succeed. This is
	// the shape a migration interrupted halfway leaves behind.
	blocking := filepath.Join(nvxHome, "versions", "node", "v20.0.0", "bin")
	if err := os.MkdirAll(blocking, 0o700); err != nil {
		t.Fatal(err)
	}

	out := captureStderrHere(t, func() { MigrateLegacyNodeVersions(nvxHome) })

	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("the legacy directory moved after all, so this test no longer covers a failed rename: %v", err)
	}
	if !strings.Contains(out, "v20.0.0") {
		t.Fatalf("a version was left unmigrated and nothing said so:\n%s", out)
	}
}
