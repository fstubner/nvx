package main

import (
	"io"
	"net"
	"sync"
	"time"
)

// Helpers shared by the tunnels that carry traffic across a sandbox boundary:
// --expose on Windows, and the --connect relays on Windows and macOS.
//
// Portable code that lived in the Windows files while Windows was the only
// caller.

// connectDialTimeout bounds a --connect relay's dial to the real service. A
// service that is not running should fail the contained connection promptly
// rather than leaving the tool waiting on a connection nvx knows it cannot
// complete.
const connectDialTimeout = 5 * time.Second

// freeLoopbackPort asks the OS for a port and returns it. Racy in principle, and
// the same approach the expose listener uses; the window is microseconds and the
// alternative is guessing a number that might already be taken.
func freeLoopbackPort() int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// spliceConns copies in both directions until either side is done, then closes
// both. Half-close is deliberately not preserved: an HTTP client that finishes
// its request and waits for a response needs the other direction to stay open,
// and closing both on the first EOF would cut the response short -- so each
// direction runs to completion before anything is closed.
func spliceConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeWrite(a)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeWrite(b)
	}()
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// closeWrite signals end-of-stream to the peer without tearing down the other
// direction, where the connection type supports it.
func closeWrite(c net.Conn) {
	type writeCloser interface{ CloseWrite() error }
	if wc, ok := c.(writeCloser); ok {
		_ = wc.CloseWrite()
	}
}
