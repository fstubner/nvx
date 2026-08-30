package main

// sandboxWritableRoots declares what a contained process may write.
//
// It exists because F22 was caused by two callers disagreeing: one granted the
// guest home and the working directory, the other also granted nvxHome and the
// runtime binary directory, and nothing in the type system or the test suite
// objected. Seatbelt and Landlock each apply this its own way -- profile text,
// path rules -- but they no longer decide the policy independently.
//
// **Windows does not call this**, and the header used to claim it did ("the
// single declaration ... for every platform and every isolation provider").
// prepareAppContainerFilesystem grants the same pair from its own code, and its
// comment names this function without calling it. An acceptance pass proved the
// gap by widening this to include the working directory's parent: the unit tests
// went red, and the real Windows containment probe stayed green, because nothing
// on that path reads this. So the F22 shape -- two callers disagreeing -- is
// prevented on two platforms of three, and the third is guarded only by its own
// separate tests. Said here rather than left as a claim that reads as broader
// than it is.
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
