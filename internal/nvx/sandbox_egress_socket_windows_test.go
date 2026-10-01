//go:build windows

package nvx

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
	sock := windowsEgressSocketPath(guestHomeSocketPrefix(guestHome))
	if !egressSocketPathFits(sock) {
		t.Errorf("a default guest home already overflows the AF_UNIX limit at %d bytes (%s); "+
			"proxied runs would fail closed for ordinary users", len(sock), sock)
	}
}

// TestWindowsEgressNeedsRelayCoversEveryMode ties the relay decision to the modes
// that must not have one. "open" is the documented opt-out and offline has no
// egress to allowlist; everything else, including an unset mode, must be relayed
// rather than silently connecting direct.
//
// loopback was in the first list until 2026-09-08, which made it offline by
// another name on this platform: no capability and no relay reaches nothing, and
// this mode exists to reach the services on 127.0.0.1. It relays, like proxy,
// and the capability check in sandbox_network_windows_test.go is what keeps that
// from meaning more.
func TestWindowsEgressNeedsRelayCoversEveryMode(t *testing.T) {
	for _, mode := range []string{"open", "OPEN", " open ", "offline", "OFFLINE"} {
		if windowsEgressNeedsRelay(mode) {
			t.Errorf("mode %q should not use the relay", mode)
		}
	}
	// An unrecognised or empty mode must fail towards enforcement, not away from
	// it: reaching the direct path by typo is how an allowlist quietly stops
	// applying.
	for _, mode := range []string{"proxy", "PROXY", "", "  ", "prxy", "strict", "loopback", "LOOPBACK"} {
		if !windowsEgressNeedsRelay(mode) {
			t.Errorf("mode %q must use the relay; anything unrecognised has to fail towards enforcement", mode)
		}
	}
}

// TestALongNvxHomeMovesTheSocketsToTheAppContainerFolder pins where a session's
// sockets go. A 132-character NVX_HOME is the length measured refusing on
// 2026-10-01, and 65 the longest measured working in the guest home that day.
func TestALongNvxHomeMovesTheSocketsToTheAppContainerFolder(t *testing.T) {
	const container = `C:\Users\felix\AppData\Local\Packages\nvx.sandbox.0123456789abcdef\AC`
	guestUnder := func(nvxHome string) string {
		return filepath.Join(getSandboxHomeDir(nvxHome), "0123456789abcdef")
	}
	home := func(n int) string { return `C:\` + strings.Repeat("h", n-3) }
	egressOnly := NetworkLaunchContext{egress: &EgressProxy{}}
	withConnect := NetworkLaunchContext{egress: &EgressProxy{}, ConnectPorts: []connectMapping{{Host: 65535}}}

	// Short enough: the guest home, as before.
	guest := guestUnder(home(65))
	if got := windowsSocketPrefix(guest, container, egressOnly); got != guest+`\` {
		t.Errorf("a 65-character NVX_HOME moved the sockets to %q; they fit in the guest home", got)
	}
	// Only the sockets this session binds count. A tunnel socket has the longest
	// name, so the same NVX_HOME moves when one is asked for.
	if got := windowsSocketPrefix(guest, container, withConnect); got == guest+`\` {
		t.Errorf("a tunnel socket that does not fit in the guest home was left there")
	}

	// Too long: the AppContainer's folder, tagged with the session.
	guest = guestUnder(home(132))
	got := windowsSocketPrefix(guest, container, egressOnly)
	if want := container + `\01234567-`; got != want {
		t.Fatalf("a 132-character NVX_HOME put the sockets at %q, want %q", got, want)
	}
	if err := windowsSocketRoomError(windowsSocketPrefix(guest, container, withConnect), home(132), guest, withConnect); err != nil {
		t.Errorf("the sockets in the AppContainer folder were refused: %v", err)
	}

	// No AppContainer folder: the guest home, and a refusal that names the
	// longest NVX_HOME that works.
	if got := windowsSocketPrefix(guest, "", egressOnly); got != guest+`\` {
		t.Errorf("with no AppContainer folder the sockets went to %q", got)
	}
	err := windowsSocketRoomError(guest+`\`, home(132), guest, egressOnly)
	if err == nil || !strings.Contains(err.Error(), "at most 65 characters (it is 132)") {
		t.Errorf("the refusal does not name the measured limit: %v", err)
	}

	// An AppContainer folder too long to help changes nothing.
	long := `C:\Users\` + strings.Repeat("u", 80) + `\AppData\Local\Packages\nvx.sandbox.0123456789abcdef\AC`
	if got := windowsSocketPrefix(guest, long, egressOnly); got != guest+`\` {
		t.Errorf("the sockets went to %q, which does not hold them either", got)
	}
}
