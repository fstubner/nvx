package nvx

import (
	"strings"
	"testing"
)

// Colour codes are for a terminal.
//
// Every Log helper wrote escape codes unconditionally, so a CI log or a pipe
// carried literal "\x1b[32m", and NO_COLOR was ignored.
func TestColourFollowsTheTerminalAndNoColor(t *testing.T) {
	notQuiet(t)
	orig := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = orig })

	for _, tc := range []struct {
		name     string
		terminal bool
		noColor  string
		want     bool
	}{
		{"terminal", true, "", true},
		{"pipe", false, "", false},
		{"terminal with NO_COLOR", true, "1", false},
		{"pipe with NO_COLOR", false, "1", false},
	} {
		stderrIsTerminal = func() bool { return tc.terminal }
		t.Setenv("NO_COLOR", tc.noColor)
		got := captureStderrHere(t, func() {
			LogInfo("hello %s", "world")
			LogWarn("careful")
			LogError("broken")
			LogSuccess("done")
		})
		if has := strings.Contains(got, "\x1b"); has != tc.want {
			t.Errorf("%s: escape codes present = %v, want %v:\n%q", tc.name, has, tc.want, got)
		}
		for _, text := range []string{"hello world", "careful", "broken", "done"} {
			if !strings.Contains(got, text) {
				t.Errorf("%s: message %q missing from %q", tc.name, text, got)
			}
		}
	}
}

// Progress goes to a log as a few plain lines, not as a bar redrawn with
// carriage returns.
func TestDownloadProgressIsPlainLinesWhenNotATerminal(t *testing.T) {
	orig := stderrIsTerminal
	t.Cleanup(func() { stderrIsTerminal = orig })
	stderrIsTerminal = func() bool { return false }

	got := captureStderrHere(t, func() {
		pw := &progressWriter{total: 1000}
		for i := 0; i < 1000; i++ {
			_, _ = pw.Write([]byte{0})
		}
	})
	if strings.ContainsAny(got, "\r\x1b") {
		t.Errorf("progress to a pipe carries control characters:\n%q", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 10 {
		t.Errorf("progress wrote %d lines for a 1000 byte download, want 10:\n%s", len(lines), got)
	}
	if !strings.Contains(got, "100%") {
		t.Errorf("progress never reported completion:\n%s", got)
	}
}
