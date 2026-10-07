package nvx

import (
	"strings"
	"testing"
)

// The Seatbelt profile denies TIOCSTI so a contained process cannot type into
// the terminal nvx shares with the user's shell. See seatbeltTerminalInputDeny.
//
// This checks the generated text, as the other seatbelt profile tests do.
// scripts/sandbox-enforcement-macos.sh checks a real kernel honours it, in the
// macOS CI job, which is the only place that can.
func TestSeatbeltDeniesTerminalInputInjection(t *testing.T) {
	const denyTIOCSTI = "(deny file-ioctl (ioctl-command 2147578994))"
	const writeAllow = "(allow file-write*\n"

	for _, mode := range []string{"proxy", "offline", "loopback", "open", "", " Proxy "} {
		p := buildSeatbeltProfile(NetworkLaunchContext{
			Mode:           mode,
			HTTPProxyPort:  8080,
			SOCKSProxyPort: 1080,
		}, tempDir(t), tempDir(t), "", nil)

		denyAt := strings.Index(p, denyTIOCSTI)
		if denyAt < 0 {
			t.Errorf("mode %q: the profile does not deny TIOCSTI, so a contained process could type into the terminal:\n%s", mode, p)
			continue
		}
		// The deny has to come after the write allow that grants /dev/tty and
		// /dev/ptmx, or that allow would override it. Every mode emits the write
		// allow, so its absence is a test-breaking change worth failing on.
		allowAt := strings.Index(p, writeAllow)
		if allowAt < 0 {
			t.Fatalf("mode %q: the profile no longer has a file-write* allow, so the ordering check is meaningless:\n%s", mode, p)
		}
		if denyAt < allowAt {
			t.Errorf("mode %q: the TIOCSTI deny comes before the file-write* allow, which then overrides it:\n%s", mode, p)
		}
		// Nothing may grant file-ioctl, which would reopen the door the deny shuts.
		// The profile grants it nowhere today, and this pins that: the deny relies
		// on it, because Seatbelt takes the last matching rule.
		if strings.Contains(p, "(allow file-ioctl") {
			t.Errorf("mode %q: the profile now allows file-ioctl somewhere; if it covers TIOCSTI the deny is undone:\n%s", mode, p)
		}
	}
}

// The command number is TIOCSTI in decimal, and decimal on purpose.
//
// 2147578994 is 0x80017472, _IOW('t', 114, char). The TIOCSTI symbol and a hex
// literal both fail to parse under sandbox-exec on macOS 13 and 14 (measured by
// the openai/codex and rntz projects), so a well-meant rewrite to either would
// make sandbox-exec reject the whole profile and nvx fail closed on every run.
// This pins the form that parses.
func TestSeatbeltTerminalDenyUsesTheDecimalCommand(t *testing.T) {
	if seatbeltTerminalInputDeny != "(deny file-ioctl (ioctl-command 2147578994))" {
		t.Errorf("seatbeltTerminalInputDeny changed to %q; a non-decimal ioctl-command does not parse on macOS 13/14 and fails the whole profile closed", seatbeltTerminalInputDeny)
	}
	if 0x80017472 != 2147578994 {
		t.Fatal("0x80017472 is not 2147578994; the decimal in the rule is wrong")
	}
}
