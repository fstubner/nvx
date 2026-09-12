//go:build !linux

package nvx

func runLandlockExecChild(_ supervisorExecArgs) int {
	LogError("internal __landlock-exec is only available on Linux")
	return 1
}
