package nvx

import (
	"os/exec"
	"syscall"
)

// childExitCode is the exit code nvx reports for a finished child.
//
// ExitError.ExitCode is -1 when a signal ended the child, and os.Exit(-1) is
// 255 -- so a runtime killed by SIGTERM looked like an ordinary failure, and
// `echo $?` showed 255 where every shell shows 143. A signal death is reported
// as 128 plus the signal number, the same convention the supervisor and
// npm/nvx/bin/nvx.js use.
func childExitCode(err *exec.ExitError) int {
	if ws, ok := err.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return err.ExitCode()
}
