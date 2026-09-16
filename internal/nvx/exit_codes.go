package nvx

// Exit codes for `nvx policy check`.
//
// Every other nvx command exits 0 or 1, so a CI job could not tell a policy
// violation from a network blip, a typo in a flag, or nvx failing to start. That
// is the whole difficulty of gating a pipeline on a security tool: "it failed"
// is not actionable, and the usual workaround -- grepping the output -- breaks
// the first time a message is reworded.
//
// 0 and 1 keep their existing meanings so nothing that already treats non-zero
// as failure changes behaviour. The classes start at 10, leaving 2 alone (nvx
// already uses it for a usage error in one place) and leaving room below 10 for
// anything that turns out to be more general than one command.
//
// These numbers are a published contract: docs/exit-codes.md documents them and
// TestExitCodesMatchTheirDocumentation pins the two together. Renumbering one
// breaks somebody's pipeline silently, which is the failure this replaces.
const (
	// exitPolicyPass means every check that ran found nothing.
	exitPolicyPass = 0
	// exitPolicyInternalError is nvx itself failing: an unreadable home
	// directory, an unknown flag, a lookup that errored. Not a verdict about the
	// project.
	exitPolicyInternalError = 1

	// exitPolicyViolation is a project that disagrees with the effective policy
	// in a way none of the narrower classes covers -- today, a project policy
	// file that loosens an enforced baseline, or one that loosens settings and
	// has never been trusted, so the settings a reader sees in the repository are
	// not the settings in force.
	exitPolicyViolation = 10
	// exitBlockedPackage is a package named in blocked_packages present in the
	// project's dependencies.
	exitBlockedPackage = 11
	// exitVulnerability is an advisory at or above vulnerabilities.min_severity
	// that vulnerabilities.allowed_advisories does not accept.
	exitVulnerability = 12
	// exitReleaseAge is a dependency published inside the release_age cooling-off
	// window.
	exitReleaseAge = 13
	// exitSandboxUnavailable is a policy that requires containment on a platform
	// nvx cannot contain on. The project is fine; the machine cannot honour the
	// policy, and a pipeline usually wants to treat that differently from a
	// developer having done something wrong.
	exitSandboxUnavailable = 14
	// exitPolicyFileInvalid is a policy file that could not be read or parsed. No
	// verdict was reached, so it must not be reported as a pass.
	exitPolicyFileInvalid = 15
)

// policyCheckClass names a failure class for the JSON output and for the
// documentation test. Ordered most severe first: when several checks fail, the
// command exits with the first of these that did.
//
// "Most severe" here means "answer the question the reader will ask next". A run
// that could not finish outranks every verdict, because it did not reach one; a
// policy that could not be read comes next, for the same reason one step later;
// a missing sandbox is last because it is a property of the machine rather than
// of the change under review.
var policyCheckClasses = []struct {
	Name string
	Code int
}{
	{"internal_error", exitPolicyInternalError},
	{"policy_file_invalid", exitPolicyFileInvalid},
	{"policy_violation", exitPolicyViolation},
	{"blocked_package", exitBlockedPackage},
	{"vulnerability", exitVulnerability},
	{"release_age", exitReleaseAge},
	{"sandbox_unavailable", exitSandboxUnavailable},
}
