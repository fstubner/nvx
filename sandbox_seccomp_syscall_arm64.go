//go:build linux && arm64

package main

func seccompSyscall() uintptr { return 277 }

// auditArch is AUDIT_ARCH_AARCH64, used to reject foreign ABIs whose syscall
// numbers differ and would otherwise bypass the number-based filter.
func auditArch() uint32 { return 0xC00000B7 }
