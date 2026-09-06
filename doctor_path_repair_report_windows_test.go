//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nvx doctor --fix` says which PATH entries it removes.
//
// The repair rebuilds the User PATH with the shim directory first and every
// raw-runtime directory under ~/.nvx dropped. Dropping them is the fix; not
// saying so is the defect: a person whose PATH shrank by three entries had
// nothing to tell them what went, or that it was deliberate, or why. The
// registry is behind a seam here so the repair can be shown a PATH and asked
// what it would do, without writing anything -- apply is false.
func TestDoctorFixNamesThePathEntriesItRemoves(t *testing.T) {
	// Under the user profile, not the test temp directory: the repair refuses
	// an NVX_HOME inside the temp directory outright, which is right, and would
	// otherwise be what this test measured.
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	nvxHome, err := os.MkdirTemp(userHome, ".nvx-doctor-report-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(nvxHome) })
	runtimeBin := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
	if err := os.MkdirAll(runtimeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := `C:\Program Files\Git\cmd`
	orig := readUserPath
	readUserPath = func() (string, error) {
		return strings.Join([]string{runtimeBin, keep}, ";"), nil
	}
	t.Cleanup(func() { readUserPath = orig })

	stderr := captureStderr(t, func() {
		if _, err := repairPersistentPathImpl(nvxHome, false); err != nil {
			t.Fatalf("repair (report only) failed: %v", err)
		}
	})
	if !strings.Contains(stderr, runtimeBin) {
		t.Fatalf("the repair would drop %s from PATH and did not say so:\n%s", runtimeBin, stderr)
	}
	if strings.Contains(stderr, keep) && strings.Contains(stderr, "remov") {
		t.Fatalf("the repair named an entry it keeps as removed:\n%s", stderr)
	}
}
