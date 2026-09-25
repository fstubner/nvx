package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// auditLogFailureWarning is the text a failed audit write prints.
const auditLogFailureWarning = "Could not write to the audit log"

// An audit record nvx could not write is said, once.
//
// auditLog's comment promised that a failure "is reported once", and nothing
// reported anything: a failed open returned and a failed write was discarded.
// The log is the record of what nvx refused, so losing it silently leaves a
// user reading `nvx audit` as complete when it is not.
func TestAnAuditLogThatCannotBeOpenedIsReportedOnce(t *testing.T) {
	auditWriteFailureOnce = sync.Once{}
	t.Cleanup(func() { auditWriteFailureOnce = sync.Once{} })

	home := tempDir(t)
	// A directory where the file should be: the open fails on every platform.
	if err := os.MkdirAll(filepath.Join(home, "audit.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := captureStderrHere(t, func() {
		auditLog(home, "egress_deny", map[string]string{"host": "a.example"})
		auditLog(home, "egress_deny", map[string]string{"host": "b.example"})
	})
	if n := strings.Count(out, auditLogFailureWarning); n != 1 {
		t.Fatalf("the failure was reported %d times, want once; stderr:\n%s", n, out)
	}
}
