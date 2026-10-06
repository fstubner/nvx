//go:build windows

package nvx

import "testing"

// A contained launch does not carry the capability an older `nvx setup` granted
// drive-root and Users-folder access to.
//
// Nothing needs those entries. Leaving the identity off the token makes any entry
// an older setup left on a machine inert at once, instead of leaving it live
// until someone runs an elevated `nvx setup`. It cannot be seen from a launch
// that works, because a launch works either way, so the capability list is what
// is asserted.
func TestLaunchesDoNotCarryTheSetupCapability(t *testing.T) {
	setupCap, err := deriveCapabilitySIDString(setupCapabilityName)
	if err != nil {
		t.Skipf("cannot derive a capability SID here: %v", err)
	}
	caps := launchCapabilitySIDs([]string{"S-1-15-3-1024-1"}, []string{capabilityInternetClientSID})
	for _, c := range caps {
		if c == setupCap {
			t.Fatalf("launchCapabilitySIDs carries the setup capability %s; an entry an older setup left on a drive root would apply again", setupCap)
		}
	}
	if len(caps) < 3 {
		t.Errorf("launchCapabilitySIDs = %v, lost a capability it should carry (the scope, the network and the runtime ones)", caps)
	}
}
