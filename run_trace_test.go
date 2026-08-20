package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firstAction must not carry secrets into the log.
//
// This is the failure this test exists for: audit.log is the file a user pastes
// into a bug report, and `npm config set //registry.npmjs.org/:_authToken=...`
// puts a live credential in argv. Recording the subcommand is useful; recording
// the argument after it is a credential leak with a log file's lifetime.
func TestFirstActionOmitsSecretBearingArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"plain subcommand", []string{"install"}, "install"},
		{"steps over known boolean flags", []string{"-y", "--silent", "trust"}, "trust"},
		{"steps over --flag=value", []string{"--registry=https://r.example.com", "install"}, "install"},
		// The reason this is fail-closed: -e takes the next argument, so skipping
		// it would record the script body as the subcommand.
		{"an unknown flag ends the search", []string{"-e", "process.exit(3)"}, ""},
		{"a value-taking flag does not leak its value", []string{"--otp", "482913", "publish"}, ""},
		{"registry token is not an action", []string{"//registry.npmjs.org/:_authToken=abc123"}, ""},
		{"scoped package is not an action", []string{"@acme/internal-tool"}, ""},
		{"path is not an action", []string{"./scripts/deploy.js"}, ""},
		{"url is not an action", []string{"https://example.com/tarball.tgz"}, ""},
		{"long opaque value is not an action", []string{strings.Repeat("k", 64)}, ""},
		{"no arguments", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstAction(tc.args); got != tc.want {
				t.Errorf("firstAction(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// A run record is only worth writing if it says whether the run was contained
// and why not, and surfaces the warnings that fired.
func TestRunTraceRecordsContainmentAndWarnings(t *testing.T) {
	home := tempDir(t)
	resetTracedWarnings()

	LogWarn("a runtime dir is ahead of nvx's shim dir on PATH")

	tr := beginRunTrace(home, "npx", []string{"-y", "trust", "github"})
	tr.top = true // the process running the tests is not itself a shim invocation
	tr.note(runModeDirect, "--no-sandbox")
	tr.finish(0)

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 record, got %d", len(entries))
	}
	e := entries[0]

	for field, want := range map[string]string{
		"event":   "run",
		"command": "npx",
		"action":  "trust",
		"mode":    runModeDirect,
		"reason":  "--no-sandbox",
		"exit":    "0",
	} {
		if e[field] != want {
			t.Errorf("%s = %q, want %q", field, e[field], want)
		}
	}
	if !strings.Contains(e["warnings"], "ahead of nvx's shim dir") {
		t.Errorf("the warning that fired was not recorded: %q", e["warnings"])
	}
	if e["duration_ms"] == "" {
		t.Error("no duration recorded")
	}
}

// Nested nvx processes must not each look like a separate user action. One typed
// npm command can spawn a tree of lifecycle scripts, and counting each as a run
// would make every summary above it wrong.
func TestRunTraceSkipsNestedInvocations(t *testing.T) {
	home := tempDir(t)
	resetTracedWarnings()

	tr := beginRunTrace(home, "node", []string{"build.js"})
	tr.top = false
	tr.note(runModeDirect, "your code at standard isolation")
	tr.finish(0)

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a nested invocation was recorded: %v", entries)
	}
}

// The same warning printed repeatedly within one run is one fact, not many. A
// loop emitting it a thousand times must not be able to grow the log.
func TestWarningsAreDedupedAndCapped(t *testing.T) {
	resetTracedWarnings()
	for i := 0; i < 50; i++ {
		recordWarning("the same warning")
	}
	if got := collectedWarnings(); len(got) != 1 {
		t.Fatalf("want 1 deduped warning, got %d", len(got))
	}

	resetTracedWarnings()
	for i := 0; i < maxTracedWarnings+10; i++ {
		recordWarning(strings.Repeat("x", i+1))
	}
	if got := collectedWarnings(); len(got) != maxTracedWarnings {
		t.Fatalf("cap not applied: got %d warnings, want %d", len(got), maxTracedWarnings)
	}
}

// Rotation keeps the log bounded, and the reader must still see across the seam
// -- otherwise rotating silently discards the history a review depends on.
func TestAuditLogRotatesAndStaysReadableAcrossGenerations(t *testing.T) {
	home := tempDir(t)
	path := filepath.Join(home, "audit.log")

	if err := os.WriteFile(path, []byte(`{"event":"run","command":"old"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Grow past the cap with realistic records, then rotate the way a real run
	// does.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	filler := []byte(`{"event":"run","command":"filler"}` + "\n")
	for written := 0; written < maxAuditBytes; written += len(filler) {
		if _, err := f.Write(filler); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	rotateAuditLog(home)
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("the log was not rotated: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the live log should have been moved aside, not left in place")
	}

	auditLog(home, "run", map[string]string{"command": "new"})

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, e := range entries {
		commands = append(commands, e["command"])
	}
	if len(commands) < 2 || commands[0] != "old" || commands[len(commands)-1] != "new" {
		t.Fatalf("the reader lost history across rotation, oldest-first: %v", commands)
	}
}

// A torn line -- a write interrupted by a crash, or two processes appending at
// once -- must not make the whole log unreadable.
func TestAuditReaderSkipsCorruptedLines(t *testing.T) {
	home := tempDir(t)
	content := `{"event":"run","command":"good1"}` + "\n" +
		`{"event":"run","comm` + "\n" +
		`{"event":"run","command":"good2"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "audit.log"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatalf("a corrupted line aborted the read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want the 2 intact records, got %d", len(entries))
	}
}

// One absurd line must not cost the history after it. A Scanner-based reader
// fails permanently at that point; this pins that the reader keeps going.
func TestAuditReaderSurvivesAnOversizedLine(t *testing.T) {
	home := tempDir(t)
	content := `{"event":"run","command":"before"}` + "\n" +
		`{"event":"run","junk":"` + strings.Repeat("z", 4<<20) + `"}` + "\n" +
		`{"event":"run","command":"after"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "audit.log"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatalf("an oversized line aborted the read: %v", err)
	}
	if len(entries) != 2 || entries[0]["command"] != "before" || entries[1]["command"] != "after" {
		t.Fatalf("records after the oversized line were lost: %v", entries)
	}
}

func resetTracedWarnings() {
	warningsMu.Lock()
	defer warningsMu.Unlock()
	seenWarnings = nil
}
