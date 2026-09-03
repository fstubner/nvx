package main

import (
	"strings"
	"testing"
)

// A command that never ran contained and a contained command that failed are
// different facts, and until 2026-09-03 the audit log could not tell them
// apart: every refusal returned a bare 1 and wrote nothing, so both produced
//
//	{"event":"run","exit":"1","mode":"sandboxed",...}
//
// Measured that day with an NVX_HOME too long for an AF_UNIX socket: nvx
// refused to start the sandbox, and the record was byte-identical to an
// `npm install` that ran fully contained and failed on its own terms. "Was
// this command actually contained?" is the question `nvx audit` exists to
// answer, and it could not.
func TestARefusalIsRecordedAndDistinguishable(t *testing.T) {
	home := tempDir(t)
	config := SandboxConfig{
		NvxHome: home,
		// Nothing resolves this, so runNativeSandbox declines before launching.
		Command: "nvx-no-such-command-xyzzy",
		WorkDir: tempDir(t),
	}

	code := runNativeSandbox(config, Policy{}, nil, NetworkLaunchContext{})
	if code == 0 {
		t.Fatal("an unresolvable command reported success")
	}

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var refusals []map[string]string
	for _, e := range entries {
		if e["event"] == "sandbox_not_started" {
			refusals = append(refusals, e)
		}
	}
	if len(refusals) != 1 {
		t.Fatalf("want exactly one sandbox_not_started record, got %d from %v", len(refusals), entries)
	}
	got := refusals[0]
	if got["command"] != config.Command {
		t.Errorf("the refusal names command %q, want %q", got["command"], config.Command)
	}
	if strings.TrimSpace(got["reason"]) == "" {
		t.Error("the refusal carries no reason, so the log says a command did not run but not why")
	}
	// Unconditional: NVX_TRACE is off here, and a containment decision is logged
	// whether or not per-run tracing is on.
}

// The reason is a fixed string, never a rendered error. LogWarn records format
// strings for the same reason: a rendered message can carry a package URL with
// credentials in it, and this log goes to disk.
func TestARefusalReasonCarriesNoRuntimeData(t *testing.T) {
	home := tempDir(t)
	secret := "https://deploy:s3cr3t@git.internal/pkg.git"
	config := SandboxConfig{NvxHome: home, Command: "npm", Args: []string{"install", secret}}

	code := sandboxDidNotStart(config, "the egress proxy could not be reached from the sandbox", 1)
	if code != 1 {
		t.Errorf("sandboxDidNotStart returned %d, want the code it was given", code)
	}
	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		for field, v := range e {
			if strings.Contains(v, "s3cr3t") {
				t.Fatalf("a credential from the command line reached the audit log in %q: %q", field, v)
			}
		}
	}
	if errSandboxDidNotStart == nil || strings.Contains(errSandboxDidNotStart.Error(), "%") {
		t.Error("the launcher's sentinel should be a fixed sentence carrying no runtime data")
	}
}
