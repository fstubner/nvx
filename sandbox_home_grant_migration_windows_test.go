//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The bug was that "is there an ACE" was used where "is it the RIGHT ACE" was
// meant, so a pre-0.5.0 (RX) on ~/.nvx was skipped as already granted and the
// sandbox could list nvx's own home forever. These pin the distinction.
func TestTraverseOnlyGrantIsDistinguishedFromLegacyReadExec(t *testing.T) {
	cases := []struct {
		rights string
		want   bool
	}{
		{"X,RA", true},
		{"RA,X", true},  // icacls prints these in either order
		{"X, RA", true}, // and sometimes with a space
		{"RX", false},   // the legacy grant: permits listing
		{"R", false},
		{"M", false},
		{"F", false},
		{"", false},
		{"X", false},  // traverse without read-attributes is not what we write
		{"RA", false}, // nor the reverse
		{"X,RA,W", false},
	}
	for _, tc := range cases {
		if got := aceIsTraverseOnly(tc.rights); got != tc.want {
			t.Errorf("aceIsTraverseOnly(%q) = %v, want %v", tc.rights, got, tc.want)
		}
	}
}

func TestAppContainerAceRightsReadsTheRightsNotJustPresence(t *testing.T) {
	// A real `icacls` listing: the target SID, a similar capability SID, an
	// inherited ACE, and a deny.
	const sid = "S-1-15-2-125897231-4118270468-3890225265-1944594370-665964903-770884402-3722446281"
	dir := t.TempDir()

	// Nothing granted yet: no ACE for this SID.
	rights, err := appContainerAceRights(sid, dir)
	if err != nil {
		t.Fatalf("appContainerAceRights on a fresh dir: %v", err)
	}
	if rights != "" {
		t.Errorf("fresh directory reported rights %q, want none", rights)
	}

	// Grant the legacy shape and confirm it is reported as (RX), not merely
	// "present" -- that difference is the whole fix.
	if out, err := runWinCmd(20*time.Second, "icacls", dir, "/grant", "*"+sid+":(RX)", "/c", "/q"); err != nil {
		t.Skipf("cannot set an AppContainer ACE here: %v (%s)", err, out)
	}
	rights, err = appContainerAceRights(sid, dir)
	if err != nil {
		t.Fatalf("appContainerAceRights after granting RX: %v", err)
	}
	if rights != "RX" {
		t.Fatalf("granted (RX), read back %q", rights)
	}
	if aceIsTraverseOnly(rights) {
		t.Error("the legacy (RX) grant was accepted as traverse-only; it permits listing")
	}

	// Narrowing must leave a traverse-only ACE behind.
	if err := replaceAceWithTraverseOnly(sid, dir); err != nil {
		t.Fatalf("replaceAceWithTraverseOnly: %v", err)
	}
	rights, err = appContainerAceRights(sid, dir)
	if err != nil {
		t.Fatalf("appContainerAceRights after narrowing: %v", err)
	}
	if !aceIsTraverseOnly(rights) {
		t.Errorf("after narrowing, rights are %q, want traverse-only (X,RA)", rights)
	}
}

// The migration must run once and then stop, or it pays two icacls calls on
// every launch to re-answer a question whose answer cannot change.
func TestHomeGrantMigrationRunsOnce(t *testing.T) {
	const sid = "S-1-15-2-1-2-3-4-5-6-7"
	home := t.TempDir()
	marker := homeGrantMigrationMarker(home)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker exists before the migration ran")
	}
	narrowLegacyHomeGrant(sid, home)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no marker after the migration ran: %v", err)
	}

	// A second call must be a no-op even if the ACL changed underneath, which is
	// what "once per home" means.
	before, _ := os.ReadFile(marker)
	if err := os.WriteFile(filepath.Join(home, "sentinel"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	narrowLegacyHomeGrant(sid, home)
	after, _ := os.ReadFile(marker)
	if string(before) != string(after) {
		t.Errorf("second run rewrote the marker (%q -> %q); it should have returned early", before, after)
	}
}
