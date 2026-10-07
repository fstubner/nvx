package nvx

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// withEnv sets an environment variable for one test and restores it after.
func withEnv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// someoneAtTheConsole makes the prompt code see a terminal on stdin and a y
// typed at it: a person, or an agent driving a pseudo-terminal. asked reports
// whether anything read that y.
func someoneAtTheConsole(t *testing.T) (asked *bool) {
	t.Helper()
	oldInteractive, oldConsole := stdinInteractive, consoleYesNo
	t.Cleanup(func() { stdinInteractive, consoleYesNo = oldInteractive, oldConsole })
	var was bool
	stdinInteractive = func() bool { return true }
	consoleYesNo = func(string, string) bool {
		was = true
		return true
	}
	return &was
}

// TestBlanketYesDoesNotWidenTheTrustBoundary is the fix for the worst finding an
// independent acceptance pass produced.
//
// Requests that widen the sandbox decide the security model rather than a step
// inside it, such as trusting a project's own .nvx-policy.json when it loosens
// settings, and adding a host to the egress allowlist. Both were covered by -y
// and NVX_YES, and --agent-mode set that yes. So the mode built for AI agents,
// which clone repositories nobody has read, auto-approved a repository's request
// to switch containment off. Measured against the shipped binary, a policy
// carrying {"isolation":{"enabled":false}} was refused without the flag and
// silently trusted with it, and arbitrary egress hosts were approved and
// persisted.
func TestBlanketYesDoesNotWidenTheTrustBoundary(t *testing.T) {
	for _, key := range []string{"NVX_YES", "NVX_AGENT_MODE"} {
		t.Run(key, func(t *testing.T) {
			withEnv(t, "NVX_TRUST_YES", "")
			withEnv(t, key, "true")
			oldY, oldA := yesFlag, agentModeFlag
			yesFlag, agentModeFlag = true, key == "NVX_AGENT_MODE"
			t.Cleanup(func() { yesFlag, agentModeFlag = oldY, oldA })

			captureStderrHere(t, func() {
				if approveWidening(wideningRequest{what: "a project policy that disables containment"}) {
					t.Errorf("%s approved a request that widens the sandbox; a repository could "+
						"switch its own sandbox off", key)
				}
			})
		})
	}
}

// A request to widen the sandbox is refused at a terminal too, without reading
// it. A terminal on stdin was taken to mean a person, and an agent that drives a
// pseudo-terminal can type y. TestATerminalAnswerCannotWidenTheSandbox drives
// that through a real pseudo-terminal where the platform has one. This pins the
// decision everywhere.
func TestAWideningRequestIsNotAskedAtATerminal(t *testing.T) {
	withEnv(t, "NVX_TRUST_YES", "")
	asked := someoneAtTheConsole(t)
	out := captureStderrHere(t, func() {
		if approveWidening(wideningRequest{what: "a connection to example.com:443", command: "nvx allow-host example.com:443"}) {
			t.Error("a y at the terminal widened the sandbox")
		}
	})
	if *asked {
		t.Error("nvx read an answer from the terminal for a request that widens the sandbox")
	}
	if !strings.Contains(out, "nvx allow-host example.com:443") {
		t.Errorf("the refusal does not give the command that allows it:\n%s", out)
	}
}

// TestTrustBoundaryHasADeliberateOptIn covers the escape hatch. Someone pinning
// their own policy in CI has a way through -- it is just not a variable that gets
// set by habit, which is the property that made NVX_YES the wrong door.
func TestTrustBoundaryHasADeliberateOptIn(t *testing.T) {
	withEnv(t, "NVX_TRUST_YES", "true")
	if !approveWidening(wideningRequest{what: "this project policy"}) {
		t.Error("NVX_TRUST_YES did not approve a request to widen the sandbox, so there is no way to " +
			"pin a policy non-interactively at all")
	}
}

// TestTrustBoundaryDeniesWhenNobodyIsThere pins the default. With no opt-in and
// no terminal, the answer is no, and the refusal says what a person runs and
// what an agent must not.
func TestTrustBoundaryDeniesWhenNobodyIsThere(t *testing.T) {
	withEnv(t, "NVX_TRUST_YES", "")
	withEnv(t, "NVX_YES", "")
	old := yesFlag
	yesFlag = false
	t.Cleanup(func() { yesFlag = old })
	agentWideningNoteOnce = sync.Once{}

	out := captureStderrHere(t, func() {
		if approveWidening(wideningRequest{what: "this project policy", refusal: "nvx refused.", command: "nvx trust .nvx-policy.json"}) {
			t.Error("a request to widen the sandbox was approved with no opt-in and no terminal")
		}
	})
	for _, want := range []string{"run this in your own terminal", "nvx trust .nvx-policy.json", agentWideningNote} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// A command that fails after nvx refused to widen the sandbox for it exits 77,
// as every refusal does. One that succeeds anyway keeps its 0.
func TestARefusedWideningMakesAFailingRunExit77(t *testing.T) {
	wideningRefusalsMu.Lock()
	saved := wideningRefusals
	wideningRefusals = nil
	wideningRefusalsMu.Unlock()
	t.Cleanup(func() {
		wideningRefusalsMu.Lock()
		wideningRefusals = saved
		wideningRefusalsMu.Unlock()
	})

	if got := exitCodeAfterRefusedWidening(1); got != 1 {
		t.Fatalf("with nothing refused a failing run exited %d, want its own 1", got)
	}
	withEnv(t, "NVX_TRUST_YES", "")
	p := newPromptingProxy(t, nil)
	captureStderrHere(t, func() { p.allowed(parseHostPortSpec("example.com", 443), nil) })

	var got int
	out := captureStderrHere(t, func() { got = exitCodeAfterRefusedWidening(1) })
	if got != exitRefused {
		t.Fatalf("a run that failed after nvx refused a host exited %d, want %d", got, exitRefused)
	}
	if !strings.Contains(out, "example.com:443") {
		t.Errorf("the exit code is not explained:\n%s", out)
	}
	if got := exitCodeAfterRefusedWidening(0); got != 0 {
		t.Errorf("a run that succeeded after a refused host exited %d, want 0", got)
	}
}
