//go:build !linux

package nvx

import "os/exec"

// killChildWhenParentDies does nothing off Linux. macOS has no kill-on-parent-
// death primitive and Windows uses a job object instead.
func killChildWhenParentDies(cmd *exec.Cmd) {}
