//go:build linux

package nvx

import "syscall"

// applyLinuxTerminalSeccomp stops a contained process from typing into the
// terminal it shares with nvx.
//
// The target gets the user's terminal as its stdin, and as its controlling
// terminal, since it stays in nvx's session. ioctl(TIOCSTI) pushes a byte into
// that terminal's input queue. When nvx exits the user's shell reads the queue
// as typed input, so a postinstall script could leave a command and Enter
// behind, and it would run as the user outside the sandbox. This is the class of
// bubblewrap's CVE-2017-5226. TIOCLINUX does the same on a Linux virtual console
// by pasting a selection.
//
// From 6.2 the kernel refuses TIOCSTI when dev.tty.legacy_tiocsti is 0. Below
// 6.2, or with the sysctl at 1, it allows it. Ubuntu 22.04's 5.15 is one of
// those kernels, and nvx runs there.
//
// A filter of its own, so it is installed in every network mode. Open mode gets
// no network filter. installSeccompFilter applies it to every thread of the
// supervisor, and the target inherits it when it starts.
func applyLinuxTerminalSeccomp() error {
	return installSeccompFilter(buildTerminalInputFilter())
}

// buildTerminalInputFilter refuses ioctl with TIOCSTI or TIOCLINUX with EPERM.
//
// The kernel takes the request as an unsigned int, so it runs 0x100005412 as
// TIOCSTI. Only the low 32 bits are compared for that reason. A filter matching
// all 64 bits let such a request through in Flatpak (CVE-2019-10063). The load
// at sdOffsetArgs1 reads the low half because amd64 and arm64 are
// little-endian.
//
// A call through another ABI is refused outright, as seccompPrologue does for
// the network filters. Its ioctl has another number and would pass the check.
//
// Jump targets are index+1+offset.
func buildTerminalInputFilter() []syscall.SockFilter {
	retAllow := bpfStmt(bpfRet|bpfK, seccompRetAllow)
	retDeny := bpfStmt(bpfRet|bpfK, seccompRetErrno)
	return []syscall.SockFilter{
		/* 0 */ ldWAbs(sdOffsetArch),
		/* 1 */ bpfJump(bpfJmp|bpfJeq|bpfK, seccompAuditArch(), 1, 0), // native -> 3
		/* 2 */ retDeny,
		/* 3 */ ldWAbs(sdOffsetNr),
		/* 4 */ bpfJump(bpfJmp|bpfJge|bpfK, x32SyscallBit, 5, 0), // x32 -> 10
		/* 5 */ bpfJump(bpfJmp|bpfJeq|bpfK, uint32(syscall.SYS_IOCTL), 0, 3), // not ioctl -> 9
		/* 6 */ ldWAbs(sdOffsetArgs1), // the request
		/* 7 */ bpfJump(bpfJmp|bpfJeq|bpfK, syscall.TIOCSTI, 2, 0), // -> 10
		/* 8 */ bpfJump(bpfJmp|bpfJeq|bpfK, syscall.TIOCLINUX, 1, 0), // -> 10
		/* 9 */ retAllow,
		/* 10 */ retDeny,
	}
}
