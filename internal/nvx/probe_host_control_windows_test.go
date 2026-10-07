//go:build windows

package nvx

import (
	"fmt"
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
// "Capable" is not required on a developer machine, which may well be one that
// cannot. On GitHub Actions it is. Hosted Windows runners refused to create
// AppContainer children until 2026-09-21 and launch them now (run 37244525606),
// so a runner that refuses again means every probe in the run verifies nothing,
// and the run has to say so instead of passing. A negative verdict anywhere
// also has to come with a reason, because a bare "cannot run here" is exactly
// the unexplained skip this whole mechanism exists to abolish.
func TestTheHostCapabilityControlReportsItsVerdict(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	capable, why := hostCanCreateAppContainers()
	t.Logf("host control launch: canCreateAppContainers=%v why=%q", capable, why)
	if problem := hostControlVerdictProblem(capable, why, os.Getenv("GITHUB_ACTIONS") == "true"); problem != "" {
		t.Fatal(problem)
	}
}

// hostControlVerdictProblem says what is wrong with the control's answer, or ""
// when nothing is. Separate from the test above so the decision can be checked
// without a host that cannot create AppContainers.
func hostControlVerdictProblem(capable bool, why string, onGitHubActions bool) string {
	switch {
	case !capable && why == "":
		return "the control reports this host cannot create AppContainer children but gives no reason; " +
			"every containment probe skipped in this run would be unexplained"
	case capable && why != "":
		return fmt.Sprintf("the control reports success and a reason at once, so callers cannot tell which it meant: %q", why)
	case !capable && onGitHubActions:
		return fmt.Sprintf("this GitHub Actions runner cannot create AppContainer children (%s), "+
			"so the containment probes in this run verify nothing", why)
	}
	return ""
}

// The decision against both kinds of host. A machine that really cannot create
// AppContainers is needed to see the failing case, and a hosted runner no longer
// is one.
func TestAnIncapableHostIsAFailureOnGitHubActionsAndNotElsewhere(t *testing.T) {
	const why = "the control launch was refused: Access is denied."
	if got := hostControlVerdictProblem(false, why, false); got != "" {
		t.Errorf("a developer machine that cannot create AppContainers was called a problem: %q", got)
	}
	if got := hostControlVerdictProblem(false, why, true); got == "" {
		t.Error("a GitHub Actions runner that cannot create AppContainers passed, and its probes verify nothing")
	}
	if got := hostControlVerdictProblem(true, "", true); got != "" {
		t.Errorf("a capable GitHub Actions runner was called a problem: %q", got)
	}
	if hostControlVerdictProblem(false, "", false) == "" {
		t.Error("a negative verdict with no reason passed")
	}
	if hostControlVerdictProblem(true, why, false) == "" {
		t.Error("a positive verdict with a reason passed")
	}
}
