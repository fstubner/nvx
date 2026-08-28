//go:build !windows

package main

// revokeSandboxReadExec has nothing to undo off Windows: read/execute roots are
// applied to a Landlock ruleset that exists only for the life of the process, so
// nothing is written to disk and nothing outlives the run. The ledger stays
// cross-platform so its tests and reconciliation logic are exercised everywhere.
func revokeSandboxReadExec(sidStr, path string) error { return nil }
