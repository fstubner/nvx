//go:build !windows

package nvx

import (
	"os"
	"testing"
)

// A lockfile that is there keeps its permissions through staging and back.
// Windows keeps no permission bits to keep, so this is not run there.
func TestStagingKeepsThePermissionsOfALockfileThatIsThere(t *testing.T) {
	root := tempDir(t)
	if err := os.WriteFile(lockfileAt(root), []byte(`{}`), 0o640); err != nil {
		t.Fatal(err)
	}
	s, ok := stageLockfile(lockfileAt(root), []byte(`{"staged":true}`))
	if !ok {
		t.Fatal("not staged")
	}
	info, _ := os.Stat(lockfileAt(root))
	if info.Mode().Perm() != 0o640 {
		t.Errorf("staged lockfile is %v, want 0640", info.Mode().Perm())
	}
	s.finish()
	info, _ = os.Stat(lockfileAt(root))
	if info.Mode().Perm() != 0o640 {
		t.Errorf("restored lockfile is %v, want 0640", info.Mode().Perm())
	}
}
