//go:build !linux

package main

func runLandlockExecChild(_ supervisorExecArgs) int {
	LogError("internal __landlock-exec is only available on Linux")
	return 1
}
