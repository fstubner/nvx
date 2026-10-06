package nvx

import (
	"strings"
	"testing"
)

// A contained process on macOS reached the system resolver through the Mach
// service com.apple.dnssd.service, which the blanket (allow mach-lookup) let
// through, so it could send data out encoded in the names it looked up. Every
// mode but open denies that service after the allow, since Seatbelt applies the
// last matching rule. scripts/sandbox-enforcement-macos.sh checks the kernel
// honours it.
func TestSeatbeltDeniesTheResolverServiceOutsideOpenMode(t *testing.T) {
	const allowMach = "(allow mach-lookup)\n"
	const denyResolver = `(deny mach-lookup (global-name "com.apple.dnssd.service"))`

	profileFor := func(mode string) string {
		return buildSeatbeltProfile(NetworkLaunchContext{
			Mode:           mode,
			HTTPProxyPort:  8080,
			SOCKSProxyPort: 1080,
		}, tempDir(t), tempDir(t), "", nil)
	}

	for _, mode := range []string{"proxy", "offline", "loopback", "", "offlin", " Proxy "} {
		p := profileFor(mode)
		allowAt := strings.Index(p, allowMach)
		denyAt := strings.Index(p, denyResolver)
		if allowAt < 0 {
			t.Fatalf("mode %q: the profile no longer allows mach-lookup, so this test's ordering check means nothing:\n%s", mode, p)
		}
		if denyAt < 0 {
			t.Errorf("mode %q: the profile does not deny com.apple.dnssd.service, so a contained process can resolve names:\n%s", mode, p)
			continue
		}
		if denyAt < allowAt {
			t.Errorf("mode %q: the resolver deny comes before (allow mach-lookup), which then overrides it:\n%s", mode, p)
		}
	}

	if p := profileFor("open"); strings.Contains(p, denyResolver) {
		t.Errorf("open mode is unrestricted and should resolve names, but denies the resolver:\n%s", p)
	}
}
