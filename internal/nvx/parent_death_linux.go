//go:build linux

package nvx

import (
	"os/exec"
	"syscall"
)

// killChildWhenParentDies has the kernel SIGKILL cmd's process when nvx goes
// away, including by SIGKILL, which no handler can catch.
//
// Only meaningful for a child started from a thread that stays alive for the
// child's whole life; see startChildForwardingSignals.
func killChildWhenParentDies(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
