package main

import (
	"io"
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
		cmd  string
		args []string
		want string
	}{
		{"plain subcommand", "npm", []string{"install"}, "install"},
		{"steps over known boolean flags", "npm", []string{"-y", "--silent", "trust"}, "trust"},
		{"steps over --flag=value", "npm", []string{"--registry=https://r.example.com", "install"}, "install"},
		// The reason this is fail-closed: -e takes the next argument, so skipping
		// it would record the script body as the subcommand.
		{"an unknown flag ends the search", "node", []string{"-e", "process.exit(3)"}, ""},
		{"a value-taking flag does not leak its value", "npm", []string{"--otp", "482913", "publish"}, ""},
		{"registry token is not an action", "npm", []string{"//registry.npmjs.org/:_authToken=abc123"}, ""},
		{"scoped package is not an action", "npm", []string{"@acme/internal-tool"}, ""},
		{"path is not an action", "node", []string{"./scripts/deploy.js"}, ""},
		{"url is not an action", "npm", []string{"https://example.com/tarball.tgz"}, ""},
		{"long opaque value is not an action", "npm", []string{strings.Repeat("k", 64)}, ""},
		{"no arguments", "npm", nil, ""},

		// An ad-hoc tool runner has no subcommand: the first positional IS the
		// package, and a private tool name is not ours to log.
		{"npx package is not an action", "npx", []string{"-y", "acme-internal-deploy-2024"}, ""},
		{"bunx package is not an action", "bunx", []string{"some-private-tool"}, ""},

		// Nor does node: its first positional is a script. This recorded
		// `acme-client-secret.js` while SECURITY.md said arguments were not
		// recorded -- a filename can name a client or an unshipped product.
		{"node script name is not an action", "node", []string{"acme-client-secret.js"}, ""},
		{"bun is ambiguous, so records nothing", "bun", []string{"install"}, ""},
		{"an unknown command records nothing", "some-future-tool", []string{"build"}, ""},

		// Flags are case-sensitive to the tools themselves. Lowercasing before
		// the lookup made -F match the -f entry, so pnpm's filter VALUE was
		// recorded as the subcommand.
		{"-F does not step over its value", "pnpm", []string{"-F", "mypkg", "build"}, ""},
		{"-D is still not a subcommand", "npm", []string{"-D", "install"}, "install"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstAction(tc.cmd, tc.args); got != tc.want {
				t.Errorf("firstAction(%q, %q) = %q, want %q", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}

// The regression that made acceptance BLOCK: a password reached audit.log.
//
// Warnings were recorded as rendered text, and warnings quote arguments --
// LogWarn("Proceeding without registry metadata checks for %s.", pkgName) with
// pkgName being a URL carrying credentials. firstAction refusing to record argv
// was worth nothing while this door was open.
func TestWarningsRecordTheTemplateNotTheSecret(t *testing.T) {
	resetTracedWarnings()

	const secret = "s3cr3t-P4ssw0rd"
	LogWarn("Proceeding without registry metadata checks for %s.",
		"https://deploy:"+secret+"@git.internal.corp/pkg.git")

	got := collectedWarnings()
	if len(got) != 1 {
		t.Fatalf("want 1 warning, got %d", len(got))
	}
	if strings.Contains(got[0], secret) {
		t.Fatalf("the password reached the log: %q", got[0])
	}
	if strings.Contains(got[0], "git.internal.corp") {
		t.Fatalf("a private host reached the log: %q", got[0])
	}
	if !strings.Contains(got[0], "Proceeding without registry metadata checks") {
		t.Errorf("the warning is no longer identifiable: %q", got[0])
	}
}

// Recording the format string is only safe while every call site passes a
// literal. That invariant lives at the call sites, not where it is relied on,
// so nothing at the recording site would reveal a `LogWarn(userInput)` added
// later -- it would simply start logging user input again.
func TestEveryLogWarnUsesALiteralFormat(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			idx := strings.Index(line, "LogWarn(")
			if idx < 0 || strings.Contains(line, "func LogWarn(") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // prose about LogWarn, including this file's own
			}
			rest := line[idx+len("LogWarn("):]
			if !strings.HasPrefix(rest, `"`) {
				t.Errorf("%s:%d passes a non-literal format to LogWarn, which would put "+
					"runtime data into audit.log: %s", file, i+1, strings.TrimSpace(line))
				continue
			}
			// A leading quote is not enough: LogWarn("host " + h + " denied")
			// starts with one and still builds the format from runtime data,
			// which is the exact leak this guard exists to prevent. The literal
			// has to END the format argument.
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				continue // a string spanning lines; not something in this tree
			}
			after := strings.TrimSpace(rest[1+end+1:])
			if !strings.HasPrefix(after, ",") && !strings.HasPrefix(after, ")") {
				t.Errorf("%s:%d builds LogWarn's format from runtime data, which would put it "+
					"into audit.log: %s", file, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// A warning containing a newline forged a whole extra row in `nvx audit`
// output -- a fabricated "sandboxed" run in the listing whose only job is
// telling you which runs were contained.
func TestWarningsCannotForgeAnAuditRow(t *testing.T) {
	resetTracedWarnings()
	recordWarning("harmless\n2026-08-21 10:00  sandboxed  npm install  0.0s")

	got := collectedWarnings()
	if len(got) != 1 {
		t.Fatalf("want 1 warning, got %d", len(got))
	}
	if strings.ContainsAny(got[0], "\n\r\t") {
		t.Fatalf("control characters survived into the record: %q", got[0])
	}
	// The reader splits this field on " | ", so that sequence must not survive
	// either or one warning counts as two in the summary.
	if strings.Contains(got[0], "|") {
		t.Fatalf("the field separator survived into the record: %q", got[0])
	}
}

// A run record is only worth writing if it says whether the run was contained
// and why not, and surfaces the warnings that fired.
func TestRunTraceRecordsContainmentAndWarnings(t *testing.T) {
	home := tempDir(t)
	resetTracedWarnings()

	LogWarn("a runtime dir is ahead of nvx's shim dir on PATH")

	t.Setenv(nvxTraceEnvVar, "1")
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
		// No action: npx is an ad-hoc tool runner, so "trust" here is an
		// argument to the package, and the first positional is the package name
		// itself -- neither is ours to log.
		"action": "",
		"mode":   runModeDirect,
		"reason": "--no-sandbox",
		"exit":   "0",
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

	t.Setenv(nvxTraceEnvVar, "1")
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

// Run recording is off unless asked for.
//
// It is a debugging aid, not a feature everyone gets: recording every command a
// developer runs is not something to switch on for people who did not ask,
// however local the file stays. Security decisions are the separate, always-on
// half -- those record what nvx refused, which is the reason to run it.
func TestRunRecordingIsOffUnlessEnabled(t *testing.T) {
	home := tempDir(t)
	resetTracedWarnings()
	t.Setenv(nvxTraceEnvVar, "")

	tr := beginRunTrace(home, "npm", []string{"install"})
	tr.top = true
	tr.note(runModeSandboxed, "")
	tr.finish(0)

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e["event"] == "run" {
			t.Fatalf("a run was recorded without %s being set: %v", nvxTraceEnvVar, e)
		}
	}

	// The status line is not part of the opt-in: it must still print, so tracing
	// being off cannot quietly change what the user sees.
	if !tr.isTop() {
		t.Error("the invocation stopped reporting itself as top-level when tracing was off, " +
			"which would drop the \"Running directly (not sandboxed)\" line")
	}

	// And a security decision still lands, because that half is unconditional.
	auditLog(home, "egress_deny", map[string]string{"host": "evil.example.com:443"})
	entries, err = readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0]["event"] != "egress_deny" {
		t.Fatalf("security decisions must be recorded regardless of %s: %v", nvxTraceEnvVar, entries)
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

// runVerifyInstall must return its exit code rather than calling os.Exit.
//
// It used to exit the process on all nine of its abort paths, every one of them
// jumping straight over runShim's trace. The runs that went unrecorded were
// therefore exactly the ones a security review looks for -- policy blocks,
// typosquat aborts, CVE aborts, script refusals -- and `nvx audit --failures`
// could not show a single one. A returned code cannot skip the caller's
// bookkeeping; os.Exit always can.
func TestRunVerifyInstallReturnsRatherThanExiting(t *testing.T) {
	home := tempDir(t)
	policy := `{"blocked_packages":["acme-internal-secret-sauce"]}`
	if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}

	// Reaching this line at all is the assertion: before the fix the process
	// died inside the call and the test binary went with it.
	code := runVerifyInstall([]string{"acme-internal-secret-sauce"}, home)

	if code == 0 {
		t.Fatalf("a blocked package returned success (%d); the policy check is not aborting", code)
	}
}

func resetTracedWarnings() {
	warningsMu.Lock()
	defer warningsMu.Unlock()
	seenWarnings = nil
}

// A record must not be able to forge another record.
//
// Several audit fields carry values chosen by the contained process -- a SOCKS5
// request names an arbitrary destination host, and that host is what an
// egress_deny record stores. A newline in one produced a complete extra row in
// `nvx audit` output, so a tool whose only job is reporting which runs were
// contained could be made to report that an uncontained one was. An escape
// sequence could clear the screen above a denial.
//
// The earlier fix covered the warnings field alone; host, tool, project, path
// and action all reach the same renderer. Found by acceptance review.
func TestAuditFieldsCannotForgeRowsOrDriveTheTerminal(t *testing.T) {
	home := tempDir(t)

	auditLog(home, "egress_deny", map[string]string{
		"host": "evil.example\n2026-01-01 00:00  egress_allow_prompted     host=totally.safe\x1b[2J",
	})

	entries, err := readAuditEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 record, got %d", len(entries))
	}
	if strings.ContainsAny(entries[0]["host"], "\n\r\x1b") {
		t.Errorf("control characters were stored verbatim: %q", entries[0]["host"])
	}

	// And the renderer must not trust the file either, since it is editable and
	// may hold records from a version that did not sanitise.
	rendered := formatAuditEntry(map[string]string{
		"event": "egress_deny",
		"time":  "2026-08-21T11:00:00Z",
		"host":  "evil.example\nFORGED  sandboxed  npm install\x1b[31m",
	})
	if strings.ContainsAny(rendered, "\n\x1b") {
		t.Errorf("a crafted record forged output: %q", rendered)
	}
}

// EVERY render path, not just the one that was fixed.
//
// This test previously covered formatAuditEntry alone, so printAuditSummary kept
// printing raw values and a crafted `mode` could clear the screen -- wiping the
// real counts and leaving a forged one as the only visible output. That is the
// third time this class of bug survived a fix aimed at one field or one path, so
// the test now names the paths rather than a field.
func TestEveryAuditRenderPathNeutralisesCraftedRecords(t *testing.T) {
	const clearScreen = "\x1b[2J\x1b[H"
	crafted := []map[string]string{{
		"event":       "run",
		"time":        "2026-08-21T10:01:00Z",
		"command":     "npm",
		"mode":        clearScreen + "sandboxed",
		"reason":      clearScreen + "contained",
		"warnings":    clearScreen + "all clear",
		"exit":        "0",
		"duration_ms": "10",
	}}

	// Newlines are legitimate in a summary, so only the escape is asserted on.
	summary := captureStdout(t, func() { printAuditSummary(crafted) })
	if strings.Contains(summary, "\x1b") {
		t.Errorf("nvx audit --summary passed a terminal escape through: %q", summary)
	}

	listed := formatAuditEntry(crafted[0])
	if strings.Contains(listed, "\x1b") {
		t.Errorf("nvx audit passed a terminal escape through: %q", listed)
	}
}

// captureStdout collects what fn prints.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = prev
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
