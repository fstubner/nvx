package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// Parses the repository's own site lockfile, which is the file that produced
// the "Failed to parse package-lock.json for verification" warning in a real
// run on 2026-09-16.
func TestParsesThisRepositorysOwnLockfile(t *testing.T) {
	src := filepath.Join("..", "..", "site", "package-lock.json")
	if _, err := os.Stat(src); err != nil {
		t.Skip("site lockfile not present")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	pkgs := packagesFromPackageLock()
	if len(pkgs) == 0 {
		t.Fatal("parsed nothing from a real lockfileVersion 3 file")
	}
	t.Logf("resolved %d packages from the real lockfile; first: %s", len(pkgs), pkgs[0])
}
