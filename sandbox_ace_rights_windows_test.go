//go:build windows

package main

import "testing"

// Matching an ACE by SID alone reported the current design's own ancestor grant
// as a pre-0.5.0 leftover: `grantAppContainerPathReadExec` writes (X,RA) --
// traverse and read-attributes -- on the directories above a sandbox, so running
// nvx from a subdirectory leaves one on the project root. doctor announced it as
// a grant letting "any nvx sandbox read and write this project", which is false in
// every clause, and offered to remove it. The same scan drives the launch-path
// cleanup, so the bad match would have revoked a grant nvx had just written.
//
// The distinction is the access mask, so that is what these pin.
func TestAceRightsDistinguishTraverseFromRealAccess(t *testing.T) {
	cases := []struct {
		rights string
		stale  bool
		why    string
	}{
		// What the current design writes on ancestors. Never stale.
		{"(X,RA)", false, "the ancestor traverse grant nvx writes today"},
		{"(RA,X)", false, "icacls prints the pair in either order"},
		{"(X, RA)", false, "and sometimes with a space"},

		// What a pre-0.5.0 nvx left behind. Always stale.
		{"(OI)(CI)(M)", true, "the legacy inheritable modify grant"},
		{"(OI)(CI)(F)", true, "full control"},
		{"(M)", true, "modify without inheritance"},
		{"(RX)", true, "read+execute lets another sandbox LIST the project"},
		{"(R)", true, "read"},
		{"(W)", true, "write"},

		// Anything ADDED to the traverse pair is stale...
		{"(X,RA,RD)", true, "adds read-data, which is a real read of the project"},

		// ...but a strict subset of it is not. Neither of these is a grant nvx
		// writes, and neither permits reading contents or writing, so neither is
		// what this check hunts. The check exists to find grants letting another
		// sandbox read or write a project; reporting one that does neither is the
		// false-positive class this whole change is fixing.
		{"(X)", false, "traverse alone: cannot read or write anything"},
		{"(RA)", false, "read-attributes alone: metadata, not contents"},

		// Unreadable rights stay quiet: this drives both a security claim shown to
		// the user and a removal, and asserting either from an ACE we could not
		// parse is exactly how the false positive happened.
		{"", false, "no rights text at all"},
		{"(OI)(CI)", false, "inheritance flags only, no access rights"},
	}

	for _, tc := range cases {
		if got := aceGrantsMoreThanTraverse(tc.rights); got != tc.stale {
			t.Errorf("aceGrantsMoreThanTraverse(%q) = %v, want %v -- %s", tc.rights, got, tc.stale, tc.why)
		}
	}
}

func TestRightsAfterSIDExtractsTheMask(t *testing.T) {
	const sid = "S-1-15-2-1-2-3-4-5-6-7"
	cases := []struct{ line, want string }{
		{`C:\proj ` + sid + `:(X,RA)`, "(X,RA)"},
		{`        ` + sid + `:(OI)(CI)(M)`, "(OI)(CI)(M)"},
		{`        ` + sid + `:(M)   `, "(M)"},
		{`        NT AUTHORITY\SYSTEM:(F)`, ""},
		{``, ""},
	}
	for _, tc := range cases {
		if got := rightsAfterSID(tc.line, sid); got != tc.want {
			t.Errorf("rightsAfterSID(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}
