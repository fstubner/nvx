//go:build linux && amd64

package nvx

func seccompSyscall() uintptr { return 317 }

// seccompAuditArch is AUDIT_ARCH_X86_64, what seccomp_data.arch holds for a native call.
func seccompAuditArch() uint32 { return 0xC000003E }
