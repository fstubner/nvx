//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestEgressSocketPathFitsAtTheAFUnixLimit pins the boundary because getting it
// wrong is silent. Over the limit, bind fails with "invalid argument" -- a message
// that reads as a permissions problem, which is exactly how it was first
// misdiagnosed while building the relay probe.
func TestEgressSocketPathFitsAtTheAFUnixLimit(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"empty", "", false},
		{"typical guest home", `C:\Users\felix\.nvx\sandbox_home\3197382443c798fc\egress.sock`, true},
		{"one byte under", strings.Repeat("a", unixSocketPathMax-1), true},
		{"exactly the field size", strings.Repeat("a", unixSocketPathMax), false},
		{"over", strings.Repeat("a", unixSocketPathMax+40), false},
	}
	for _, tc := range cases {
		if got := egressSocketPathFits(tc.path); got != tc.want {
			t.Errorf("%s: egressSocketPathFits(%d bytes) = %v, want %v", tc.name, len(tc.path), got, tc.want)
		}
	}
}

// TestDefaultGuestHomeLeavesRoomForTheSocket checks the case that actually ships:
// a guest home under a default nvx home must leave room for the socket name. If
// this ever stops holding, every proxied run on Windows fails closed.
func TestDefaultGuestHomeLeavesRoomForTheSocket(t *testing.T) {
	// getSandboxHomeDir + a 16-hex session id, under a plausible profile path.
	guestHome := filepath.Join(`C:\Users\some-fairly-long-username\.nvx`, "sandbox_home", "0123456789abcdef")
	sock := windowsEgressSocketPath(guestHome)
	if !egressSocketPathFits(sock) {
		t.Errorf("a default guest home already overflows the AF_UNIX limit at %d bytes (%s); "+
			"proxied runs would fail closed for ordinary users", len(sock), sock)
	}
}

// TestWindowsEgressNeedsRelayCoversEveryMode ties the relay decision to the modes
// that must not have one. "open" is the documented opt-out and offline/loopback
// have no egress to allowlist; everything else, including an unset mode, must be
// relayed rather than silently connecting direct.
func TestWindowsEgressNeedsRelayCoversEveryMode(t *testing.T) {
	for _, mode := range []string{"open", "OPEN", " open ", "offline", "loopback", "LOOPBACK"} {
		if windowsEgressNeedsRelay(mode) {
			t.Errorf("mode %q should not use the relay", mode)
		}
	}
	// An unrecognised or empty mode must fail towards enforcement, not away from
	// it: reaching the direct path by typo is how an allowlist quietly stops
	// applying.
	for _, mode := range []string{"proxy", "PROXY", "", "  ", "prxy", "strict"} {
		if !windowsEgressNeedsRelay(mode) {
			t.Errorf("mode %q must use the relay; anything unrecognised has to fail towards enforcement", mode)
		}
	}
}
