package nvx

import (
	"os"
	"strings"
)

// The switches that answer nvx's questions, read in one place so the checks,
// the prompts and `nvx doctor` agree on whether each is set.

// nvxYesSet reports whether NVX_YES approves the pre-install checks.
func nvxYesSet() bool {
	v := os.Getenv("NVX_YES")
	return v == "true" || v == "1"
}

// trustYesSet reports whether NVX_TRUST_YES approves widening the sandbox.
func trustYesSet() bool {
	v := os.Getenv("NVX_TRUST_YES")
	return v == "true" || v == "1"
}

// agentModeEnvSet reports whether NVX_AGENT_MODE turns on --agent-mode.
func agentModeEnvSet() bool {
	v := os.Getenv("NVX_AGENT_MODE")
	return v == "1" || strings.EqualFold(v, "true")
}

// trailingApprovalFlag returns an approval flag typed after a wrapped command,
// and that command. args is os.Args once the leading flags are read, so
// `npm install esbuild -y` through a shim gives "-y" and "npm".
//
// nvx reads its own flags only before the command. After it they belong to the
// command, so `npm install esbuild -y` went to npm, and the install-script check
// refused the install again with nothing to say the -y had not counted. It is
// the first thing an agent tries after a refusal. Scanning stops at "--", as
// parseShimOptions does.
func trailingApprovalFlag(args []string) (flag, command string) {
	i := 1
	if len(args) > 2 && strings.EqualFold(args[1], "shim") {
		i = 2
	}
	if len(args) <= i+1 || !isShimCommand(args[i]) {
		return "", ""
	}
	for _, a := range args[i+1:] {
		if a == "--" {
			break
		}
		switch a {
		case "-y", "--yes", "--agent-mode":
			return a, strings.ToLower(args[i])
		}
	}
	return "", ""
}

// noteTrailingApprovalFlag says that an approval flag typed after the command
// went to the command. It is called only where the flag would have mattered,
// at a check that is about to ask or to refuse. Saying it on every `npx -y`
// would be noise, since there -y is npx's own flag and the person meant it for
// npx.
func noteTrailingApprovalFlag() {
	flag, cmd := trailingApprovalFlag(os.Args)
	switch flag {
	case "":
		return
	case "--agent-mode":
		LogRefusalDetail("--agent-mode was passed to %s, not to nvx. To set it, run NVX_AGENT_MODE=1 %s ... or nvx --agent-mode %s ...", cmd, cmd, cmd)
	default:
		LogRefusalDetail("%s was passed to %s, not to nvx, so it does not approve nvx's checks. To approve them, run NVX_YES=true %s ... or nvx -y %s ...", flag, cmd, cmd, cmd)
	}
}
