package nvx

import (
	"fmt"
	"os"
)

// lockProjectGrants takes the lock that serialises changes to one project's
// ledger, across processes. The lock is a sibling file of the ledger, so the
// ledger's own atomic rename is never disturbed by it.
func lockProjectGrants(nvxHome, scope string) (unlock func(), err error) {
	if err := os.MkdirAll(grantsDir(nvxHome), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(grantsPath(nvxHome, scope)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}

// updateProjectGrants is the one way to change a project's grants ledger:
// load, apply fn, save, under the project's lock.
//
// saveProjectGrants is atomic on its own -- temp file and rename -- but the
// read-modify-write around it was not serialised, and the three callers each
// loaded the ledger, changed one thing and wrote the whole file back. Two nvx
// processes on one project at once are routine: an npm lifecycle script runs
// shimmed commands, a test runner spawns workers through the shims, an agent
// runs two things at once. The second writer silently discarded the first's
// entry. The entries are policy pins, approved egress hosts and the record of
// filesystem permissions nvx granted, and a permission with no record is one
// nothing can withdraw.
func updateProjectGrants(nvxHome, scope string, fn func(g *projectGrants) error) error {
	if nvxHome == "" || scope == "" {
		return fmt.Errorf("grants ledger: no home or project scope to record against")
	}
	unlock, err := lockProjectGrants(nvxHome, scope)
	if err != nil {
		return fmt.Errorf("lock the grants ledger: %w", err)
	}
	defer unlock()

	g := loadProjectGrants(nvxHome, scope)
	if err := fn(&g); err != nil {
		return err
	}
	if g.ProjectPath == "" {
		g.ProjectPath = scope
	}
	return saveProjectGrants(nvxHome, g)
}
