//go:build linux && !amd64 && !arm64

package nvx

import "syscall"

// The syscall package carries the right number for every Linux architecture Go
// builds for. This returned amd64's 157 until 2026-09-26, and 157 is setsid on
// riscv64 and loong64, so PR_SET_NO_NEW_PRIVS was never set there and no error
// said so.
func prctlSyscall() uintptr { return syscall.SYS_PRCTL }
