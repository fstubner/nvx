//go:build linux || windows

package nvx

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The refusal for a socket path that will not bind names the longest NVX_HOME
// that does. Windows said so already. Linux said only "a shorter directory",
// which leaves a person guessing how much shorter.
//
// "Longest that works" is checked as a boundary: a home of exactly that length
// fits, and one byte more does not.
func TestTheSocketPathRefusalNamesTheLongestNvxHome(t *testing.T) {
	const suffix = "/sandbox_home/0123456789abcdef/.nvx-egress.sock"
	sockUnder := func(nvxHome string) string {
		guestHome := filepath.Join(getSandboxHomeDir(nvxHome), "0123456789abcdef")
		return filepath.Join(guestHome, ".nvx-egress.sock")
	}
	homeOfLength := func(n int) string { return "/" + strings.Repeat("h", n-1) }
	wantMax := unixSocketPathMax - 1 - len(suffix)

	if !egressSocketPathFits(sockUnder(homeOfLength(wantMax))) {
		t.Fatalf("a %d-byte NVX_HOME does not fit, so %d is not the longest that works", wantMax, wantMax)
	}
	if egressSocketPathFits(sockUnder(homeOfLength(wantMax + 1))) {
		t.Fatalf("a %d-byte NVX_HOME fits, so %d is not the longest that works", wantMax+1, wantMax)
	}

	long := homeOfLength(wantMax + 40)
	err := unixSocketPathTooLong("egress socket", sockUnder(long), long)
	want := fmt.Sprintf("at most %d characters (it is %d)", wantMax, len(long))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("refusal for a %d-byte NVX_HOME = %v, want it to contain %q", len(long), err, want)
	}

	if err := unixSocketPathTooLong("egress socket", sockUnder(homeOfLength(wantMax)), homeOfLength(wantMax)); err != nil {
		t.Errorf("a path that fits was refused: %v", err)
	}
}
