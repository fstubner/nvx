//go:build linux

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

const (
	seccompSetModeFilter   = 1
	seccompFilterFlagTSync = 1

	bpfLd  = 0x00
	bpfW   = 0x00
	bpfAbs = 0x20
	bpfJmp = 0x05
	bpfJeq = 0x10
	bpfK   = 0x00
	bpfRet = 0x06
	bpfAlu = 0x04
	bpfAnd = 0x50

	// sockTypeMask isolates the base socket type from the SOCK_CLOEXEC /
	// SOCK_NONBLOCK flags that socket(2) accepts OR'd into its type argument
	// (linux/net.h SOCK_TYPE_MASK).
	sockTypeMask = 0xF

	seccompRetAllow = 0x7fff0000
	seccompRetErrno = 0x00050000 + 1 // EPERM

	afInet        = 2
	afInet6       = 10
	sockDgram     = 2
	sdOffsetNr    = 0
	sdOffsetArgs0 = 16
	sdOffsetArgs1 = 24
)

// applyLinuxNetworkSeccomp installs seccomp filters for network isolation.
// Loopback-only network namespaces block WAN TCP/UDP; seccomp adds defense in
// depth by denying inet connect and UDP socket creation in restricted modes.
func applyLinuxNetworkSeccomp(networkMode string, proxyPort int) error {
	mode := strings.ToLower(networkMode)
	switch mode {
	case "open", "":
		return nil
	case "offline", "loopback":
		return installSeccompFilter(buildOfflineNetworkFilter())
	case "proxy":
		_ = proxyPort
		return installSeccompFilter(buildProxyNetworkFilter())
	default:
		return nil
	}
}

func installSeccompFilter(filter []syscall.SockFilter) error {
	trap := seccompSyscall()
	if trap == 0 {
		return fmt.Errorf("seccomp not supported on this architecture")
	}
	if err := prctlSetNoNewPrivs(); err != nil {
		return fmt.Errorf("prctl NO_NEW_PRIVS: %w", err)
	}
	prog := syscall.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}
	_, _, errno := syscall.RawSyscall6(
		trap,
		seccompSetModeFilter,
		seccompFilterFlagTSync,
		uintptr(unsafe.Pointer(&prog)),
		0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("seccomp install: %w", errno)
	}
	return nil
}

func bpfStmt(code, k uint32) syscall.SockFilter {
	return syscall.SockFilter{Code: uint16(code), K: k}
}

func bpfJump(code, k, jt, jf uint32) syscall.SockFilter {
	return syscall.SockFilter{Code: uint16(code), K: k, Jt: uint8(jt), Jf: uint8(jf)}
}

func ldWAbs(off uint32) syscall.SockFilter {
	return bpfStmt(bpfLd|bpfW|bpfAbs, off)
}

// buildOfflineNetworkFilter denies connect() and all IPv4/IPv6 sockets (TCP and UDP).
func buildOfflineNetworkFilter() []syscall.SockFilter {
	retAllow := bpfStmt(bpfRet|bpfK, seccompRetAllow)
	retErrno := bpfStmt(bpfRet|bpfK, seccompRetErrno)
	connect := uint32(syscall.SYS_CONNECT)
	socket := uint32(syscall.SYS_SOCKET)

	return []syscall.SockFilter{
		ldWAbs(sdOffsetNr),
		bpfJump(bpfJmp|bpfJeq|bpfK, connect, 0, 1),
		retErrno,
		ldWAbs(sdOffsetNr),
		bpfJump(bpfJmp|bpfJeq|bpfK, socket, 0, 6),
		ldWAbs(sdOffsetArgs0),
		bpfJump(bpfJmp|bpfJeq|bpfK, afInet, 0, 1),
		retErrno,
		ldWAbs(sdOffsetArgs0),
		bpfJump(bpfJmp|bpfJeq|bpfK, afInet6, 0, 1),
		retErrno,
		retAllow,
	}
}

// buildProxyNetworkFilter denies IPv4/IPv6 UDP socket creation; TCP is allowed
// for loopback proxy use while the network namespace blocks non-loopback routes.
// AF_UNIX is never refused -- it is not a network socket, and denying it breaks
// ordinary local IPC.
//
// The predecessor of this filter was inverted: it loaded args[0] (domain),
// branched on it, and then compared the *same* accumulator against SOCK_DGRAM
// whenever the domain test fell through, because the false branch skipped the
// instruction that reloads args[1]. Net effect on a real kernel: IPv4 TCP denied
// (breaking the very proxy this mode exists to serve), IPv4 UDP allowed, and
// AF_UNIX denied. Any edit here must keep
// sandbox_network_seccomp_linux_test.go's real-kernel probes green -- hand-written
// cBPF jump offsets are not reviewable by inspection alone.
func buildProxyNetworkFilter() []syscall.SockFilter {
	retAllow := bpfStmt(bpfRet|bpfK, seccompRetAllow)
	retErrno := bpfStmt(bpfRet|bpfK, seccompRetErrno)
	socket := uint32(syscall.SYS_SOCKET)

	// Jump targets are index+1+offset; the two returns sit at [8] (deny) and
	// [9] (allow).
	return []syscall.SockFilter{
		/* 0 */ ldWAbs(sdOffsetNr),
		/* 1 */ bpfJump(bpfJmp|bpfJeq|bpfK, socket, 0, 7), // not socket() -> allow
		/* 2 */ ldWAbs(sdOffsetArgs1), // type
		/* 3 */ bpfStmt(bpfAlu|bpfAnd|bpfK, sockTypeMask), // strip SOCK_CLOEXEC/NONBLOCK
		/* 4 */ bpfJump(bpfJmp|bpfJeq|bpfK, sockDgram, 0, 4), // not datagram -> allow
		/* 5 */ ldWAbs(sdOffsetArgs0), // domain
		/* 6 */ bpfJump(bpfJmp|bpfJeq|bpfK, afInet, 1, 0), // AF_INET datagram -> deny
		/* 7 */ bpfJump(bpfJmp|bpfJeq|bpfK, afInet6, 0, 1), // AF_INET6 datagram -> deny
		/* 8 */ retErrno,
		/* 9 */ retAllow,
	}
}
