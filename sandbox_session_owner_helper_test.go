package main

import (
	"os/exec"
	"runtime"
	"testing"
)

// helperExitCommand returns a command that exits immediately, used to obtain a
// pid that is genuinely dead rather than merely unlikely to exist.
func helperExitCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "exit", "0")
	}
	return exec.Command("sh", "-c", "exit 0")
}
