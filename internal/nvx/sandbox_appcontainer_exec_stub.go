//go:build !windows

package nvx

func runAppContainerExecChild(_ supervisorExecArgs) int {
	LogError("internal __appcontainer-exec is only available on Windows")
	return 1
}
