package main

import (
	"strings"
	"testing"
)

// Every restricted mode used to emit `(allow network-outbound (remote tcp
// "localhost:*"))`, so contained code could reach any service on the developer's
// machine without an allowlist entry, and `network.mode: offline` was not offline.
//
// These assert the generated profile only, which is the same confidence every
// other macOS claim carries: nvx generates the policy it intends to, and whether
// the kernel enforces it has never been checked on macOS hardware. That is a real
// limit on this test, not a formality -- it is written down in the enforcement
// matrix under "profile only".
func TestSeatbeltGrantsLoopbackOnlyWhereTheModeMeansIt(t *testing.T) {
	const wildcardTCP = `(allow network-outbound (remote tcp "localhost:*"))`
	const wildcardUDP = `(allow network-outbound (remote udp "localhost:*"))`

	profileFor := func(mode string) string {
		return buildSeatbeltProfile(NetworkLaunchContext{
			Mode:           mode,
			HTTPProxyPort:  8080,
			SOCKSProxyPort: 1080,
		}, tempDir(t), tempDir(t))
	}

	t.Run("proxy reaches the proxy and nothing else on loopback", func(t *testing.T) {
		p := profileFor("proxy")
		if strings.Contains(p, wildcardTCP) || strings.Contains(p, wildcardUDP) {
			t.Errorf("proxy mode grants all of loopback; a contained install could reach the developer's database:\n%s", p)
		}
		for _, want := range []string{
			`(allow network-outbound (remote tcp "localhost:8080"))`,
			`(allow network-outbound (remote tcp "localhost:1080"))`,
		} {
			if !strings.Contains(p, want) {
				t.Errorf("proxy mode must still reach its own proxy; missing %s in:\n%s", want, p)
			}
		}
		// Binding a listening socket on the host's loopback is not something a
		// contained install needs, and it lets other host processes connect in.
		if strings.Contains(p, "network-bind") {
			t.Errorf("proxy mode should not grant network-bind:\n%s", p)
		}
	})

	t.Run("offline means offline", func(t *testing.T) {
		p := profileFor("offline")
		if strings.Contains(p, "network-outbound") || strings.Contains(p, "network-bind") {
			t.Errorf("offline mode grants network rules; it must grant none:\n%s", p)
		}
	})

	t.Run("loopback mode is the one that may reach loopback", func(t *testing.T) {
		p := profileFor("loopback")
		if !strings.Contains(p, wildcardTCP) {
			t.Errorf("loopback mode must reach loopback -- that is its entire meaning:\n%s", p)
		}
	})

	t.Run("open is unrestricted", func(t *testing.T) {
		if p := profileFor("open"); !strings.Contains(p, "(allow network*)") {
			t.Errorf("open mode should be unrestricted:\n%s", p)
		}
	})

	// With no proxy port known there is nothing legitimate to reach, and the old
	// code's wildcard would have quietly opened all of loopback instead.
	t.Run("proxy with no known port fails closed", func(t *testing.T) {
		p := buildSeatbeltProfile(NetworkLaunchContext{Mode: "proxy"}, tempDir(t), tempDir(t))
		if strings.Contains(p, "network-outbound") {
			t.Errorf("proxy mode with no proxy port should grant no egress:\n%s", p)
		}
	})
}
