//go:build linux

package nvx

import (
	"reflect"
	"testing"
)

// loopback mode gets proxy's filter, not offline's.
//
// It had offline's until 2026-09-08, and that filter denies connect() outright
// and refuses to create any AF_INET socket -- so a contained process could reach
// no loopback service, and could not reach nvx's own relay either. The mode was
// offline by another name, on the platform where nobody would look for it,
// because the name says the opposite.
//
// Compared against the two builders rather than by decoding the cBPF: the jump
// offsets here are hand-written and not reviewable by inspection, which the
// comment on buildProxyNetworkFilter says plainly after they were once wrong in
// a way that denied the very TCP the proxy needed.
func TestLoopbackModeGetsTheProxyFilter(t *testing.T) {
	got, wanted := seccompFilterForMode("loopback")
	if !wanted {
		t.Fatal("loopback asked for no filter; the namespace alone does not enforce a mode")
	}
	if !reflect.DeepEqual(got, buildProxyNetworkFilter()) {
		t.Error("loopback does not get the proxy filter, so it cannot reach the relay that carries it")
	}
	if reflect.DeepEqual(got, buildOfflineNetworkFilter()) {
		t.Error("loopback still gets the offline filter: connect() is denied, and the mode reaches nothing")
	}
}

// offline keeps its own, which is the filter that denies everything.
//
// The pair matters more than either half: the whole defect was two modes sharing
// one filter, so a test that only checked loopback would pass if offline were
// quietly widened to match it.
func TestOfflineKeepsTheFilterThatDeniesEverything(t *testing.T) {
	got, wanted := seccompFilterForMode("offline")
	if !wanted {
		t.Fatal("offline asked for no filter")
	}
	if !reflect.DeepEqual(got, buildOfflineNetworkFilter()) {
		t.Error("offline no longer gets the offline filter")
	}
	if reflect.DeepEqual(got, buildProxyNetworkFilter()) {
		t.Error("offline was widened to the proxy filter; it must deny connect() outright")
	}
}

// Both modes still get a network namespace of their own.
//
// This is what stops loopback from meaning "the host's loopback, directly". The
// contained process reaches its own 127.0.0.1, the relay carries it out over
// AF_UNIX, and the parent proxy decides every destination -- so the mode's extra
// reach is one rule in the proxy rather than a hole in the kernel policy.
func TestLoopbackStillGetsItsOwnNamespace(t *testing.T) {
	for _, mode := range []string{"loopback", "offline", "proxy"} {
		if !networkModeRequiresNamespace(mode) {
			t.Errorf("mode %q was given the host's network namespace", mode)
		}
	}
}
