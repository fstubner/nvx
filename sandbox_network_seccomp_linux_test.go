//go:build linux

package main

import (
	"syscall"
	"testing"
)

// runBPF is a minimal classic-BPF interpreter over the subset of opcodes nvx's
// seccomp filters use (LD|W|ABS, ALU|AND|K, JMP|JEQ|K, RET|K). `mem` maps a
// byte offset in seccomp_data to the 32-bit word stored there. It returns the
// RET action value. This lets us assert filter *semantics* so an inverted or
// mis-offset filter fails the build instead of shipping silently.
func runBPF(t *testing.T, filter []syscall.SockFilter, mem map[uint32]uint32) uint32 {
	t.Helper()
	var A uint32
	for pc := 0; pc < len(filter); {
		ins := filter[pc]
		switch int(ins.Code) {
		case bpfLd | bpfW | bpfAbs:
			A = mem[ins.K]
			pc++
		case bpfAlu | bpfAnd | bpfK:
			A &= ins.K
			pc++
		case bpfJmp | bpfJeq | bpfK:
			if A == ins.K {
				pc += 1 + int(ins.Jt)
			} else {
				pc += 1 + int(ins.Jf)
			}
		case bpfRet | bpfK:
			return ins.K
		default:
			t.Fatalf("unsupported BPF opcode 0x%x at pc=%d", ins.Code, pc)
		}
	}
	t.Fatal("filter fell through without RET")
	return 0
}

// mkData builds a seccomp_data word map for a syscall invocation.
func mkData(nr uint32, arch uint32, family uint32, sockType uint32) map[uint32]uint32 {
	return map[uint32]uint32{
		sdOffsetNr:    nr,
		sdOffsetArch:  arch,
		sdOffsetArgs0: family,
		sdOffsetArgs1: sockType,
	}
}

func TestProxyNetworkFilterSemantics(t *testing.T) {
	filter := buildProxyNetworkFilter()
	native := auditArch()
	socket := uint32(syscall.SYS_SOCKET)
	connect := uint32(syscall.SYS_CONNECT)

	const (
		sockStream = 1
		afUnix     = 1
	)
	cloexec := uint32(0x80000)

	cases := []struct {
		name     string
		data     map[uint32]uint32
		wantDeny bool
	}{
		// The core intent: UDP denied, TCP allowed.
		{"AF_INET UDP -> DENY", mkData(socket, native, afInet, sockDgram), true},
		{"AF_INET6 UDP -> DENY", mkData(socket, native, afInet6, sockDgram), true},
		{"AF_INET TCP -> ALLOW", mkData(socket, native, afInet, sockStream), false},
		{"AF_INET6 TCP -> ALLOW", mkData(socket, native, afInet6, sockStream), false},
		{"AF_UNIX -> ALLOW", mkData(socket, native, afUnix, sockStream), false},
		// Flag-masking: UDP|CLOEXEC must still be denied.
		{"AF_INET UDP|CLOEXEC -> DENY", mkData(socket, native, afInet, sockDgram|cloexec), true},
		// Non-socket syscalls pass; connect is allowed in proxy mode.
		{"connect -> ALLOW", mkData(connect, native, 0, 0), false},
		// Foreign ABI (i386) must be blocked by the arch guard regardless.
		{"foreign arch socket -> DENY", mkData(socket, 0x40000003, afInet, sockStream), true},
	}
	for _, c := range cases {
		got := runBPF(t, filter, c.data)
		deny := got == seccompRetErrno
		if deny != c.wantDeny {
			t.Errorf("%s: got action 0x%x (deny=%v), want deny=%v", c.name, got, deny, c.wantDeny)
		}
	}
}

func TestOfflineNetworkFilterSemantics(t *testing.T) {
	filter := buildOfflineNetworkFilter()
	native := auditArch()
	socket := uint32(syscall.SYS_SOCKET)
	connect := uint32(syscall.SYS_CONNECT)
	const afUnix = 1

	cases := []struct {
		name     string
		data     map[uint32]uint32
		wantDeny bool
	}{
		{"connect -> DENY", mkData(connect, native, 0, 0), true},
		{"AF_INET socket -> DENY", mkData(socket, native, afInet, 1), true},
		{"AF_INET6 socket -> DENY", mkData(socket, native, afInet6, 1), true},
		{"AF_UNIX socket -> ALLOW", mkData(socket, native, afUnix, 1), false},
		{"foreign arch -> DENY", mkData(socket, 0x40000003, afInet, 1), true},
	}
	for _, c := range cases {
		got := runBPF(t, filter, c.data)
		deny := got == seccompRetErrno
		if deny != c.wantDeny {
			t.Errorf("%s: got 0x%x (deny=%v), want deny=%v", c.name, got, deny, c.wantDeny)
		}
	}
}
