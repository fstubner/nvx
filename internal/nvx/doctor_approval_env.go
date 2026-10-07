package nvx

// reportApprovalEnvironment warns about the variables that answer nvx's
// questions for everything started from this environment, and says what each
// turns off.
//
// They are set once, in a shell profile, a CI config or an agent's settings,
// and forgotten. Nothing about a later run shows they are there. A check that
// NVX_YES approved prints one line among an install's output. Doctor is where
// someone asks whether nvx is protecting them, so it says so here.
//
// A warning, not a failure. Each can be set on purpose, and doctor's exit code
// is about whether nvx intercepts and contains commands, which these do not
// change.
func reportApprovalEnvironment() {
	if nvxYesSet() {
		LogWarn("NVX_YES is set in this environment. It turns off the pre-install checks for every command started from it. A likely typosquat, a release inside the cooling-off window, install scripts, a known vulnerability and a lookup that failed are each approved without asking. A package OSV lists as malicious is still refused.")
	}
	if agentModeEnvSet() {
		LogWarn("NVX_AGENT_MODE is set in this environment. It turns off nvx's questions. Anything that would ask is refused with exit 77, including at a terminal, so a check cannot be approved there by typing y.")
	}
	if trustYesSet() {
		LogWarn("NVX_TRUST_YES is set in this environment. It turns off the refusal to widen the sandbox. A project policy that loosens settings, a host the allowlist does not name and a persistent tool profile are all approved without asking. Whatever sets this environment makes those decisions.")
	}
}
