package nvx

import (
	"strings"
	"testing"
)

// A command that fails after nvx refused a host exits 77, and the line saying so
// does not claim the host is why it failed. nvx cannot read the command's
// output. Measured 2026-10-07, `nvx --strict npx next build` had
// telemetry.nextjs.org refused, which the build shrugged off, and then failed on
// child_process.fork(). The line read "The command exited 1 after nvx refused a
// connection to telemetry.nextjs.org:443", and a person who allowed the host got
// the same failure.
func TestTheLineAfterARefusedHostDoesNotBlameTheHost(t *testing.T) {
	forgetWideningRefusals()
	t.Cleanup(forgetWideningRefusals)
	withEnv(t, "NVX_TRUST_YES", "")
	p := newPromptingProxy(t, nil)
	captureStderrHere(t, func() { p.allowed(parseHostPortSpec("telemetry.example.com", 443), nil) })

	var got int
	out := captureStderrHere(t, func() { got = exitCodeAfterRefusedWidening(1) })
	if got != exitRefused {
		t.Fatalf("exit %d, want %d for a command that failed after a refused host", got, exitRefused)
	}
	for _, want := range []string{
		"nvx refused a connection to telemetry.example.com:443 while the command ran",
		"the command exited 1",
		"helps only if the command's own error, above, is about that connection",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the line does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "exited 1 after nvx refused") {
		t.Errorf("the line still reads as though the refused host is why the command failed:\n%s", out)
	}
}
