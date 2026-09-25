//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"
)

var procLockFileExAuditTest = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

// The write failing after the open succeeded is reported too.
//
// This was the half the old code threw away with `_, _ =`. A byte-range lock
// held on another handle is the one portable-enough way to make a real append
// fail after a successful open: Windows enforces the lock on every other
// handle's WriteFile with ERROR_LOCK_VIOLATION.
func TestAnAuditLogWriteThatFailsIsReported(t *testing.T) {
	auditWriteFailureOnce = sync.Once{}
	t.Cleanup(func() { auditWriteFailureOnce = sync.Once{} })

	home := tempDir(t)
	path := filepath.Join(home, "audit.log")
	holder, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	const lockExclusive, failImmediately = 0x2, 0x1
	var ov syscall.Overlapped
	r, _, lerr := procLockFileExAuditTest.Call(holder.Fd(), lockExclusive|failImmediately, 0,
		0xFFFFFFFF, 0xFFFFFFFF, uintptr(unsafe.Pointer(&ov)))
	if r == 0 {
		t.Fatalf("LockFileEx: %v", lerr)
	}

	out := captureStderrHere(t, func() {
		auditLog(home, "egress_deny", map[string]string{"host": "a.example"})
	})
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("the locked file was written to (stat: %v, %v); the setup does not make the write fail", info, err)
	}
	if n := strings.Count(out, auditLogFailureWarning); n != 1 {
		t.Fatalf("the failed write was reported %d times, want once; stderr:\n%s", n, out)
	}
}
