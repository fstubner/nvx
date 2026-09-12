//go:build linux && !amd64 && !arm64

package nvx

func prctlSyscall() uintptr { return 157 }
