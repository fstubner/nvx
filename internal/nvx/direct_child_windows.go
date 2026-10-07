//go:build windows

package nvx

import (
	"os/exec"
	"sync/atomic"
	"syscall"
)

// runDirectChild runs an uncontained runtime to completion.
//
// Start and Wait rather than Run, so the hangup watchdog has something to stop,
// and with superviseDirectChild as the backstop that reaps the child if nvx stops
// waiting on it by any route -- the hangup watchdog is the polite one, and this
// also covers nvx being killed outright, which is how the leak it fixes was
// actually produced: nvx was gone a second after its client, before the
// watchdog's first poll, and the child ran on for ever.
//
// The tree is reaped only when nvx ended the command. A command that exits on
// its own keeps whatever it deliberately left running, as it would without nvx.
func runDirectChild(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	var ended atomic.Bool
	finish := superviseDirectChild(cmd.Process.Pid)
	defer func() { finish(ended.Load()) }()

	setActiveChildKiller(func() {
		ended.Store(true)
		_ = cmd.Process.Kill()
	})
	defer setActiveChildKiller(nil)

	return cmd.Wait()
}

// directExecCommand builds a launch of path outside the sandbox: the shim's
// uncontained run, a run nested in a sandbox session (execBareCommand), and the
// docker CLI. A batch file is started through cmd.exe with its arguments
// escaped for cmd.exe, as a contained launch does (see windowsBatchLaunch).
// exec.Command escapes arguments the way the C runtime reads them, and Windows
// hands a batch file's command line to cmd.exe, which does not. So an argument
// such as x&echo.INJECTED>file ran a second command, as the user, outside the
// sandbox.
//
// Args still names the batch file and its arguments, so the caller can tell
// what runs. cmd.Path is cmd.exe, and the command line it gets is
// SysProcAttr.CmdLine, which Windows is given in place of Args.
func directExecCommand(path string, args []string) (*exec.Cmd, error) {
	if !isWindowsBatchFile(path) {
		// #nosec G702 -- path is a program nvx resolved itself, the runtime a
		// shim exists to run or the docker CLI. It does not come from input.
		return exec.Command(path, args...), nil
	}
	exe, line, err := windowsBatchLaunch(path, args)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe)
	cmd.Args = append([]string{path}, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	return cmd, nil
}
