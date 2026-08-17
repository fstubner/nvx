//go:build linux

package main

// Landlock syscall numbers.
//
// These are deliberately NOT split per-architecture. Landlock was added to every
// architecture's syscall table simultaneously in Linux 5.13 with the same three
// numbers, so a per-arch copy buys nothing and costs correctness: the arm64 copy
// this replaces returned 445/446/447 -- one too high on every entry -- which made
// landlock_restrict_self invoke 447 (memfd_secret) and reported the failure as
// "landlock not supported (kernel 5.13+ required)", pointing the reader at their
// kernel instead of at a wrong constant. One definition makes that divergence
// impossible rather than merely detectable.
//
// Source of truth: include/uapi/asm-generic/unistd.h (5.13+), which x86_64's own
// table also matches for these entries.
//
// Note on scope: architectures whose tables carry a base offset (MIPS o32/n64/n32)
// would need their own values. nvx does not ship MIPS builds -- release targets are
// amd64 and arm64 (see install.sh) -- and the per-arch files this replaces were
// already wrong for MIPS, so nothing is lost. Add a tagged file if that changes.
//
// prctl and seccomp DO differ per architecture and keep their own files.
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446
)

func landlockSyscallCreateRuleset() uintptr { return sysLandlockCreateRuleset }
func landlockSyscallAddRule() uintptr       { return sysLandlockAddRule }
func landlockSyscallRestrictSelf() uintptr  { return sysLandlockRestrictSelf }
