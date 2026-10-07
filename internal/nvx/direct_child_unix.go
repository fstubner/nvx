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

// directExecCommand builds the uncontained launch of path. Only Windows has
// batch files to treat differently, see the Windows version.
func directExecCommand(path string, args []string) (*exec.Cmd, error) {
	// #nosec G702 -- running the runtime nvx resolved for this project is what
	// a shim is for. path comes from nvx's own resolution, not from input.
	return exec.Command(path, args...), nil
}
