//go:build linux && arm64

package nvx

func prctlSyscall() uintptr { return 167 }

func setnsSyscall() uintptr { return 268 }
