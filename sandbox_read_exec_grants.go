package main

import (
	"errors"
	"os"
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

// osStat is os.Stat, named so tests can reason about the check without a real
// filesystem.
var osStat = os.Stat

// errPermissionBroadened says the permission on a recorded path is wider than the
// read/execute entry nvx recorded, so removing it here would take away more than
// this feature granted.
//
// Withdrawing takes an identity's whole entry, not one right from it. A record
// naming a path whose entry is something broader -- a modify grant written for a
// writable root, say -- must therefore never be acted on: removing it would
// delete write access nvx granted for a different reason. A ledger can name such
// a path through a bug or a hand edit, and one did: a project directory that was
// both the sandbox's writable root and named in allow_read_exec was recorded as a
// read/execute grant, and `nvx grants reset` deleted the sandbox's write access
// to the user's own project while reporting it had withdrawn a read/execute
// permission.
//
// Checked here rather than only where records are written, because the wrong
// records already exist on disk.
var errPermissionBroadened = errors.New("the permission on this path is broader than the one nvx recorded")

// errNothingToWithdraw says there is no permission for this identity on the path
// at all, so the record is stale and nothing was removed. Distinguished from a
// real withdrawal because reporting "Withdrew 1 permission" having removed
// nothing is a claim about the filesystem that is not true.
var errNothingToWithdraw = errors.New("no permission for this identity on this path")

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
func reconcileReadExecGrants(existing []readExecGrant, wanted []string, capSIDs []string, revoke func(sid, path string) error) (keep, revoked []readExecGrant) {
	keep = make([]readExecGrant, 0, len(existing))
	for _, g := range existing {
		if grantStillWanted(g, wanted, capSIDs) {
			keep = append(keep, g)
			continue
		}
		if err := revoke(g.SID, g.Path); err != nil {
			if errors.Is(err, errNothingToWithdraw) {
				// Nothing on disk to remove; stop tracking it, quietly.
				revoked = append(revoked, g)
				continue
			}
			if errors.Is(err, errPermissionBroadened) {
				// Left in place, and the record retired: what is there now is a
				// wider grant nvx maintains for another reason, and removing it
				// would take that away too.
				LogWarn("Left the permission on %s in place: it is now wider than the read/execute one nvx recorded, so withdrawing it here would remove more than this policy granted.", g.Path)
				LogInfo("The sandbox keeps that access through the wider grant; nvx has stopped tracking it as a read/execute permission.")
				revoked = append(revoked, g)
				continue
			}
			LogWarn("Could not withdraw the sandbox's read access to %s: %v", g.Path, err)
			keep = append(keep, g)
			continue
		}
		LogInfo("Withdrew the sandbox's read access to %s (no longer in the policy).", g.Path)
		revoked = append(revoked, g)
	}
	return keep, revoked
}

// mergeLedgerForSave folds this run's ledger together with whatever is on disk
// now, so a concurrent run in the same project cannot erase the other's records.
//
// Two nvx runs in one project each load the ledger, change it, and write it back;
// the later write would otherwise drop whatever the earlier one added. That is
// only untidy for most of what this file records, but an unrecorded grant is the
// one state this feature exists to prevent: the permission is on disk and nothing
// -- not reconciliation, not `grants reset` -- knows to take it back.
//
// Entries this run revoked are excluded, or a stale copy on disk would resurrect
// a permission that has just been removed. Everything else is kept, erring toward
// recording: a record with no permission behind it costs one no-op revoke, while a
// permission with no record is invisible.
func mergeLedgerForSave(ours, stored, revoked []readExecGrant) []readExecGrant {
	out := append([]readExecGrant{}, ours...)
	for _, s := range stored {
		if containsGrant(revoked, s) || containsGrant(out, s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func containsGrant(in []readExecGrant, g readExecGrant) bool {
	for _, c := range in {
		if strings.EqualFold(c.SID, g.SID) && sameGrantPath(c.Path, g.Path) {
			return true
		}
	}
	return false
}

// grantStillWanted reports whether a recorded grant is one the current policy
// asks for.
//
// A grant to a capability this run is not carrying is left alone rather than
// treated as unwanted: the ledger is per project, but a stale or hand-edited
// entry naming some other identity is not this run's to revoke.
// A consequence worth stating, because it looks like a leak and is not.
//
// The identity is derived from the project root, so anything that moves that root
// -- adding or removing a package.json above the working directory, moving the
// project -- gives later runs a different identity, and the grants made under the
// old one stop matching any capability this run carries. They are kept, and this
// run reconciles only its own.
//
// Measured 2026-08-28: adding a package.json produced a second permission on the
// same directory, and dropping the policy withdrew only the current scope's. The
// other one sits on disk unreconciled.
//
// It cannot be used while stale, which is what keeps it out of the containment
// story: the permission admits only a sandbox carrying that old identity, and such
// a run is one rooted at that old scope -- which loads that scope's ledger and
// reconciles it before the contained process starts. Verified by reverting the
// scope with the policy emptied: the permission was withdrawn and the sandbox got
// EPERM in the same launch. `nvx grants reset --all` walks every ledger and clears
// them regardless.
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

// planReadExecRecords returns the ledger to write before any permission is
// granted: every root this run will own, and nothing else.
//
// The ownership check is made here rather than passed in. An earlier version took
// it as an argument so a test could answer it, which tested the loop but left the
// wiring open -- handing it a function that always agrees restored the defect with
// the suite green. Calling it directly means unwiring the guard now takes deleting
// this function's body rather than flipping one argument, and the tests below
// drive it against real permissions.
//
// The check has to happen before anything is written, because the record is
// written before the permission is granted: a permission nvx cannot record is one
// it could never withdraw.
func planReadExecRecords(existing []readExecGrant, roots, capSIDs []string) []readExecGrant {
	out := existing
	for _, root := range roots {
		for _, sid := range capSIDs {
			// A root already carrying a broader permission is not nvx's to take
			// back, so recording it would let a later withdrawal delete access
			// granted for another reason.
			if !readExecGrantWouldBeOurs(sid, root) {
				continue
			}
			out = recordReadExecGrant(out, sid, root)
		}
	}
	return out
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
//
// pathExists lets the caller decide what a vanished directory means. On a normal
// run it is a failure worth keeping the record for -- the directory may have been
// renamed, in which case the permission moved with it and the record is the only
// thing still naming it. On an explicit reset it is not: the user has asked to
// clear this state, nvx can do nothing more about that path, and refusing for ever
// would leave `grants reset` permanently unable to finish.
// revokeOutcome is what a reset achieved, split by what the caller must do about
// it. Four counters that always travel together and were previously returned as
// loose ints; naming them is what makes "finished but not clean" expressible.
type revokeOutcome struct {
	// Revoked was withdrawn and the removal confirmed by reading the ACL back.
	Revoked int
	// Failed could not be withdrawn and may work later. The record is KEPT so a
	// later reset retries.
	Failed int
	// Stranded had no directory at the recorded path. The record is dropped -- see
	// revokeAllReadExecGrantsWithin -- so the reset finishes.
	Stranded int
	// Broadened was left in place on purpose: the permission there is wider than
	// the read/execute one nvx recorded, so nvx will not touch it. The record is
	// dropped with the others.
	Broadened int
}

// Unaccounted is the count of permissions that may still be on disk with nothing
// left tracking them. Stranded and Broadened both drop their record, so this is
// the caller's last chance to say so.
func (o revokeOutcome) Unaccounted() int { return o.Stranded + o.Broadened }

func revokeAllReadExecGrants(grants []readExecGrant, revoke func(sid, path string) error) revokeOutcome {
	return revokeAllReadExecGrantsWithin(grants, revoke, func(p string) bool {
		_, err := osStat(p)
		return err == nil
	})
}

// revokeAllReadExecGrantsWithin returns three counts, not two.
//
// stranded is a directory that is no longer at its recorded path. That case is
// deliberately NOT counted as failed: failed keeps the record so a later reset
// can retry, and a vanished directory would keep the record for ever, leaving
// `nvx grants reset` permanently unable to complete. That reasoning is older than
// this comment and still holds.
//
// What it left out is that a reset then deleted the record and printed success at
// exit 0, having withdrawn nothing -- and an access-control entry travels with a
// renamed directory, so the permission may well still be in force somewhere nvx
// can no longer name. An acceptance pass demonstrated it: rename the directory,
// reset, and the entry is still on disk under the new name with the ledger gone.
//
// Counting it separately lets the caller do both things that are true at once:
// finish the reset, and refuse to call it a success. The SID and old path go to
// the terminal, because after this the ledger no longer has them.
func revokeAllReadExecGrantsWithin(grants []readExecGrant, revoke func(sid, path string) error, pathExists func(string) bool) revokeOutcome {
	var out revokeOutcome
	for _, g := range grants {
		if !pathExists(g.Path) {
			LogWarn("%s no longer exists, so its permission could not be withdrawn.", g.Path)
			LogInfo("If that directory was renamed rather than deleted, the permission moved with it and is still in force. "+
				"Remove it there with: icacls \"<new path>\" /remove:g *%s", g.SID)
			out.Stranded++
			continue
		}
		if err := revoke(g.SID, g.Path); err != nil {
			if errors.Is(err, errNothingToWithdraw) {
				continue // nothing was there; nothing was removed
			}
			if errors.Is(err, errPermissionBroadened) {
				// Counted, not merely warned about.
				//
				// This branch sat three lines below the stranded one and had the same
				// defect it was just fixed for: it warned that a permission was being
				// left on disk, returned nothing, and the caller deleted the record and
				// exited 0. An acceptance pass found it immediately afterwards.
				//
				// Not Failed, for the same reason Stranded is not: retrying cannot help.
				// nvx refuses to remove an entry wider than the one it recorded, so a
				// later reset would refuse identically and the record would be kept for
				// ever. It is dropped, and reported.
				LogWarn("Left the permission on %s in place: it is now wider than the read/execute one nvx recorded.", g.Path)
				LogInfo("nvx only withdraws the exact entry it granted. Remove it yourself with: icacls %q /remove:g *%s", g.Path, g.SID)
				out.Broadened++
				continue
			}
			LogWarn("Could not withdraw the sandbox's read access to %s: %v", g.Path, err)
			out.Failed++
			continue
		}
		out.Revoked++
	}
	return out
}
