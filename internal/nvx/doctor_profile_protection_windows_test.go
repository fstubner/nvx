//go:build windows

package nvx

import (
	"strings"
	"testing"
)

// Doctor reports an unprotected profile, and names the repair only when the
// folder's own entries keep SYSTEM, Administrators and the owner in.
func TestDoctorReportsAnUnprotectedProfile(t *testing.T) {
	orig := pathDACLProtected
	t.Cleanup(func() { pathDACLProtected = orig })

	pathDACLProtected = func(string) (bool, error) { return false, nil }
	out := captureStdout(t, func() {
		if !reportUnprotectedProfile() {
			t.Error("an unprotected profile did not count against health")
		}
	})
	if !strings.Contains(out, "[FAIL]") || !strings.Contains(out, "takes its parent's permissions") {
		t.Errorf("no finding printed:\n%s", out)
	}

	pathDACLProtected = func(string) (bool, error) { return true, nil }
	out = captureStdout(t, func() {
		if reportUnprotectedProfile() {
			t.Error("a protected profile was reported")
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected silence for a protected profile, got:\n%s", out)
	}
}

func TestProtectionIsRestoredOnlyWhenTheOwnerStaysIn(t *testing.T) {
	const owner = "S-1-5-21-1-2-3-1001"
	full := func(sid string) aclEntry { return aclEntry{SID: sid, Mask: fileAllAccess} }
	stock := []aclEntry{full("S-1-5-18"), full("S-1-5-32-544"), full(owner)}

	if !protectionIsSafeToRestore(stock, owner) {
		t.Error("a stock profile's own entries were judged unsafe")
	}
	if protectionIsSafeToRestore(stock[:2], owner) {
		t.Error("removing inherited entries would lock the owner out, and the repair was still offered")
	}
	inheritedOnly := []aclEntry{full("S-1-5-18"), full("S-1-5-32-544"), {SID: owner, Mask: fileAllAccess, Inherited: true}}
	if protectionIsSafeToRestore(inheritedOnly, owner) {
		t.Error("the owner's access was inherited, so it goes with the repair, and the repair was still offered")
	}
}

// assumeProtectedProfile stands in a protected profile for doctor tests whose
// subject is something else, so the state of the machine running them cannot
// decide their verdict.
func assumeProtectedProfile(t *testing.T) {
	t.Helper()
	orig := pathDACLProtected
	pathDACLProtected = func(string) (bool, error) { return true, nil }
	t.Cleanup(func() { pathDACLProtected = orig })
}
