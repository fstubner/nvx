//go:build windows

package main

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Deciding whether the process on the other end of a --connect tunnel is one
// this sandbox is actually running.
//
// It has to be asked, because an AppContainer's loopback is not private. Windows
// permits loopback WITHIN a package, and every nvx sandbox shares one package
// identity (stableSandboxProfile) -- only the capability SID is per-project. So a
// TCP listener the supervisor runs inside the container is reachable from every
// other nvx sandbox on the machine, measured 2026-08-28: sandbox B, a different
// project with no grant of its own, read the service sandbox A had been granted.
//
// This is not a new hazard, it is a known one. The egress relay has the same
// exposure and defends itself with a per-session proxy credential -- see the note
// on EgressProxy.token, and the acceptance pass of 2026-08-19 that found a
// sibling sandbox borrowing another project's allowlist by port-scanning
// loopback. A credential works there because HTTP has somewhere to put one.
// A --connect tunnel carries whatever protocol the tool speaks, so there is no
// header to add without breaking the transparency that is the whole feature.
//
// So the peer is identified instead of challenged. Every process this sandbox
// launches is in the session's Job Object (see createReapingJob); a process from
// another sandbox is in a different one. Job membership is inherited, so this
// covers anything the target spawns, however deep.
//
// The check runs in the PARENT, and it has to: GetExtendedTcpTable is
// ACCESS_DENIED inside an AppContainer (measured -- rc=5 from the sizing call),
// so the supervisor cannot resolve a socket to a PID even for itself. The
// supervisor reports the port it saw; the parent, outside the container, resolves
// it and decides. The supervisor is nvx's own binary and the only thing that can
// reach the tunnel socket, which lives in a guest home ACL'd to this project's
// capability alone.

var (
	iphlpapi                = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procIsProcessInJob      = modKernel32.NewProc("IsProcessInJob")
	procOpenProcessForQuery = modKernel32.NewProc("OpenProcess")
)

const (
	afINet = 2
	// TCP_TABLE_OWNER_PID_CONNECTIONS: established connections with owning PIDs.
	tcpTableOwnerPidConnections = 4
	processQueryLimitedInfo     = 0x1000
	// 127.0.0.1 as the table stores it: network byte order read as a
	// little-endian uint32, so the octets appear reversed.
	loopbackAddrLE = 0x0100007F
)

// mibTCPRowOwnerPID mirrors MIB_TCPROW_OWNER_PID. Ports and addresses are in
// network byte order; the port occupies the low two bytes of a 32-bit field.
type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

func netPort(v uint32) uint16 {
	return uint16(v&0xff)<<8 | uint16((v>>8)&0xff)
}

// ownerOfLoopbackConnection returns the PID that owns the loopback connection
// from srcPort to dstPort. Both ends are matched, not just the source port: a
// loopback connection puts two rows in the table (the client's and the
// listener's), and only the client's identifies the peer.
func ownerOfLoopbackConnection(srcPort, dstPort uint16) (uint32, error) {
	var size uint32
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0,
		afINet, tcpTableOwnerPidConnections, 0)
	if size == 0 {
		return 0, fmt.Errorf("TCP table size query returned nothing")
	}
	buf := make([]byte, size)
	rc, _, err := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)), 0, afINet, tcpTableOwnerPidConnections, 0)
	if rc != 0 {
		return 0, fmt.Errorf("GetExtendedTcpTable: rc=%d (%v)", rc, err)
	}

	return ownerInTCPTable(buf, srcPort, dstPort)
}

// ownerInTCPTable scans a MIB_TCPTABLE_OWNER_PID buffer for the loopback
// connection from srcPort to dstPort.
//
// Split from the syscall so a test can hand it a table containing a decoy: a
// non-loopback connection using the same two port numbers. Nothing else here can
// produce one on demand, and without it a test cannot tell this scan from one
// that matches ports alone -- which is exactly the mistake this must not make,
// since it decides which process is allowed through a tunnel.
func ownerInTCPTable(buf []byte, srcPort, dstPort uint16) (uint32, error) {
	if len(buf) < int(unsafe.Sizeof(uint32(0))) {
		return 0, fmt.Errorf("TCP table is too short to read")
	}
	n := *(*uint32)(unsafe.Pointer(&buf[0]))
	rowSize := unsafe.Sizeof(mibTCPRowOwnerPID{})
	for i := uintptr(0); i < uintptr(n); i++ {
		off := unsafe.Sizeof(uint32(0)) + i*rowSize
		if off+rowSize > uintptr(len(buf)) {
			break
		}
		row := (*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[off]))
		if rowIsLoopbackConnection(row, srcPort, dstPort) {
			return row.OwningPID, nil
		}
	}
	return 0, fmt.Errorf("no connection from port %d to port %d", srcPort, dstPort)
}

// rowIsLoopbackConnection reports whether a table row is the loopback connection
// from srcPort to dstPort.
//
// Both addresses as well as both ports. Matching ports alone would accept a
// connection that merely happens to use the reported source port toward the
// same-numbered port on some other interface -- this decides which process is
// allowed through a tunnel, so it must require what its name claims.
func rowIsLoopbackConnection(row *mibTCPRowOwnerPID, srcPort, dstPort uint16) bool {
	return row.LocalAddr == loopbackAddrLE && row.RemoteAddr == loopbackAddrLE &&
		netPort(row.LocalPort) == srcPort && netPort(row.RemotePort) == dstPort
}

// sessionJob is this run's reaping job, published so the connect tunnel can ask
// whether a peer belongs to this sandbox.
//
// Atomic because the launch goroutine writes it while tunnel accept goroutines
// read it. The window is small -- it is set before the target starts and cleared
// after it exits -- but a tunnel connection can arrive in it, and a torn read of
// a handle would be decided against a value that was never valid. The race
// detector did not catch this: no test drives a launch and a tunnel together.
var sessionJob atomic.Uintptr

func setSessionJob(h syscall.Handle) { sessionJob.Store(uintptr(h)) }

// verifyTunnelPeer is the check the tunnel actually calls, held in a variable so
// a test can stand in for it.
//
// A test process cannot publish a real session job to be verified against: the
// job kills every member as soon as its last handle closes, so a test that
// joined one would take itself down at cleanup. The substitution is confined to
// proving the plumbing carries bytes; that the check itself fails closed is
// covered directly by TestPeerCheckFailsClosedWithoutAJob and
// TestTheTunnelRefusesAPeerItCannotPlaceInThisSandbox, which use the real one.
var verifyTunnelPeer = peerBelongsToThisSandbox

// peerBelongsToThisSandbox reports whether the process behind a tunnel
// connection is one this run launched.
//
// Fails closed on every uncertainty -- no job, unresolvable port, unopenable
// process. A sandbox that cannot prove the caller is its own must not dial the
// host service on its behalf; refusing costs the feature, allowing costs the
// containment claim.
func peerBelongsToThisSandbox(srcPort, dstPort uint16) (bool, error) {
	job := sessionJob.Load()
	if job == 0 {
		return false, fmt.Errorf("this session has no process job to check membership against")
	}
	pid, err := ownerOfLoopbackConnection(srcPort, dstPort)
	if err != nil {
		return false, err
	}
	h, _, oerr := procOpenProcessForQuery.Call(uintptr(processQueryLimitedInfo), 0, uintptr(pid))
	if h == 0 {
		return false, fmt.Errorf("open process %d: %v", pid, oerr)
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	var inJob int32
	ret, _, jerr := procIsProcessInJob.Call(h, job, uintptr(unsafe.Pointer(&inJob)))
	if ret == 0 {
		return false, fmt.Errorf("IsProcessInJob for %d: %v", pid, jerr)
	}
	return inJob != 0, nil
}
