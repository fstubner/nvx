package main

import (
	"fmt"
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
	g := recordReadExecGrant(nil, sid, `C:\x`)
	g = recordReadExecGrant(g, sid, `C:\x`)
	g = recordReadExecGrant(g, sid, `C:\X\`) // same directory, spelled differently
	if len(g) != 1 {
		t.Fatalf("recorded %d entries for one directory: %+v", len(g), g)
	}
}

func TestResettingGrantsWithdrawsThemAll(t *testing.T) {
	const sid = "S-1-15-3-1024-eee"
	var revoked []string
	r, f := revokeAllReadExecGrants(
		[]readExecGrant{{Path: `C:\a`, SID: sid}, {Path: `C:\b`, SID: sid}},
		recordingRevoker(&revoked, map[string]bool{`C:\b`: true}))
	if r != 1 || f != 1 {
		t.Fatalf("revoked=%d failed=%d, want 1 and 1", r, f)
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
