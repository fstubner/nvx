package nvx

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// Concurrent updates to a project's grants ledger do not lose each other.
//
// The ledger is written atomically -- temp file and rename -- but it was read,
// modified and written back by three callers with nothing serialising them.
// Two nvx processes on one project at the same time are routine, not exotic: an
// npm lifecycle script runs nvx-shimmed commands, a test runner spawns workers
// through the shims, an agent runs two things at once. Each reads the ledger,
// adds its own entry, and writes the whole file, and the second write silently
// discards the first's entry. The entries are policy pins, approved egress
// hosts and the record of filesystem permissions nvx granted -- and a
// permission with no record is one nothing can withdraw.
//
// Sixteen writers, each adding one pin, with the window between read and write
// held open long enough for the race to be certain rather than occasional.
func TestConcurrentLedgerUpdatesDoNotLoseEntries(t *testing.T) {
	nvxHome := tempDir(t)
	scope := tempDir(t)
	const writers = 16

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := updateProjectGrants(nvxHome, scope, func(g *projectGrants) error {
				time.Sleep(20 * time.Millisecond) // hold the read-modify-write window open
				g.PolicyPins[fmt.Sprintf("pin-%02d", i)] = "hash"
				return nil
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("update failed: %v", err)
	}

	got := loadProjectGrants(nvxHome, scope)
	if len(got.PolicyPins) != writers {
		missing := []string{}
		for i := 0; i < writers; i++ {
			if _, ok := got.PolicyPins[fmt.Sprintf("pin-%02d", i)]; !ok {
				missing = append(missing, fmt.Sprintf("pin-%02d", i))
			}
		}
		t.Fatalf("%d of %d concurrent updates were lost: %v -- a later writer overwrote an earlier one's entry", len(missing), writers, missing)
	}
}

// An update must not save over a ledger it could not read. It read any failure
// as an empty ledger and wrote that back, dropping the record of every
// permission nvx had granted. Measured on Linux with the ledger at mode 000.
func TestAnUnreadableLedgerIsNotOverwritten(t *testing.T) {
	nvxHome := tempDir(t)
	scope := tempDir(t)
	if err := updateProjectGrants(nvxHome, scope, func(g *projectGrants) error {
		g.ReadExecGrants = append(g.ReadExecGrants, readExecGrant{Path: `C:\tools`, SID: "S-1-15-3-1024-1"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	prev := readLedgerFile
	readLedgerFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	err := updateProjectGrants(nvxHome, scope, func(g *projectGrants) error {
		g.PolicyPins["p"] = "hash"
		return nil
	})
	readLedgerFile = prev
	if err == nil {
		t.Error("the update saved over a ledger it could not read")
	}
	if got := loadProjectGrants(nvxHome, scope); len(got.ReadExecGrants) != 1 {
		t.Fatalf("read_exec_grants after = %d, want the 1 recorded before", len(got.ReadExecGrants))
	}
}
