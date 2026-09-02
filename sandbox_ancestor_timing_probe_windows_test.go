//go:build windows

package main

// Measurement probe (NVX_PROBE=1): which ancestor directory costs the launch its
// seconds, and is the cost the ACL read or the ACL write?
//
// The phase probe showed grantWorkdirAncestors burning its whole 3s budget on
// every run, warm included. Two paths at the 1500ms per-path timeout would do
// that, so the question is which paths and which half of the operation.
//
// It measures both guest-home shapes, because they have different ancestors:
// %TEMP% (what the probes use) sits under AppData, while the real one is
// <nvxHome>/sandbox_home/<id>. If only the first is slow, the probes have been
// measuring their own layout rather than the product's.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMeasureAncestorGrantCost(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	const probeProfile = "nvx.sandbox.anctiming"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		t.Fatal(err)
	}

	tempGuest, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempGuest)

	// The production shape: <nvxHome>/sandbox_home/<id>, with nvxHome under the
	// user profile the way a real install is.
	realNvxHome, err := os.MkdirTemp(os.Getenv("USERPROFILE"), "nvx-anc")
	if err != nil {
		t.Skipf("cannot create a directory under the user profile: %v", err)
	}
	defer os.RemoveAll(realNvxHome)
	realGuest := filepath.Join(realNvxHome, "sandbox_home", "0123456789abcdef")
	if err := os.MkdirAll(realGuest, 0o700); err != nil {
		t.Fatal(err)
	}

	report := func(label, dir string) {
		paths := ancestorGrantPaths(dir, os.Getenv("USERPROFILE"))
		var b strings.Builder
		fmt.Fprintf(&b, "\n  %s\n  %s\n", label, dir)
		fmt.Fprintf(&b, "  %-58s %10s %10s %10s\n", "ancestor", "has-grant", "grant#1", "grant#2")
		for _, p := range paths {
			start := time.Now()
			had := appContainerHasGrantFor(sidStr, p, grantTraverse)
			readMS := time.Since(start).Milliseconds()

			start = time.Now()
			_ = grantTraverseTimeboxed(sidStr, p, ancestorGrantPerPath)
			first := time.Since(start).Milliseconds()

			start = time.Now()
			_ = grantTraverseTimeboxed(sidStr, p, ancestorGrantPerPath)
			second := time.Since(start).Milliseconds()

			shown := p
			if len(shown) > 58 {
				shown = "..." + shown[len(shown)-55:]
			}
			fmt.Fprintf(&b, "  %-58s %7dms(%v) %7dms %7dms\n", shown, readMS, had, first, second)
		}
		t.Log(b.String())
	}

	report("guest home under %TEMP% (what the probes use)", tempGuest)
	report("guest home in the production layout", realGuest)
}
