//go:build linux && amd64

package nvx

func seccompSyscall() uintptr { return 317 }
