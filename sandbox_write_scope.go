package main

// sandboxWritableRoots is the single declaration of what a contained process may
// write, for every platform and every isolation provider.
//
// It exists because F22 was caused by two callers disagreeing: one granted the
// guest home and the working directory, the other also granted nvxHome and the
// runtime binary directory, and nothing in the type system or the test suite
// objected. Each platform still applies this its own way -- Seatbelt profile text,
// AppContainer ACLs, Landlock path rules -- but they no longer each decide the
// policy independently.
//
// ~/.nvx is nvx's control plane: policy.json (the trust baseline every project
// policy is compared against), grants/ (policy pins, approved egress hosts,
// trusted tools), cache/ (command name to absolute path, later executed), and
// tool_home/ (other tools' persisted credentials). A contained process that can
// write any of it can arrange its own trust on the next run, so pinning binds only
// while this stays out of reach.
//
// The guest home legitimately lives *under* nvxHome (~/.nvx/sandbox_home/<session>)
// and is writable. That is fine and is the point: granting a subdirectory is not
// granting the root. Callers must never widen a root to its parent.
func sandboxWritableRoots(guestHome, workDir string) []string {
	roots := make([]string, 0, 2)
	if guestHome != "" {
		roots = append(roots, guestHome)
	}
	if workDir != "" {
		roots = append(roots, workDir)
	}
	return roots
}
