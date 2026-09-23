//go:build linux && arm64

package nvx

func seccompSyscall() uintptr { return 277 }

// seccompAuditArch is AUDIT_ARCH_AARCH64, what seccomp_data.arch holds for a native call.
func seccompAuditArch() uint32 { return 0xC00000B7 }
