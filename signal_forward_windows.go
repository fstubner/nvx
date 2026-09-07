//go:build windows

package main

import "os/exec"

// runChildForwardingSignals is plain cmd.Run on Windows.
//
// The unix build forwards interrupt and terminate to the child so a signalled
// nvx does not leave it running. Windows needs no equivalent: contained
// launches are assigned to a job object created with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so the OS tears the whole child tree down
// when nvx's handle closes, however nvx dies. This exists so the shared launch
// code can call one name.
func runChildForwardingSignals(cmd *exec.Cmd) error {
	return cmd.Run()
}
