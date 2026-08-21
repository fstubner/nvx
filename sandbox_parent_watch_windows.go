//go:build windows

package main

import (
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
// The signal used here is nvx's OWN stdin breaking, not the parent process
// exiting. Both would have caught this case, but a parent-exit watchdog fires
// wrongly on deliberate detachment -- `start /b nvx ...` leaves cmd.exe exiting
// immediately by design -- and killing a dev server someone deliberately
// detached would be a worse bug than the one being fixed. A broken stdin pipe
// means specifically that whatever was talking to us is gone, which is the
// condition that actually stranded these processes.

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
const stdinBrokenPipeInterval = 15 * time.Second

// watchStdinForHangup calls onHangup once nvx's stdin pipe has no writer left.
//
// A no-op unless stdin is a pipe. A console or a redirected file is not evidence
// of anyone waiting on us, and the check would be meaningless there.
func watchStdinForHangup(onHangup func()) {
	stdin, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil || stdin == 0 || stdin == syscall.InvalidHandle {
		return
	}
	if fileType, _, _ := procGetFileType.Call(uintptr(stdin)); fileType != fileTypePipe {
		return
	}

	go func() {
		for {
			time.Sleep(stdinBrokenPipeInterval)
			if stdinPipeIsBroken(stdin) {
				onHangup()
				return
			}
		}
	}()
}

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
