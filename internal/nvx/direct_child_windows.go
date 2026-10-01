//go:build windows

package nvx

import "os/exec"

// runDirectChild runs an uncontained runtime to completion.
//
// Start and Wait rather than Run, so the hangup watchdog has something to stop,
// and with superviseDirectChild as the backstop that reaps the child if nvx stops
// waiting on it by any route -- the hangup watchdog is the polite one, and this
// also covers nvx being killed outright, which is how the leak it fixes was
// actually produced: nvx was gone a second after its client, before the
// watchdog's first poll, and the child ran on for ever.
func runDirectChild(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	defer superviseDirectChild(cmd.Process.Pid)()

	setActiveChildKiller(func() { _ = cmd.Process.Kill() })
	defer setActiveChildKiller(nil)

	return cmd.Wait()
}
