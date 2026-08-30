package main

// sandboxWritableRoots declares what a contained process may write.
//
// It exists because F22 was caused by two callers disagreeing: one granted the
// guest home and the working directory, the other also granted nvxHome and the
// runtime binary directory, and nothing in the type system or the test suite
// objected. Seatbelt and Landlock each apply this its own way -- profile text,
// path rules -- but they no longer decide the policy independently.
//
// Windows reads this too, but cannot share the loop: there the guest home is
// required and takes an integrity label while the working directory is
// best-effort and skipped at the profile root. So prepareAppContainerFilesystem
// grants that pair itself and REFUSES TO LAUNCH if this function names a root it
// does not implement -- fail closed, rather than silently containing less than
// the declaration says.
//
// That guard exists because the two really did drift. The header used to claim
// this was "the single declaration ... for every platform and every isolation
// provider" while Windows read none of it, and an acceptance pass proved it by
// widening this to include the working directory's parent: the unit tests went
// red and the real Windows containment probe stayed green. The same sabotage now
// stops the Windows launch with the offending path named.
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
