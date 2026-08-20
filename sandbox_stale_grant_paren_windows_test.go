//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// icacls prints a directory's path on the same line as its first ACE:
//
//	C:\some(I)path S-1-15-2-...:(OI)(CI)(M)
//	               BUILTIN\Administrators:(I)(OI)(CI)(F)
//
// The scan used to skip any line containing "(I)", meaning to skip inherited
// ACEs. For a project whose PATH contains that text, the skip swallowed its own
// first entry instead -- so a genuinely dangerous (OI)(CI)(M) grant was invisible
// to both `doctor` and the launch-path cleanup. The project stayed writable by
// every sandbox on the machine while doctor called the install healthy and --fix
// did nothing. An acceptance pass found it by naming a directory with "(I)".
//
// Inheritance is now read from the rights the SID carries rather than from the
// line, so the path cannot influence it.
func TestStaleGrantScanIsNotFooledByParenthesesInThePath(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (writes and removes real ACEs)")
	}

	sid, err := ensureAppContainerSID("nvx.sandbox.parenprobe")
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	sidStr, err := appContainerSidToString(sid)
	syscall.LocalFree(syscall.Handle(sid))
	if err != nil {
		t.Fatal(err)
	}
	defer deleteAppContainerProfile("nvx.sandbox.parenprobe")

	// Both spellings of the trap, plus a control, so a pass cannot come from the
	// scan having simply stopped filtering anything.
	for _, name := range []string{"proj(I)x", "(I)", "plain"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(tempDir(t), name)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			grant := fmt.Sprintf("*%s:(OI)(CI)(M)", sidStr)
			if out, err := runWinCmd(30*time.Second, "icacls", dir, "/grant", grant, "/c", "/q"); err != nil {
				t.Skipf("cannot write a legacy-style ACE here: %v (%s)", err, strings.TrimSpace(string(out)))
			}

			found := staleAppContainerSIDsOn(dir)
			if !sidListContains(found, sidStr) {
				t.Fatalf("a dangerous (OI)(CI)(M) grant on %q was not found (%v); the project would stay "+
					"writable by every sandbox while doctor called it healthy", dir, found)
			}
		})
	}
}

// The other half: inherited ACEs must still be skipped, because /remove:g cannot
// delete them and reporting one would send the user after something they cannot
// fix. A fresh directory inherits from its parent, so this needs no setup.
func TestStaleGrantScanStillSkipsInheritedAces(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (reads real ACLs)")
	}
	dir := tempDir(t)
	out, err := runWinCmd(30*time.Second, "icacls", dir)
	if err != nil {
		t.Skipf("cannot read an ACL here: %v", err)
	}
	if !strings.Contains(string(out), "(I)") {
		t.Skip("this directory inherits nothing; the case under test cannot arise")
	}
	if found := staleAppContainerSIDsOn(dir); len(found) != 0 {
		t.Errorf("inherited ACEs were reported as removable stale grants: %v", found)
	}
}
