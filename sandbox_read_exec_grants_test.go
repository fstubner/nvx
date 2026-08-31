package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// recordingRevoker stands in for icacls.
func recordingRevoker(calls *[]string, fail map[string]bool) func(string, string) error {
	return func(sid, path string) error {
		if fail[path] {
			return fmt.Errorf("simulated failure")
		}
		*calls = append(*calls, sid+" "+path)
		return nil
	}
}

// The point of the ledger: a grant the policy no longer asks for is taken back.
// Before this, deleting the allow_read_exec entry left the filesystem permission
// in place with nothing in the product able to remove it.
func TestAGrantIsWithdrawnOnceThePolicyStopsAskingForIt(t *testing.T) {
	const sid = "S-1-15-3-1024-aaa"
	existing := []readExecGrant{
		{Path: `C:\browsers`, SID: sid},
		{Path: `C:\tools`, SID: sid},
	}
	var revoked []string
	kept, _ := reconcileReadExecGrants(existing, []string{`C:\browsers`}, []string{sid}, recordingRevoker(&revoked, nil))

	if len(revoked) != 1 || revoked[0] != sid+` C:\tools` {
		t.Fatalf("withdrawn = %v, want only C:\tools", revoked)
	}
	if len(kept) != 1 || kept[0].Path != `C:\browsers` {
		t.Fatalf("ledger = %+v, want just the root still in the policy", kept)
	}
}

func TestRemovingThePolicyEntirelyWithdrawsEverythingItGranted(t *testing.T) {
	const sid = "S-1-15-3-1024-bbb"
	existing := []readExecGrant{{Path: `C:\a`, SID: sid}, {Path: `C:\b`, SID: sid}}
	var revoked []string
	kept, _ := reconcileReadExecGrants(existing, nil, []string{sid}, recordingRevoker(&revoked, nil))

	if len(revoked) != 2 {
		t.Fatalf("withdrew %d of 2 grants: %v", len(revoked), revoked)
	}
	if len(kept) != 0 {
		t.Fatalf("ledger still holds %+v after the policy was emptied", kept)
	}
}

// A path that cannot be revoked -- deleted, moved, permissions changed -- must
// stay in the ledger. Dropping it would lose the only record that the permission
// exists, which is exactly the state this feature exists to prevent.
func TestAnUnrevokableGrantStaysOnTheBooks(t *testing.T) {
	const sid = "S-1-15-3-1024-ccc"
	existing := []readExecGrant{{Path: `C:\gone`, SID: sid}}
	var revoked []string
	kept, _ := reconcileReadExecGrants(existing, nil, []string{sid},
		recordingRevoker(&revoked, map[string]bool{`C:\gone`: true}))

	if len(kept) != 1 {
		t.Fatal("a grant that could not be withdrawn was dropped from the ledger; nothing would ever retry it")
	}
}

// The ledger is per project but the identity is what authorises removal. An entry
// naming another project's identity is not this run's to revoke.
func TestAnotherIdentitysGrantIsLeftAlone(t *testing.T) {
	var revoked []string
	kept, _ := reconcileReadExecGrants(
		[]readExecGrant{{Path: `C:\shared`, SID: "S-1-15-3-1024-theirs"}},
		nil, []string{"S-1-15-3-1024-ours"}, recordingRevoker(&revoked, nil))

	if len(revoked) != 0 {
		t.Fatalf("revoked another identity's grant: %v", revoked)
	}
	if len(kept) != 1 {
		t.Fatal("another identity's grant was dropped from the ledger")
	}
}

func TestAGrantIsRecordedOnceHoweverOftenItIsSeen(t *testing.T) {
	const sid = "S-1-15-3-1024-ddd"
	// Paths built for the running platform. Hardcoded Windows paths passed here on
	// Windows and failed the Linux and macOS CI jobs for thirteen commits:
	// sameGrantPath normalises with filepath.Clean, and a backslash is an ordinary
	// character off Windows, so "C:\X\" and "C:\x" never converged there.
	dir := filepath.Join("some", "granted", "dir")
	g := recordReadExecGrant(nil, sid, dir)
	g = recordReadExecGrant(g, sid, dir)
	// The same directory spelled differently: a redundant element and a trailing
	// separator, both of which Clean removes on any platform.
	g = recordReadExecGrant(g, sid,
		filepath.Join("some", "granted", "extra", "..", "dir")+string(filepath.Separator))
	if len(g) != 1 {
		t.Fatalf("recorded %d entries for one directory: %+v", len(g), g)
	}
}

func TestResettingGrantsWithdrawsThemAll(t *testing.T) {
	const sid = "S-1-15-3-1024-eee"
	var revoked []string
	allExist := func(string) bool { return true }
	out := revokeAllReadExecGrantsWithin(
		[]readExecGrant{{Path: `C:\a`, SID: sid}, {Path: `C:\b`, SID: sid}},
		recordingRevoker(&revoked, map[string]bool{`C:\b`: true}), allExist)
	if out.Revoked != 1 || out.Failed != 1 || out.Unaccounted() != 0 {
		t.Fatalf("%+v, want Revoked 1, Failed 1, Unaccounted 0", out)
	}
}

// An explicit reset must be able to finish even when a recorded directory has
// vanished. On a normal run that is a failure worth keeping the record for -- the
// directory may have been renamed, taking the permission with it -- but a reset
// is the user asking to clear this state, and refusing for ever would leave the
// command permanently unable to complete.
//
// So it is still not counted as failed, and the record is still dropped. What was
// missing is the other half: it is not a success either, and it used to be
// reported as one. The vanished entry now comes back as `stranded`, which is what
// the caller uses to finish the reset and still exit non-zero.
func TestResettingSkipsRecordsWhoseDirectoryIsGone(t *testing.T) {
	const sid = "S-1-15-3-1024-fff"
	var revoked []string
	gone := func(string) bool { return false }
	out := revokeAllReadExecGrantsWithin(
		[]readExecGrant{{Path: `C:\vanished`, SID: sid}},
		recordingRevoker(&revoked, nil), gone)

	if len(revoked) != 0 {
		t.Fatalf("tried to withdraw from a path that does not exist: %v", revoked)
	}
	if out.Failed != 0 {
		t.Fatalf("Failed=%d; a vanished directory would block the reset for ever", out.Failed)
	}
	if out.Revoked != 0 {
		t.Fatalf("Revoked=%d; nothing was withdrawn, so nothing should be counted", out.Revoked)
	}
	if out.Stranded != 1 {
		t.Fatalf("Stranded=%d, want 1; without it the caller cannot tell this run apart "+
			"from one that withdrew everything, and prints a tick at exit 0", out.Stranded)
	}
}

// A mixture: one withdrawn, one refused, one vanished, one widened since nvx
// granted it. Each has a different consequence for the caller -- keep the record,
// drop it, exit code -- so they must not be conflated into a single "something
// went wrong" count.
func TestRevokeAccountingKeepsTheFourOutcomesApart(t *testing.T) {
	const sid = "S-1-15-3-1024-abc"
	var revoked []string
	exists := func(p string) bool { return p != `C:\vanished` }

	revoker := func(sidStr, path string) error {
		switch path {
		case `C:\refused`:
			return errors.New("access denied")
		case `C:\widened`:
			return errPermissionBroadened
		}
		revoked = append(revoked, path)
		return nil
	}

	out := revokeAllReadExecGrantsWithin(
		[]readExecGrant{
			{Path: `C:\ok`, SID: sid},
			{Path: `C:\refused`, SID: sid},
			{Path: `C:\vanished`, SID: sid},
			{Path: `C:\widened`, SID: sid},
		}, revoker, exists)

	if out.Revoked != 1 || out.Failed != 1 || out.Stranded != 1 || out.Broadened != 1 {
		t.Fatalf("%+v, want one of each", out)
	}
	// Stranded and Broadened both drop their record, so both must reach the
	// caller's "finished, but something is unaccounted for" branch.
	if out.Unaccounted() != 2 {
		t.Fatalf("Unaccounted()=%d, want 2; a permission left on disk with its record "+
			"deleted has to reach the exit code", out.Unaccounted())
	}
	// The vanished path must never be handed to the revoker: on Windows that call
	// is what reports success having removed nothing.
	for _, p := range revoked {
		if p == `C:\vanished` {
			t.Error("attempted to withdraw from a path that does not exist")
		}
	}
}

// Two runs in one project each load the ledger, change it, and write it back.
// The later write must not erase what the earlier one recorded: a permission with
// no record behind it is invisible to both reconciliation and `grants reset`,
// which is the one state this ledger exists to prevent.
func TestAConcurrentRunsRecordIsNotErased(t *testing.T) {
	const sid = "S-1-15-3-1024-fff"
	ours := []readExecGrant{{Path: `C:\mine`, SID: sid}}
	storedByTheOtherRun := []readExecGrant{{Path: `C:\theirs`, SID: sid}}

	merged := mergeLedgerForSave(ours, storedByTheOtherRun, nil)
	if !containsGrant(merged, readExecGrant{Path: `C:\theirs`, SID: sid}) {
		t.Fatal("the other run's record was erased; its permission would be left with nothing tracking it")
	}
	if !containsGrant(merged, readExecGrant{Path: `C:\mine`, SID: sid}) {
		t.Fatal("this run's own record was lost")
	}
}

// ...but a stale copy on disk must not resurrect a permission this run just
// withdrew, or the record would outlive the thing it records.
func TestAWithdrawnGrantIsNotResurrectedByAStaleLedger(t *testing.T) {
	const sid = "S-1-15-3-1024-ggg"
	gone := readExecGrant{Path: `C:\gone`, SID: sid}

	merged := mergeLedgerForSave(nil, []readExecGrant{gone}, []readExecGrant{gone})
	if containsGrant(merged, gone) {
		t.Fatal("a withdrawn grant came back from the stored ledger")
	}
}

// reconcile reports what it withdrew, which is what makes the exclusion above
// possible.
func TestReconcileReportsWhatItWithdrew(t *testing.T) {
	const sid = "S-1-15-3-1024-hhh"
	var calls []string
	keep, revoked := reconcileReadExecGrants(
		[]readExecGrant{{Path: `C:\a`, SID: sid}, {Path: `C:\b`, SID: sid}},
		[]string{`C:\a`}, []string{sid}, recordingRevoker(&calls, nil))

	if len(keep) != 1 || keep[0].Path != `C:\a` {
		t.Fatalf("kept = %+v, want only C:\a", keep)
	}
	if len(revoked) != 1 || revoked[0].Path != `C:\b` {
		t.Fatalf("reported withdrawn = %+v, want C:\b", revoked)
	}
}
