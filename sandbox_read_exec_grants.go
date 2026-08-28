package main

import (
	"path/filepath"
	"strings"
)

// Reclaiming the read/execute grants nvx hands out.
//
// A grant from allow_read_exec is an access-control entry written onto a
// directory that nvx does not own. It has to persist between runs -- re-applying
// it every launch would mean an icacls call per root on the startup path, which
// is the exact cost the grant cache exists to avoid -- so it outlives the process
// that made it.
//
// What it must NOT do is outlive the policy that asked for it. Before this,
// deleting the allow_read_exec entry, or the whole policy file, left the ACE in
// place with nothing in the product able to remove it; the documented recovery
// was to work out the capability SID and run icacls by hand. A security tool that
// widens access and cannot narrow it again is keeping a promise it did not make.
//
// So every grant is recorded, and the record is reconciled against the policy on
// each run: anything nvx granted that the policy no longer asks for is revoked.
// `nvx grants reset` revokes them too, rather than deleting the ledger and
// orphaning the entries it was tracking.
//
// The ledger lives with the other project grants under nvx's home, outside the
// project tree -- code running inside the sandbox can write the working directory,
// and must not be able to edit the record of what it was granted.

// readExecGrant is one access-control entry nvx wrote, and can therefore remove.
type readExecGrant struct {
	// Path is the directory that carries the entry.
	Path string `json:"path"`
	// SID is the identity it was granted to -- this project's capability, so the
	// same directory granted by two projects has two records and losing one does
	// not revoke the other's access.
	SID string `json:"sid"`
}

func sameGrantPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// reconcileReadExecGrants revokes every recorded grant for this project that
// `wanted` no longer includes, and returns the ledger that should be stored.
//
// Revoking is best-effort per entry: a directory that has been deleted, moved, or
// made unreadable cannot be revoked, and one such entry must not strand the
// others or fail a command the user is waiting on. An entry that could not be
// revoked stays in the ledger so the next run tries again -- dropping it would
// lose the only record that the grant exists.
func reconcileReadExecGrants(existing []readExecGrant, wanted []string, capSIDs []string, revoke func(sid, path string) error) []readExecGrant {
	keep := make([]readExecGrant, 0, len(existing))
	for _, g := range existing {
		if grantStillWanted(g, wanted, capSIDs) {
			keep = append(keep, g)
			continue
		}
		if err := revoke(g.SID, g.Path); err != nil {
			LogWarn("Could not withdraw the sandbox's read access to %q: %v", g.Path, err)
			keep = append(keep, g)
			continue
		}
		LogInfo("Withdrew the sandbox's read access to %s (no longer in the policy).", g.Path)
	}
	return keep
}

// grantStillWanted reports whether a recorded grant is one the current policy
// asks for.
//
// A grant to a capability this run is not carrying is left alone rather than
// treated as unwanted: the ledger is per project, but a stale or hand-edited
// entry naming some other identity is not this run's to revoke.
func grantStillWanted(g readExecGrant, wanted []string, capSIDs []string) bool {
	ours := false
	for _, sid := range capSIDs {
		if strings.EqualFold(sid, g.SID) {
			ours = true
			break
		}
	}
	if !ours {
		return true
	}
	for _, w := range wanted {
		if sameGrantPath(w, g.Path) {
			return true
		}
	}
	return false
}

// recordReadExecGrant adds a grant to the ledger if it is not already there.
func recordReadExecGrant(existing []readExecGrant, sid, path string) []readExecGrant {
	for _, g := range existing {
		if strings.EqualFold(g.SID, sid) && sameGrantPath(g.Path, path) {
			return existing
		}
	}
	return append(existing, readExecGrant{Path: filepath.Clean(path), SID: sid})
}

// revokeAllReadExecGrants withdraws every grant in a ledger, for `nvx grants
// reset`. Returns how many were withdrawn and how many could not be.
func revokeAllReadExecGrants(grants []readExecGrant, revoke func(sid, path string) error) (revoked, failed int) {
	for _, g := range grants {
		if err := revoke(g.SID, g.Path); err != nil {
			LogWarn("Could not withdraw the sandbox's read access to %q: %v", g.Path, err)
			failed++
			continue
		}
		revoked++
	}
	return revoked, failed
}
