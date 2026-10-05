package nvx

import (
	"os"
	"testing"
)

// `grants reset --all` succeeds after grants were recorded.
//
// Every ledger update leaves a lock file beside the ledger, and reset --all read
// every file in the directory as a ledger. The lock did not parse, so the first
// grant ever recorded made every later reset fail, exit 1, and tell the user to
// remove directory permissions with icacls by hand.
func TestResetAllIgnoresTheLedgerLockFiles(t *testing.T) {
	home, scope := t.TempDir(), t.TempDir()
	if err := updateProjectGrants(home, scope, func(g *projectGrants) error {
		g.TrustedTools = append(g.TrustedTools, "wrangler")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(grantsPath(home, scope) + grantsLockSuffix); err != nil {
		t.Fatalf("test setup: expected the update to leave its lock file: %v", err)
	}

	var code int
	captureStdout(t, func() { code = runGrants([]string{"reset", "--all"}, home) })
	if code != 0 {
		t.Fatalf("reset --all exited %d after one recorded grant; the ledger's lock file was read as a ledger", code)
	}
	if _, err := os.Stat(grantsPath(home, scope)); !os.IsNotExist(err) {
		t.Errorf("the ledger survived reset --all: %v", err)
	}
}

// The single-project reset still answers "nothing recorded" cleanly when no
// grants directory exists yet, now that it takes the ledger's lock first.
func TestResetWithNoGrantsIsClean(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	var code int
	captureStdout(t, func() { code = runGrants([]string{"reset"}, home) })
	if code != 0 {
		t.Errorf("reset with nothing recorded exited %d", code)
	}
}
