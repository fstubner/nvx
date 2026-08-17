//go:build !linux

package main

func runLandlockExecChild(guestHome, workDir, nvxHome, networkMode, shimCommand, egressSocket, cmdPath string, args []string) int {
	LogError("internal __landlock-exec is only available on Linux")
	return 1
}
