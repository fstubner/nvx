//go:build windows

package main

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// Exit when the process that started us has gone away.
//
// Measured on the development machine 2026-08-21: 48 live nvx processes, 38 of
// them orphaned, each still holding a staged supervisor inside an AppContainer,
// accumulating at roughly one per 60-80 seconds until Windows ran out of commit
// charge. Nearly all were `nvx shim npx <an MCP server>`.
//
// The existing Job Object reaping is the other half of this and works: when nvx
// exits, everything it launched dies with it (killing the 38 took their 38
// supervisors with them). What was missing is any reason for nvx to exit. It
// blocks in WaitForSingleObject on the contained child, and that child is an MCP
// server that does not stop when its stdin reaches EOF -- plenty do not. Client
// dies, server keeps running, nvx keeps waiting, forever.
//
// TWO signals must agree, and the first version of this shipped with only one.
//
// That version treated a broken stdin pipe on its own as "the client is gone",
// which is false for an ordinary shell pipeline: a producer closing its end is
// how a pipeline is SUPPOSED to finish. Measured against the built binary --
//
//	echo hi | nvx node -e "require('fs').readFileSync(0); <20s of work>"
//
// -- the producer exits, the child drains the buffer, PeekNamedPipe starts
// reporting ERROR_BROKEN_PIPE, and a perfectly healthy command was killed at 15
// seconds with exit 129. The same shape covers any parent that spawns nvx with
// a pipe and calls stdin.end(), which is a common Node pattern. Found by
// acceptance review.
//
// A drained, writer-closed pipe is indistinguishable at the handle level from
// an abandoned client, so the pipe alone cannot answer this. What separates them
// is who is still there: in a pipeline the shell that built it is alive and
// waiting, and an MCP client that has gone away is not. So nvx leaves only when
// its input has hung up AND the process that started it has exited.
//
// Deliberate detachment stays safe for the same reason it did before -- with
// `start /b`, stdin is a console or NUL rather than a broken pipe, so the first
// condition never holds.
//
// When the parent cannot be identified at all, nvx does NOT exit. Leaking a
// process is a bad outcome; killing work someone is waiting on is a worse one.

var (
	procPeekNamedPipe = modKernel32.NewProc("PeekNamedPipe")
	procGetFileType   = modKernel32.NewProc("GetFileType")
)

const (
	fileTypePipe = 0x0003
	// ERROR_BROKEN_PIPE: every handle to the write end has been closed.
	errorBrokenPipe = 109
)

// stdinBrokenPipeInterval is how often the pipe is checked. Generous on purpose:
// these are processes that live for hours, and the cost of noticing a minute
// late is nothing next to the cost of a poll loop on every nvx invocation.
//
// A variable rather than a constant so the orphan reproduction can run in
// seconds instead of minutes. Nothing in the product writes to it.
var stdinBrokenPipeInterval = 15 * time.Second

// watchStdinForHangup calls onHangup once nvx's stdin pipe has no writer left.
//
// A no-op unless stdin is a pipe. A console or a redirected file is not evidence
// of anyone waiting on us, and the check would be meaningless there.
func watchStdinForHangup(nvxHome string, onHangup func()) {
	stdin, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil || stdin == 0 || stdin == syscall.InvalidHandle {
		noteHangupWatch(nvxHome, "not-armed", "nvx has no usable stdin handle")
		return
	}
	if fileType, _, _ := procGetFileType.Call(uintptr(stdin)); fileType != fileTypePipe {
		noteHangupWatch(nvxHome, "not-armed", fmt.Sprintf("stdin is not a pipe (file type %d)", fileType))
		return
	}

	// Opened once, up front, and held: the handle keeps referring to this
	// process even after it exits, so a recycled PID cannot later be mistaken
	// for our parent still running -- or worse, an unrelated new process's exit
	// mistaken for ours.
	parent, ok := openParentProcess()
	if !ok {
		// Cannot tell a finished pipeline from a departed client; do nothing.
		noteHangupWatch(nvxHome, "not-armed", "the parent process could not be identified or opened")
		return
	}

	ppid, _ := parentProcessID()
	noteHangupWatch(nvxHome, "armed", fmt.Sprintf("watching stdin pipe against parent pid %d", ppid))

	go func() {
		defer syscall.CloseHandle(parent)
		lastReason := ""
		parentSeenGone := false
		for {
			time.Sleep(stdinBrokenPipeInterval)

			broken := stdinPipeIsBroken(stdin)
			gone := processHasExited(parent)

			// The parent being gone is enough, GIVEN that stdin is a pipe.
			//
			// Requiring a broken pipe as well is what let 12 orphans accumulate on
			// the maintainer's machine on 2026-08-27, each holding a sandbox and a
			// contained child, 43 node processes and 3.9 GB between them, until the
			// system froze. nvx had recorded the reason itself: "the parent has
			// exited, but something still holds the input pipe open". On Windows a
			// sibling that inherited the write end is enough to keep it open for
			// ever, so the pipe never breaks and the second signal never arrives.
			//
			// This does not reopen the regression the two-signal rule was added
			// for. That was a finished shell pipeline -- the producer closes its
			// end, the pipe reads as broken, and a healthy command was killed at 15
			// seconds while the SHELL THAT BUILT THE PIPELINE was still waiting for
			// it. The parent is alive in that case, so `gone` is false and nothing
			// fires. It was the pipe half that was the wrong signal, not this one.
			//
			// Deliberate detachment stays safe by the arming rule above rather than
			// by this one: `start /b` leaves stdin a console or NUL, so the
			// watchdog never arms at all. Reaching here means someone deliberately
			// wired a pipe to us, and the process that did it has since died.
			//
			// Confirmed across two consecutive polls so a momentary misread cannot
			// end a command on its own.
			if gone {
				if !parentSeenGone {
					parentSeenGone = true
					noteHangupWatch(nvxHome, "confirming", "the parent has exited; confirming before leaving")
					continue
				}
				noteHangupWatch(nvxHome, "fired",
					fmt.Sprintf("the parent has exited (input pipe broken: %t)", broken))
				onHangup()
				return
			}
			parentSeenGone = false

			// Why it declined, recorded on CHANGE only. Polling every 15s for
			// hours would otherwise bury the log in identical lines, and the
			// interesting thing is the transition -- the moment the pipe breaks
			// while the parent lives, or the reverse, is what distinguishes an
			// abandoned server from a finished pipeline.
			reason := "input pipe still has a writer, and the parent is still running"
			if broken {
				reason = "input hung up, but the process that started nvx is still running"
			}
			if reason != lastReason {
				noteHangupWatch(nvxHome, "waiting", reason)
				lastReason = reason
			}
		}
	}()
}

// noteHangupWatch records what the watchdog decided.
//
// It exists because the watchdog was silent except when it fired, so "declined"
// and "never armed" looked identical from outside -- which left 15 processes
// that outlived their client unexplainable without guessing. Written to
// audit.log rather than stderr: this is for reading afterwards, not during.
//
// Behind NVX_TRACE like every other per-run record, so it costs nothing unless
// someone is investigating.
func noteHangupWatch(nvxHome, state, reason string) {
	if nvxHome == "" || !runTraceEnabled() {
		return
	}
	auditLog(nvxHome, "hangup_watch", map[string]string{
		"state":  state,
		"reason": reason,
	})
}

// openParentProcess returns a handle to the process that started this one.
func openParentProcess() (syscall.Handle, bool) {
	ppid, ok := parentProcessID()
	if !ok {
		return 0, false
	}
	h, _, _ := procOpenProcessForJob.Call(uintptr(processSynchronize), 0, uintptr(ppid))
	if h == 0 {
		// Already gone, or not ours to open. Either way this watchdog cannot
		// make a safe decision, and the safe default is to leave the command
		// alone.
		return 0, false
	}
	return syscall.Handle(h), true
}

// parentProcessID finds this process's parent via a process snapshot.
//
// Toolhelp rather than NtQueryInformationProcess: the parent PID is not exposed
// by the Go standard library on Windows, and of the two ways to get it this one
// is documented and stable.
func parentProcessID() (uint32, bool) {
	const th32csSnapProcess = 0x00000002
	snap, _, _ := procCreateToolhelp32Snapshot.Call(uintptr(th32csSnapProcess), 0)
	if snap == uintptr(syscall.InvalidHandle) || snap == 0 {
		return 0, false
	}
	defer syscall.CloseHandle(syscall.Handle(snap))

	var entry processEntry32W
	entry.Size = uint32(unsafe.Sizeof(entry))
	self := uint32(syscall.Getpid())

	ret, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ret != 0 {
		if entry.ProcessID == self {
			return entry.ParentProcessID, true
		}
		ret, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return 0, false
}

// processHasExited reports whether the process behind h has ended.
func processHasExited(h syscall.Handle) bool {
	const waitObject0 = 0x00000000
	ret, _, _ := procWaitForSingleObject.Call(uintptr(h), 0)
	return ret == waitObject0
}

// processEntry32W mirrors PROCESSENTRY32W. Only Size, ProcessID and
// ParentProcessID are read; the rest is present so the struct is the size the
// API checks for.
type processEntry32W struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

var (
	procCreateToolhelp32Snapshot = modKernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = modKernel32.NewProc("Process32FirstW")
	procProcess32NextW           = modKernel32.NewProc("Process32NextW")
)

// stdinPipeIsBroken reports whether the write end of stdin has been closed.
//
// PeekNamedPipe rather than a read: nvx hands this same handle to the contained
// child by inheritance, so consuming a byte here would steal it from the process
// the bytes are meant for. Peeking does not consume, and on an idle-but-live
// pipe it simply reports zero bytes available.
func stdinPipeIsBroken(stdin syscall.Handle) bool {
	var available uint32
	ret, _, callErr := procPeekNamedPipe.Call(
		uintptr(stdin),
		0, 0, 0,
		uintptr(unsafe.Pointer(&available)),
		0,
	)
	if ret != 0 {
		return false
	}
	errno, ok := callErr.(syscall.Errno)
	// Only a broken pipe counts. Any other failure -- a handle shape this does
	// not understand, a transient error -- must not be read as "the client is
	// gone", because the consequence is killing a running command.
	return ok && uintptr(errno) == errorBrokenPipe
}
