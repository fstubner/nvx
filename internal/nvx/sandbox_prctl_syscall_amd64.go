//go:build linux && amd64

package nvx

func prctlSyscall() uintptr { return 157 }
