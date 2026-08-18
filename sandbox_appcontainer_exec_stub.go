//go:build !windows

package main

func runAppContainerExecChild(workDir, networkMode, egressSocket, cmdPath string, args []string) int {
	LogError("internal __appcontainer-exec is only available on Windows")
	return 1
}
