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

// directExecCommand builds a launch of path outside the sandbox. Only Windows
// has batch files to treat differently, see the Windows version.
func directExecCommand(path string, args []string) (*exec.Cmd, error) {
	// #nosec G702 -- path is a program nvx resolved itself, the runtime a
	// shim exists to run or the docker CLI. It does not come from input.
	return exec.Command(path, args...), nil
}
