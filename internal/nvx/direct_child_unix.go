//go:build !windows

package nvx

import "os/exec"

// runDirectChild runs an uncontained runtime to completion.
//
// nvx stays in the way rather than replacing itself with the runtime, because
// runShim records the run and reclaims stale sandboxes after the child exits.
// So it has to do what exec would have done for free: pass termination on to the
// child, and on Linux have the kernel kill the child if nvx is SIGKILLed. Before
// this the child was started with plain Start and Wait, `kill <nvx>` left node
// running, and a node ended by SIGTERM was reported as exit 255 rather than 143.
func runDirectChild(cmd *exec.Cmd) error {
	killChildWhenParentDies(cmd)
	return runChildForwardingSignals(cmd)
}
