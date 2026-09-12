//go:build windows

package nvx

import (
	"syscall"
)

var procSetHandleInformation = modKernel32.NewProc("SetHandleInformation")

const handleFlagInherit = 0x00000001

// stdioHandles is the set of handles a sandboxed child should receive, plus
// whether they can actually be inherited.
type stdioHandles struct {
	in, out, err syscall.Handle
	// inheritable is true only when all three handles were successfully marked
	// inheritable. Assigning handles in STARTUPINFO without this does nothing --
	// see prepareInheritableStdio.
	inheritable bool
}

// markHandleInheritable sets HANDLE_FLAG_INHERIT on h.
func markHandleInheritable(h syscall.Handle) error {
	if h == 0 || h == syscall.InvalidHandle {
		return syscall.EINVAL
	}
	ret, _, err := procSetHandleInformation.Call(
		uintptr(h),
		uintptr(handleFlagInherit),
		uintptr(handleFlagInherit),
	)
	if ret == 0 {
		return err
	}
	return nil
}

// prepareInheritableStdio collects this process's standard handles and marks them
// inheritable so a child can actually receive them.
//
// Assigning StdInput/StdOutput/StdErr in STARTUPINFO has no effect unless BOTH
// STARTF_USESTDHANDLES is set AND CreateProcess is called with
// bInheritHandles=TRUE. The launcher previously set neither, so the assignment was
// silently ignored: a console-attached child inherited the console anyway (which
// is why interactive use looked fine), but **pipe handles were never inherited**,
// so every stdio-JSON-RPC daemon -- every MCP server -- failed deterministically.
// Measured on a pipe: 0 bytes delivered, child exit 0, output surfacing on the
// parent's console instead.
//
// The two flags are strictly all-or-nothing. Setting STARTF_USESTDHANDLES while
// leaving bInheritHandles=FALSE makes CreateProcess hand the child handles it
// cannot use, and the child fails to start outright (measured: exit code 1) --
// worse than the original bug. Hence a single `inheritable` result rather than two
// independent knobs.
//
// Inheritability is requested explicitly rather than assumed, because some console
// handles are not inheritable (the reason the original code cited for passing
// FALSE). If any handle refuses, the caller falls back to the legacy shape rather
// than failing a launch the user is waiting on -- loudly, since piped stdio will
// not work in that configuration.
func prepareInheritableStdio() stdioHandles {
	var s stdioHandles

	in, err1 := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	out, err2 := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	errh, err3 := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	if err1 != nil || err2 != nil || err3 != nil {
		return s
	}
	s.in, s.out, s.err = in, out, errh

	for _, h := range []syscall.Handle{in, out, errh} {
		if markHandleInheritable(h) != nil {
			return s // inheritable stays false; caller degrades
		}
	}
	s.inheritable = true
	return s
}

// inheritableStdioHandleList returns the distinct, usable standard handles, in
// the order stdin, stdout, stderr.
//
// This is the exact set a contained child is meant to receive, for
// PROC_THREAD_ATTRIBUTE_HANDLE_LIST. Two filters are load-bearing, because
// CreateProcess rejects the whole launch with ERROR_INVALID_PARAMETER rather
// than ignoring a bad entry, and a rejected launch here would break every
// contained run:
//
//   - Duplicates. The three standard handles are frequently the same object: a
//     console gives all three the same handle, and `cmd > log 2>&1` gives stdout
//     and stderr the same file.
//   - Invalid entries. A process with no console and no redirection gets a null
//     or INVALID_HANDLE_VALUE back from GetStdHandle.
//
// An empty result means there is nothing to pin, and the caller omits the
// attribute rather than passing a zero-length list.
func inheritableStdioHandleList(s stdioHandles) []syscall.Handle {
	var list []syscall.Handle
	for _, h := range []syscall.Handle{s.in, s.out, s.err} {
		if h == 0 || h == syscall.InvalidHandle {
			continue
		}
		seen := false
		for _, existing := range list {
			if existing == h {
				seen = true
				break
			}
		}
		if !seen {
			list = append(list, h)
		}
	}
	return list
}
