//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verbatim `CheckNetIsolation LoopbackExempt -s` output from a machine carrying
// the leftover grant, trimmed to four entries. Two things it has to survive: an
// entry whose profile was deleted still prints "AppContainer NOT FOUND" as its
// name while remaining exempt, so matching on the name would miss it; and a
// neighbouring SID that shares the S-1-15-2- prefix must not match.
const checkNetIsolationSample = `
List Loopback Exempted AppContainers

[1] -----------------------------------------------------------------
    Name: AppContainer NOT FOUND
    SID:  S-1-15-2-490905099-2794809881-2632752266-3514030558-4166392763-3416490339-321513134

[2] -----------------------------------------------------------------
    Name: nvx.sandbox
    SID:  S-1-15-2-125897231-4118270468-3890225265-1944594370-665964903-770884402-3722446281

[3] -----------------------------------------------------------------
    Name: microsoft.win32webviewhost_cw5n1h2txyewy
    SID:  S-1-15-2-1310292540-1029022339-4008023048-2190398717-53961996-4257829345-603366646
`

const nvxSandboxSampleSID = "S-1-15-2-125897231-4118270468-3890225265-1944594370-665964903-770884402-3722446281"

func TestParseLoopbackExemptSIDsFindsTheSandbox(t *testing.T) {
	sids := parseLoopbackExemptSIDs(checkNetIsolationSample)
	if len(sids) != 3 {
		t.Fatalf("expected 3 exempted SIDs, got %d: %v", len(sids), sids)
	}
	if !sidListContains(sids, nvxSandboxSampleSID) {
		t.Errorf("the nvx.sandbox SID was not recognised as exempt: %v", sids)
	}
	// A SID that merely shares the package prefix is a different identity.
	other := "S-1-15-2-125897231-4118270468-3890225265-1944594370-665964903-770884402-9999999999"
	if sidListContains(sids, other) {
		t.Errorf("matched an unrelated SID %q", other)
	}
	// A truncated SID must not match by prefix either.
	if sidListContains(sids, "S-1-15-2-125897231") {
		t.Error("matched a truncated SID prefix")
	}
}

// The clear answer is cached so a healthy machine pays nothing per launch; the
// exempt answer must never be, or the warning would keep firing for a day after
// the user ran the elevated command it told them to run.
func TestOnlyTheClearLoopbackResultIsCached(t *testing.T) {
	home := t.TempDir()

	if loopbackExemptRecentlyClear(home, nvxSandboxSampleSID) {
		t.Fatal("a machine with no cache file must not read as recently clear")
	}

	markLoopbackExemptClear(home, nvxSandboxSampleSID)
	if !loopbackExemptRecentlyClear(home, nvxSandboxSampleSID) {
		t.Error("a freshly recorded clear result should be trusted")
	}

	// Nothing in the exempt path writes the cache: sandboxIsLoopbackExempt only
	// calls markLoopbackExemptClear after finding the SID absent.
	if _, err := os.Stat(filepath.Join(home, "loopback-exempt-clear.json")); err != nil {
		t.Fatalf("expected a cache file after a clear result: %v", err)
	}
}

func TestLoopbackClearCacheExpiresAndIsSIDScoped(t *testing.T) {
	home := t.TempDir()
	write := func(sid string, at time.Time) {
		data, err := json.Marshal(loopbackExemptCheck{SID: sid, CheckedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "loopback-exempt-clear.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(nvxSandboxSampleSID, time.Now().Add(-loopbackExemptRecheckTTL-time.Minute))
	if loopbackExemptRecentlyClear(home, nvxSandboxSampleSID) {
		t.Error("an expired clear result must be re-checked, not trusted")
	}

	// The exemption is keyed by SID; a result recorded for a different identity
	// says nothing about this one.
	write("S-1-15-2-1-2-3-4-5-6-7", time.Now())
	if loopbackExemptRecentlyClear(home, nvxSandboxSampleSID) {
		t.Error("a clear result for another SID must not be reused")
	}

	if err := os.WriteFile(filepath.Join(home, "loopback-exempt-clear.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if loopbackExemptRecentlyClear(home, nvxSandboxSampleSID) {
		t.Error("a corrupt cache must fall back to re-checking")
	}
}

// The bug this whole file exists for was silence: the exemption was present, the
// allowlist looked enforced, and nothing said otherwise. So the property worth
// pinning against the real machine is that an exempt SID produces the warning.
// Skips when the machine is clean, which is the state we want users in.
func TestExemptMachineIsWarnedAbout(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (reads the machine's loopback exemption list)")
	}
	sidStr, err := deriveAppContainerSIDString(stableSandboxProfile)
	if err != nil {
		t.Skipf("cannot derive the sandbox SID here: %v", err)
	}
	sids, err := listLoopbackExemptSIDs()
	if err != nil {
		t.Skipf("cannot read the exemption list here: %v", err)
	}
	if !sidListContains(sids, sidStr) {
		t.Skip("this machine has no nvx loopback exemption (the healthy state)")
	}

	out := captureStderr(t, func() {
		warnIfSandboxLoopbackExempt(t.TempDir(), sidStr, "proxy")
	})
	if !strings.Contains(out, "loopback exemption") {
		t.Errorf("an exempt machine was not warned; stderr was:\n%s", out)
	}
	if !strings.Contains(out, sidStr) {
		t.Errorf("the warning must print the SID the removal command needs; stderr was:\n%s", out)
	}

	// network.mode "open" asks for no egress restriction, so there is nothing to
	// warn about weakening.
	if quiet := captureStderr(t, func() {
		warnIfSandboxLoopbackExempt(t.TempDir(), sidStr, "open")
	}); strings.TrimSpace(quiet) != "" {
		t.Errorf("expected no warning under network.mode open, got:\n%s", quiet)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	orig := os.Stderr
	os.Stderr = f
	fn()
	os.Stderr = orig
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
