//go:build windows

package nvx

import "os/exec"

// runChildForwardingSignals is plain cmd.Run on Windows, and is never reached.
//
// Its only callers are the macOS launches. sandbox_seatbelt.go builds on every
// platform and refuses to run anywhere but macOS before it gets here, so this
// copy exists for the build alone.
func runChildForwardingSignals(cmd *exec.Cmd) error {
	return cmd.Run()
}
