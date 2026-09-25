package nvx

import (
	"os"
	"strings"
	"testing"
)

// A reader that saw a damaged ledger must not move aside a good one written
// since. Reads take no lock, so a reader could read the damaged file, lose the
// CPU while updateProjectGrants (which holds the lock) moved it aside and saved
// a good ledger, and then rename that good ledger too: the permissions just
// recorded were untracked again. The quarantine now happens under the lock and
// only if the file still holds the bytes that failed to parse.
func TestAStaleReaderDoesNotQuarantineAGoodLedger(t *testing.T) {
	home := tempDir(t)
	scope := tempDir(t)
	if err := os.MkdirAll(grantsDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	path := grantsPath(home, scope)
	damaged := []byte("{ damaged")

	// The writer's turn: a good ledger is in place.
	if err := updateProjectGrants(home, scope, func(g *projectGrants) error {
		g.AllowHosts = []string{"registry.example:443"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The stale reader resumes with the damaged bytes it read earlier.
	quarantineUnreadableGrants(home, scope, path, damaged, os.ErrInvalid, false)

	if got := loadProjectGrants(home, scope); len(got.AllowHosts) != 1 {
		t.Fatalf("the good ledger was moved aside by a reader that had seen an older, damaged one: %+v", got)
	}
	entries, _ := os.ReadDir(grantsDir(home))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable") {
			t.Errorf("%s was created from a ledger that parses", e.Name())
		}
	}
}

// And the damaged ledger itself is still moved aside when it is what is there.
func TestADamagedLedgerIsStillKeptAside(t *testing.T) {
	home := tempDir(t)
	scope := tempDir(t)
	if err := os.MkdirAll(grantsDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grantsPath(home, scope), []byte("{ damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = loadProjectGrants(home, scope)
	entries, _ := os.ReadDir(grantsDir(home))
	kept := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable") {
			kept++
		}
	}
	if kept != 1 {
		t.Fatalf("kept %d unreadable record(s), want 1", kept)
	}
	if _, err := os.Stat(grantsPath(home, scope)); !os.IsNotExist(err) {
		t.Fatalf("the damaged ledger is still in place: %v", err)
	}
}
