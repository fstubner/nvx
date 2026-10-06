//go:build linux && amd64

package nvx

func prctlSyscall() uintptr { return 157 }

// setnsSyscall is setns(2). The syscall package has no number for it here.
func setnsSyscall() uintptr { return 308 }
