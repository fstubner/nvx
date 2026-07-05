//go:build linux && amd64

package main

func seccompSyscall() uintptr { return 317 }

// auditArch is AUDIT_ARCH_X86_64, used to reject foreign ABIs (i386/x32) whose
// syscall numbers differ and would otherwise bypass the number-based filter.
func auditArch() uint32 { return 0xC000003E }
