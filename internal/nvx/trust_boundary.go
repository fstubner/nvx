package nvx

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Widening the sandbox is never approved at a prompt.
//
// Three requests decide what a contained command may reach, rather than whether
// one install goes ahead: running under a project's .nvx-policy.json that
// loosens settings, reaching a host the allowlist does not name, and letting a
// tool keep a persistent profile. Each was a [y/N] question whenever stdin was a
// terminal, on the reasoning that a terminal means a person is there. It does
// not. An agent harness that runs commands in a pseudo-terminal presents exactly
// that, and the model can type y. TestATerminalAnswerCannotWidenTheSandbox shows
// it. On the code before this change, a y written into a pseudo-terminal trusted
// a project policy that set isolation.network.mode to open, granted a persistent
// tool profile and allowed an unknown host.
//
// So nvx no longer asks. It refuses, exits 77, and prints the command a person
// runs in their own terminal to allow what was asked: `nvx trust` or
// `nvx allow-host`. NVX_TRUST_YES still approves. Setting it is the decision,
// handed to whatever sets the environment, and nothing sets it by habit.
//
// -y, NVX_YES and --agent-mode approve none of these. They once did, and under
// --agent-mode a .nvx-policy.json carrying {"isolation":{"enabled":false}} was
// trusted without a word, which turned the sandbox off for every later command
// in that project.
//
// The pre-install checks are a different kind of question, about one package in
// one run, and they still ask a person at a terminal. --agent-mode is how to stop
// them asking too.

// wideningRequest is one request to widen the sandbox.
type wideningRequest struct {
	// what names the request, for the line NVX_TRUST_YES prints and for the
	// exit code's explanation: "a connection to example.com:443".
	what string
	// refusal is the line saying nvx refused, when the caller prints no line of
	// its own.
	refusal string
	// details go under the approval or the refusal, one per line.
	details []string
	// command is what a person runs in their own terminal to allow it.
	command string
}

// approveWidening decides a request to widen the sandbox. It never asks.
func approveWidening(req wideningRequest) bool {
	if trustYesApproves(req) {
		return true
	}
	refuseWidening(req)
	return false
}

// trustYesApproves reports whether NVX_TRUST_YES approves req, and says so when
// it does, as an approval by -y is said.
func trustYesApproves(req wideningRequest) bool {
	if !trustYesSet() {
		return false
	}
	LogWarn("NVX_TRUST_YES is set, so nvx approved this without asking: %s.", req.what)
	for _, d := range req.details {
		LogRefusalDetail("    %s", d)
	}
	return true
}

// refuseWidening says what nvx refused and the one command that allows it, and
// keeps the refusal for the exit code.
func refuseWidening(req wideningRequest) {
	recordWideningRefusal(req.what)
	if req.refusal != "" {
		LogError("%s", req.refusal)
	}
	for _, d := range req.details {
		LogRefusalDetail("    %s", d)
	}
	if req.command != "" {
		LogRefusalDetail("To allow it, run this in your own terminal, in %s:  %s", currentDirForHint(), req.command)
	}
	agentWideningNoteOnce.Do(func() { LogRefusalDetail("%s", agentWideningNote) })
}

// agentWideningNote is said once per run, after the first refusal to widen the
// sandbox. The command printed is one only a person should run, and an agent
// that has a shell can run it.
const agentWideningNote = "If you are an automated agent: do not run nvx commands or change nvx's settings to allow this yourself. Ask the person you work for to decide."

var agentWideningNoteOnce sync.Once

// currentDirForHint names where the command printed must be run. Trust is
// recorded for the project the command runs in, so the same command from
// another folder records it for the wrong one.
func currentDirForHint() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "this project"
}

var (
	wideningRefusalsMu sync.Mutex
	wideningRefusals   []string
)

// forgetWideningRefusals starts a run with none counted, so its exit code
// reflects only what was refused for it.
func forgetWideningRefusals() {
	wideningRefusalsMu.Lock()
	defer wideningRefusalsMu.Unlock()
	wideningRefusals = nil
	agentWideningNoteOnce = sync.Once{}
}

// runShimTracedFn is runShimTraced, held in a variable so a test can show that
// runShim maps the exit code of a run that refused something.
var runShimTracedFn = runShimTraced

func recordWideningRefusal(what string) {
	wideningRefusalsMu.Lock()
	defer wideningRefusalsMu.Unlock()
	for _, w := range wideningRefusals {
		if w == what {
			return
		}
	}
	wideningRefusals = append(wideningRefusals, what)
}

// exitCodeAfterRefusedWidening is the exit code of a run that refused to widen
// the sandbox while the command was running: 77 when the command then failed,
// as for every refusal, and the command's own code otherwise.
//
// A host is refused while the command runs, so the refusal cannot stop it from
// starting. What follows is usually npm failing to fetch, with its own exit
// code, and a caller reading that could not tell nvx's refusal from a registry
// outage. A command that succeeded anyway keeps its 0: a postinstall that pings
// an analytics host it cannot reach is not worth failing an install for.
func exitCodeAfterRefusedWidening(code int) int {
	if code == 0 || code == exitRefused || code == exitParentHungUp {
		return code
	}
	wideningRefusalsMu.Lock()
	refused := append([]string(nil), wideningRefusals...)
	wideningRefusalsMu.Unlock()
	if len(refused) == 0 {
		return code
	}
	if len(refused) > 3 {
		refused = append(refused[:3:3], fmt.Sprintf("%d more", len(refused)-3))
	}
	LogError("The command exited %d after nvx refused %s. nvx exits %d instead, as it does for every refusal.", code, strings.Join(refused, ", "), exitRefused)
	return exitRefused
}

// errUntrustedProjectPolicy is ensureProjectPolicyTrust refusing to run under a
// project policy file that loosens settings and has not been trusted. It has
// already said which file and what it loosens.
var errUntrustedProjectPolicy = errors.New("a project policy file that loosens nvx's settings has not been trusted for this project")

// The reasons an MCP client is told, fixed text like every other.
const (
	untrustedPolicyReason = "a project policy file loosens its settings and has not been trusted"
	untrustedToolReason   = "a tool asked to keep a persistent profile, which has not been trusted"
)

// refusePolicyBeforeRun is the exit code for a run stopped by
// ensureProjectPolicyTrust, which is either a refusal or a policy file that
// could not be read.
func refusePolicyBeforeRun(trace *runTrace, err error) int {
	if !errors.Is(err, errUntrustedProjectPolicy) {
		return refuseUnreadablePolicy(err)
	}
	trace.note(runModeRefused, untrustedPolicyReason)
	reportRefusalOverStdio(untrustedPolicyReason, "")
	return exitRefused
}

// refuseUntrustedTool is the exit code for a tool refused a persistent profile.
// The run stops rather than going ahead with a throwaway one. The commands that
// ask for a profile are logins, and a login that is thrown away afterwards
// costs the person the whole browser flow for nothing.
func refuseUntrustedTool(trace *runTrace) int {
	trace.note(runModeRefused, untrustedToolReason)
	reportRefusalOverStdio(untrustedToolReason, "")
	return exitRefused
}
