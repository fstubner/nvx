//go:build windows

package main

import (
	"os"
	"testing"
)

// Runs the host control once per gate run and puts its answer in the log.
//
// The answer decides whether every later containment refusal is a failure or a
// skip, so it is worth stating out loud rather than leaving implicit in whichever
// probe happens to be refused first. A run that skipped its containment probes
// can then be read back: the control's verdict is right there, with its reason.
//
// The assertion is narrow on purpose. "Capable" is not something to require --
// GitHub-hosted Windows runners are legitimately not, and demanding it would turn
// CI red for a correct state. What must hold is that a negative verdict comes
// with a reason, because a bare "cannot run here" is exactly the unexplained skip
// this whole mechanism exists to abolish.
func TestTheHostCapabilityControlReportsItsVerdict(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	capable, why := hostCanCreateAppContainers()
	t.Logf("host control launch: canCreateAppContainers=%v why=%q", capable, why)
	if !capable && why == "" {
		t.Fatal("the control reports this host cannot create AppContainer children but gives no reason; " +
			"every containment probe skipped in this run would be unexplained")
	}
	if capable && why != "" {
		t.Fatalf("the control reports success and a reason at once, so callers cannot tell which it meant: %q", why)
	}
}
