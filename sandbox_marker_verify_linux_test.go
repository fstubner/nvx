//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestContainmentNotDisprovedUnderLandlock is the Linux half of the F25 check: from
// inside a real Landlock sandbox, containmentDisproved() must NOT claim to disprove
// containment. If it did, every nested nvx would try to sandbox itself again.
//
// Landlock is irreversible for a process, so the restricted half runs in a re-exec'd
// child, matching the pattern the other Landlock tests use.
func TestContainmentNotDisprovedUnderLandlock(t *testing.T) {
	if os.Getenv("NVX_TEST_MARKER_CHILD") == "1" {
		guest := os.Getenv("NVX_TEST_GUEST")
		work := os.Getenv("NVX_TEST_WORK")
		nvxHome := os.Getenv("NVX_TEST_NVXHOME")
		if err := applyLandlockSandbox(guest, work, nvxHome); err != nil {
			fmt.Fprintf(os.Stderr, "LANDLOCK_FAILED: %v\n", err)
			os.Exit(3)
		}
		// A real launch redirects HOME to the guest home; realHomeDir must ignore it.
		os.Setenv("HOME", guest)
		if containmentDisproved() {
			fmt.Print("containment=DISPROVED\n")
		} else {
			fmt.Print("containment=NOT_DISPROVED\n")
		}
		os.Exit(0)
	}

	if fd, err := landlockCreateRuleset(landlockAccessFull); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestContainmentNotDisprovedUnderLandlock")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_MARKER_CHILD=1",
		"NVX_TEST_GUEST="+t.TempDir(),
		"NVX_TEST_WORK="+t.TempDir(),
		"NVX_TEST_NVXHOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("contained child failed: %v\noutput:\n%s", err, out)
	}

	got := string(out)
	switch {
	case strings.Contains(got, "containment=NOT_DISPROVED"):
		t.Log("correct: under Landlock the check stays inconclusive, so a legitimate nested nvx still skips re-sandboxing")
	case strings.Contains(got, "containment=DISPROVED"):
		t.Errorf("the check disproved containment from inside a Landlock sandbox; nested nvx would re-sandbox:\n%s", got)
	default:
		t.Errorf("inconclusive child output:\n%s", got)
	}
}
