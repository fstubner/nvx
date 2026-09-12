package nvx

import (
	"strings"
	"testing"
)

// The Seatbelt profile opens the port nvx listens on, never the service's own.
//
// This is the whole containment property of --connect on macOS. macOS could
// permit the service's port directly -- the sandbox shares the host's loopback,
// so nothing routes around it the way Landlock's namespace does on Linux -- and
// that shortcut is what this asserts nvx does NOT do. Permitting 9222 would let
// the contained process address the real service, and every other port it later
// guessed correctly would be a separate ask; permitting only nvx's listener
// keeps the sandbox reaching a pipe whose far end nvx chose.
func TestSeatbeltConnectOpensTheRelayPortAndNotTheService(t *testing.T) {
	profile := buildSeatbeltProfile(NetworkLaunchContext{
		Mode:          "proxy",
		HTTPProxyPort: 8080,
		ConnectPorts:  []connectMapping{{Host: 9222, Inside: 19222}},
	}, tempDir(t), tempDir(t))

	if !strings.Contains(profile, `(allow network-outbound (remote tcp "localhost:19222"))`) {
		t.Errorf("the contained process cannot reach nvx's relay, so --connect does nothing:\n%s", profile)
	}
	if strings.Contains(profile, `(allow network-outbound (remote tcp "localhost:9222"))`) {
		t.Errorf("the profile opens the service's own port; the sandbox can address it directly:\n%s", profile)
	}
}

// offline plus an explicit --connect reaches that one service.
//
// The neighbouring TestSeatbeltGrantsLoopbackOnlyWhereTheModeMeansIt asserts
// that offline with no --connect emits no network rule at all, and both are
// deliberate: offline means the sandbox gets no network of its own, not that a
// service the developer named on the command line is withheld. Windows behaves
// the same way -- its relay does not consult the network mode -- and a flag that
// worked on one platform and was silently dropped on the other is the defect
// this whole feature keeps running into.
func TestSeatbeltConnectWorksInOfflineMode(t *testing.T) {
	profile := buildSeatbeltProfile(NetworkLaunchContext{
		Mode:         "offline",
		ConnectPorts: []connectMapping{{Host: 5432, Inside: 15432}},
	}, tempDir(t), tempDir(t))

	if !strings.Contains(profile, `(allow network-outbound (remote tcp "localhost:15432"))`) {
		t.Errorf("--connect was dropped in offline mode:\n%s", profile)
	}
	if strings.Contains(profile, `localhost:*`) {
		t.Errorf("offline mode opened all of loopback:\n%s", profile)
	}
}

// A mapping whose in-sandbox port was never resolved contributes no rule.
//
// Inside is 0 until a listener binds. Rendering `localhost:0` would be a rule
// naming a port nothing listens on, and a profile that claims to have granted
// something it did not is worse than one that fails: the run would look
// configured and the tool would fail to connect with nothing to explain it.
func TestSeatbeltConnectEmitsNoRuleForAnUnresolvedPort(t *testing.T) {
	profile := buildSeatbeltProfile(NetworkLaunchContext{
		Mode:         "proxy",
		ConnectPorts: []connectMapping{{Host: 9222}},
	}, tempDir(t), tempDir(t))

	if strings.Contains(profile, `localhost:0`) {
		t.Errorf("the profile names port 0:\n%s", profile)
	}
}
