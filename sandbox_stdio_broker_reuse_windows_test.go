//go:build windows

package main

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// A channel serves more than one client over the life of a session.
//
// The preload hands a channel back to its free list when the child that used
// it closes, and the Go side used to serve each channel exactly once: accept a
// client, pump, close the destination, return. So the name went back into
// circulation attached to a dead pipe instance. The ninth piped child in one
// contained process -- the first to draw a recycled name -- found its open
// refused, and the preload's failure path was the raw spawn that hangs inside
// libuv. That is the hang the whole broker exists to remove, reached by way of
// the broker.
//
// Both directions, because the reverse (stdin) channel closes the other handle
// at end of stream and could be wrong independently.
func TestAStdioChannelServesASecondClientAfterTheFirstLeaves(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "stdout"
		if reverse {
			name = "stdin"
		}
		t.Run(name, func(t *testing.T) {
			sddl := "D:(A;;GA;;;WD)" // both endpoints are the current user here
			ch, err := newStdioChannel("reusetest"+name, 0, sddl, reverse)
			if err != nil {
				t.Skipf("cannot create pipes on this host: %v", err)
			}
			broker := &stdioBroker{channels: []*stdioChannel{ch}}
			defer broker.Close()
			go ch.pump()

			for use := 1; use <= 3; use++ {
				roundTrip(t, ch, use)
			}
		})
	}
}

// roundTrip is one complete use of a channel: open both ends the way contained
// code does, move one message through, close the writer, and see EOF on the
// reader. On the second and later uses the opens are what used to fail.
func roundTrip(t *testing.T, ch *stdioChannel, use int) {
	t.Helper()

	// The preload opens the child end first, then the node end. Keep that
	// order so the test exercises the same sequence the pump sees.
	childEnd, err := openNamedPipeRetrying(ch.childPipe)
	if err != nil {
		t.Fatalf("use %d: could not open the child end: %v -- a recycled channel name points at a dead pipe", use, err)
	}
	nodeEnd, err := openNamedPipeRetrying(ch.nodePipe)
	if err != nil {
		syscall.CloseHandle(childEnd)
		t.Fatalf("use %d: could not open the node end: %v", use, err)
	}

	// Which end writes depends on direction: stdout flows child -> node,
	// stdin flows node -> child.
	writer, reader := childEnd, nodeEnd
	if ch.reverse {
		writer, reader = nodeEnd, childEnd
	}

	msg := "use " + string(rune('0'+use)) + "\n"
	// Bounded, like the reads below. A write to a pipe instance nobody is
	// draining blocks in the kernel with no deadline of its own, and when that
	// happened on a CI runner the whole job sat for ten minutes and then died
	// with a stack trace instead of a sentence. One occurrence, never reproduced
	// in 40 local runs, cause not established -- so the point of this is that
	// the next one says which step stalled.
	wrote := make(chan error, 1)
	go func() {
		var n uint32
		wrote <- syscall.WriteFile(writer, []byte(msg), &n, nil)
	}()
	select {
	case err := <-wrote:
		if err != nil {
			syscall.CloseHandle(childEnd)
			syscall.CloseHandle(nodeEnd)
			t.Fatalf("use %d: write: %v", use, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("use %d: the write never completed; nothing is draining this channel", use)
	}

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		var n uint32
		if err := syscall.ReadFile(reader, buf, &n, nil); err != nil {
			got <- "READ-FAILED: " + err.Error()
			return
		}
		got <- string(buf[:n])
	}()
	select {
	case v := <-got:
		if !strings.Contains(v, msg) {
			t.Fatalf("use %d: the reader got %q, want %q", use, v, msg)
		}
	case <-time.After(10 * time.Second):
		syscall.CloseHandle(childEnd)
		syscall.CloseHandle(nodeEnd)
		t.Fatalf("use %d: nothing reached the reader; the pump is not carrying bytes", use)
	}

	// The writer leaving must end the reader's stream, every time.
	syscall.CloseHandle(writer)
	ended := make(chan bool, 1)
	go func() {
		buf := make([]byte, 64)
		var n uint32
		err := syscall.ReadFile(reader, buf, &n, nil)
		ended <- err != nil || n == 0
	}()
	select {
	case ok := <-ended:
		if !ok {
			t.Errorf("use %d: the reader kept returning data after the writer closed", use)
		}
	case <-time.After(10 * time.Second):
		t.Errorf("use %d: the reader never reached EOF after the writer closed", use)
	}
	syscall.CloseHandle(reader)
}

// openNamedPipeRetrying opens a pipe client, tolerating the brief window in
// which the pump is replacing a used-up instance with a fresh one. A client
// that arrives in that window sees ERROR_FILE_NOT_FOUND or ERROR_PIPE_BUSY for
// a few microseconds; in production the preload skips to the next channel and
// this one is picked up next time, so a short retry here stands in for that.
func openNamedPipeRetrying(name string) (syscall.Handle, error) {
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		var h syscall.Handle
		h, err = openNamedPipe(name)
		if err == nil {
			return h, nil
		}
		if time.Now().After(deadline) {
			return syscall.InvalidHandle, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
