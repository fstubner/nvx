//go:build windows

package nvx

import (
	"runtime"
	"syscall"
	"testing"
	"time"
)

// The probes' pipe readers used to wrap the read handle in an *os.File, while
// every caller also closed that handle itself. The os.File's finalizer then
// closed the same handle value a second time, whenever the collector ran --
// by which point Windows had often handed the value to something else. What
// it closed was whatever held it: a socket, which the next test saw as
// "wsasend: An operation was attempted on something that is not a socket",
// or one of the Go runtime's own events ("fatal error: runtime.semasleep
// wait_failed"). Measured 2026-09-24: both messages from real CI and local
// runs, and the socket one reproduced in a standalone program on the same
// pattern. It was the source of relay and AF_UNIX probe failures that moved
// between tests from run to run.
func TestProbeReadersLeaveTheHandleToTheCaller(t *testing.T) {
	for name, read := range map[string]func(*testing.T, syscall.Handle) string{
		"readProbeOutput": readProbeOutput,
		"readWithTimeout": readWithTimeout,
	} {
		t.Run(name, func(t *testing.T) {
			r, w := makeTestPipe(t)
			var n uint32
			if err := syscall.WriteFile(w, []byte("x"), &n, nil); err != nil {
				t.Fatal(err)
			}
			syscall.CloseHandle(w)
			if got := read(t, r); got != "x" {
				t.Fatalf("read %q, want %q", got, "x")
			}
			for i := 0; i < 5; i++ {
				runtime.GC()
				time.Sleep(50 * time.Millisecond)
			}
			if err := syscall.CloseHandle(r); err != nil {
				t.Fatalf("the caller's own close failed (%v): something else already closed the handle it owns", err)
			}
		})
	}
}
