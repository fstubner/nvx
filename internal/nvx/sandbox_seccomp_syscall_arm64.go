//go:build linux && arm64

package nvx

func seccompSyscall() uintptr { return 277 }
