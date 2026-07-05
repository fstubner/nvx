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

	// sockTypeMask isolates the base socket type from OR'd-in flags
	// (SOCK_CLOEXEC/SOCK_NONBLOCK), so `socket(AF_INET, SOCK_DGRAM|SOCK_CLOEXEC)`
	// cannot evade a type check.
	sockTypeMask = 0xFF

	seccompRetAllow = 0x7fff0000
	seccompRetErrno = 0x00050000 + 1 // EPERM

	afInet        = 2
	afInet6       = 10
	sockDgram     = 2
	sdOffsetNr    = 0
	sdOffsetArch  = 4
	sdOffsetArgs0 = 16
	sdOffsetArgs1 = 24
)

// archGuard rejects any syscall issued under a foreign ABI (e.g. i386/x32 on
// x86-64), whose syscall numbers differ from the native table this filter
// matches against. Without it, a child can bypass the filter by switching ABI.
// It must be prepended to every filter; it consumes 3 instructions.
func archGuard() []syscall.SockFilter {
	return []syscall.SockFilter{
		ldWAbs(sdOffsetArch),
		bpfJump(bpfJmp|bpfJeq|bpfK, auditArch(), 1, 0), // native arch -> skip the deny
		bpfStmt(bpfRet|bpfK, seccompRetErrno),          // foreign ABI -> EPERM
	}
}

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
		uintptr(unsafe.Pointer(&prog)),
		seccompFilterFlagTSync,
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

	return append(archGuard(), []syscall.SockFilter{
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
	}...)
}

// buildProxyNetworkFilter denies IPv4/IPv6 UDP socket creation; TCP is allowed
// for loopback proxy use while the network namespace blocks non-loopback routes.
// Instruction offsets are hand-verified and covered by a BPF-interpreter test.
//
//	0: A = nr
//	1: nr==socket ? next : ALLOW(+7)
//	2: A = args0 (family)
//	3: family==AF_INET  ? TYPECHK(+1) : next
//	4: family==AF_INET6 ? TYPECHK(+0) : ALLOW(+4)   (e.g. AF_UNIX)
//	5: A = args1 (type)          [TYPECHK]
//	6: A &= 0xFF                 (strip SOCK_CLOEXEC/NONBLOCK)
//	7: type==SOCK_DGRAM ? DENY(+0) : ALLOW(+1)
//	8: ret ERRNO                 [DENY]
//	9: ret ALLOW                 [ALLOW]
func buildProxyNetworkFilter() []syscall.SockFilter {
	retAllow := bpfStmt(bpfRet|bpfK, seccompRetAllow)
	retErrno := bpfStmt(bpfRet|bpfK, seccompRetErrno)
	socket := uint32(syscall.SYS_SOCKET)

	return append(archGuard(), []syscall.SockFilter{
		ldWAbs(sdOffsetNr),                           // 0
		bpfJump(bpfJmp|bpfJeq|bpfK, socket, 0, 7),    // 1 -> ALLOW if not socket
		ldWAbs(sdOffsetArgs0),                        // 2
		bpfJump(bpfJmp|bpfJeq|bpfK, afInet, 1, 0),    // 3 -> TYPECHK
		bpfJump(bpfJmp|bpfJeq|bpfK, afInet6, 0, 4),   // 4 -> TYPECHK / ALLOW
		ldWAbs(sdOffsetArgs1),                        // 5 TYPECHK
		bpfStmt(bpfAlu|bpfAnd|bpfK, sockTypeMask),    // 6
		bpfJump(bpfJmp|bpfJeq|bpfK, sockDgram, 0, 1), // 7 -> DENY / ALLOW
		retErrno, // 8 DENY (UDP)
		retAllow, // 9 ALLOW (TCP, AF_UNIX, non-socket)
	}...)
}
