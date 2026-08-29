//go:build !windows

package main

// revokeSandboxReadExec has nothing to undo off Windows: read/execute roots are
// applied to a Landlock ruleset that exists only for the life of the process, so
// nothing is written to disk and nothing outlives the run. The ledger stays
// cross-platform so its tests and reconciliation logic are exercised everywhere.
func revokeSandboxReadExec(sidStr, path string) error { return nil }

// readExecGrantWouldBeOurs has nothing to decide off Windows: read/execute roots
// go into a Landlock ruleset that lives only as long as the process, so there is
// no permission on disk to own or mistake for someone else's.
func readExecGrantWouldBeOurs(sidStr, path string) bool { return true }
