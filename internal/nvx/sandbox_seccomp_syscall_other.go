//go:build linux && !amd64 && !arm64

package nvx

func seccompSyscall() uintptr { return 0 }
