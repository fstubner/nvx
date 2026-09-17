package nvx

import (
	"net"
	"testing"
)

// The loopback refusal exists so untrusted contained code cannot ask the
// developer for access to a local service. 0.0.0.0 and :: are the unspecified
// address, which net.IP.IsLoopback does not cover, and connecting to either
// reaches 127.0.0.1. Both slipped past the refusal into the ordinary prompt,
// which then read as an unfamiliar external host rather than "your local
// database". Found by the 2026-09-17 audit, probe P2/P8.
func TestUnspecifiedAddressCountsAsLoopback(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::", "[::]", "0.0.0.0 "} {
		if !isLoopback(host) {
			t.Errorf("isLoopback(%q) = false; the unspecified address reaches localhost", host)
		}
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "127.5.5.5"} {
		if !isLoopback(host) {
			t.Errorf("isLoopback(%q) = false; a genuine loopback address stopped matching", host)
		}
	}
	for _, host := range []string{"registry.npmjs.org", "8.8.8.8", "2606:4700::1111"} {
		if isLoopback(host) {
			t.Errorf("isLoopback(%q) = true; a public host was refused as local", host)
		}
	}
}

func TestAnyLoopbackCatchesUnspecifiedResolution(t *testing.T) {
	if !anyLoopback([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("0.0.0.0")}) {
		t.Error("a resolution containing 0.0.0.0 was not treated as loopback")
	}
	if !anyLoopback([]net.IP{net.ParseIP("::")}) {
		t.Error("a resolution to :: was not treated as loopback")
	}
	if anyLoopback([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1")}) {
		t.Error("a purely public resolution was refused as loopback")
	}
}
