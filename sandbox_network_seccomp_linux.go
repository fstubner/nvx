//go:build linux

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

const (
	seccompSetModeFilter  = 1
	seccompFilterFlagTSync = 1

	bpfLd  = 0x00
	bpfW   = 0x00
	bpfAbs = 0x20
	bpfJmp = 0x05
	bpfJeq = 0x10
	bpfK   = 0x00
	bpfRet = 0x06

	seccompRetAllow = 0x7fff0000
	seccompRetErrno = 0x00050000 + 1 // EPERM

	afInet  = 2
	afInet6 = 10

	sdOffsetNr    = 0
	sdOffsetArgs0 = 16
)

// applyLinuxNetworkSeccomp installs seccomp filters for network isolation.
// Basic seccomp cannot inspect connect() destination addresses; proxy mode
// relies on the parent egress proxy for HTTP(S) stacks. Offline/loopback
// modes block connect() and new IPv4/IPv6 socket creation.
func applyLinuxNetworkSeccomp(networkMode string, proxyPort int) error {
	mode := strings.ToLower(networkMode)
	switch mode {
	case "open", "":
		return nil
	case "offline", "loopback":
		return installInetSocketBlockSeccomp()
	case "proxy":
		_ = proxyPort
		// Compliant HTTP(S) stacks use HTTP_PROXY; raw TCP bypass is documented in README.
		return nil
	default:
		return nil
	}
}

func installInetSocketBlockSeccomp() error {
	if err := prctlNoNewPrivs(); err != nil {
		return fmt.Errorf("prctl NO_NEW_PRIVS: %w", err)
	}

	filter := buildInetSocketBlockFilter()
	prog := syscall.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_SECCOMP,
		seccompSetModeFilter,
		uintptr(unsafe.Pointer(&prog)),
		seccompFilterFlagTSync,
	)
	if errno != 0 {
		return fmt.Errorf("seccomp install: %w", errno)
	}
	return nil
}

func prctlNoNewPrivs() error {
	const prSetNoNewPrivs = 38
	_, _, errno := syscall.Syscall(prctlSyscall(), prSetNoNewPrivs, 1, 0, 0)
	if errno != 0 && errno != syscall.EINVAL {
		return errno
	}
	return nil
}

func bpfStmt(code, k uint32) syscall.SockFilter {
	return syscall.SockFilter{Code: code, K: k}
}

func bpfJump(code, k, jt, jf uint32) syscall.SockFilter {
	return syscall.SockFilter{Code: code, K: k, Jt: jt, Jf: jf}
}

// buildInetSocketBlockFilter denies connect() and socket(AF_INET|AF_INET6).
func buildInetSocketBlockFilter() []syscall.SockFilter {
	ldWAbs := func(off uint32) syscall.SockFilter {
		return bpfStmt(bpfLd|bpfW|bpfAbs, off)
	}
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
