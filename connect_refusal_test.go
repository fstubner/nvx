package main

import (
	"strings"
	"testing"
)

// The docker provider says --connect does nothing, on every platform and in
// every mode.
//
// It was silent before. dockerRunArgs never read ConnectPorts, so a policy
// carrying connect_ports or a command line carrying --connect launched a
// container that simply could not reach the service, and nothing said why. That
// is the same defect the flag itself was written to fix on Linux and macOS:
// accepted, ignored, and left for the developer to blame their own service for.
func TestDockerSaysItCannotCarryConnect(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, mode := range []string{"proxy", "open", "offline", "loopback"} {
			warn, hint := connectRefusalFor("docker", goos, mode)
			if warn == "" {
				t.Errorf("docker on %s in mode %q accepted --connect in silence", goos, mode)
				continue
			}
			if !strings.Contains(hint, "native") {
				t.Errorf("the refusal does not say what to use instead: %q", hint)
			}
		}
	}
}

// Linux refuses the two modes whose seccomp filter denies the sandbox an IP
// socket, and no others.
func TestLinuxRefusesConnectOnlyWhereTheModeCannotCarryIt(t *testing.T) {
	for _, mode := range []string{"offline", "loopback"} {
		if warn, _ := connectRefusalFor("native", "linux", mode); warn == "" {
			t.Errorf("mode %q denies the sandbox every IP socket, so --connect must be reported as unhonoured", mode)
		}
	}
	for _, mode := range []string{"proxy", "open", ""} {
		if warn, _ := connectRefusalFor("native", "linux", mode); warn != "" {
			t.Errorf("mode %q can carry --connect, but nvx warns: %s", mode, warn)
		}
	}
}

// Windows and macOS carry it in every mode, including offline.
//
// The asymmetry is deliberate and worth pinning, because it looks like an
// oversight. Neither platform's containment refuses the contained process a
// loopback socket -- the AppContainer tunnel and the Seatbelt relay are both
// independent of the network mode -- so `offline` there means "no network of
// your own", not "not even the one service you named on the command line".
// Linux cannot make that distinction, because its filter is what enforces the
// mode.
func TestWindowsAndMacOSCarryConnectInEveryMode(t *testing.T) {
	for _, goos := range []string{"windows", "darwin"} {
		for _, mode := range []string{"proxy", "open", "offline", "loopback"} {
			if warn, _ := connectRefusalFor("native", goos, mode); warn != "" {
				t.Errorf("%s in mode %q reports --connect unhonoured, but it carries it: %s", goos, mode, warn)
			}
		}
	}
}

// The provider name is matched the way every other reader of it matches: after
// trimming and without case sensitivity. A policy file is hand-edited, and
// "Docker" or "docker " reaching the wrong branch would restore the silence.
func TestTheDockerRefusalIsNotDefeatedByCaseOrSpace(t *testing.T) {
	for _, name := range []string{"Docker", "DOCKER", " docker", "docker "} {
		if warn, _ := connectRefusalFor(name, "linux", "offline"); !strings.Contains(warn, "docker provider") {
			t.Errorf("provider %q did not get the docker refusal: %q", name, warn)
		}
	}
}
