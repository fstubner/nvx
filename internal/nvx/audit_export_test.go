package nvx

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAuditLog(t *testing.T, home string, lines ...string) {
	t.Helper()
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, "audit.log"), []byte(body), 0o600); err != nil {
		t.Fatalf("write audit.log: %v", err)
	}
}

func readExport(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	return string(data)
}

// A whole log is not evidence of anything in particular. The two filters a
// review actually asks for are a period and an event type.
func TestAuditExportFiltersByTimeAndEvent(t *testing.T) {
	home := tempDir(t)
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeAuditLog(t, home,
		`{"time":"`+old+`","pid":1,"event":"egress_deny","host":"old.example:443"}`,
		`{"time":"`+recent+`","pid":2,"event":"egress_deny","host":"recent.example:443"}`,
		`{"time":"`+recent+`","pid":3,"event":"run","command":"npm"}`,
	)

	out := filepath.Join(home, "export.jsonl")
	if code := runAuditExport([]string{"--since", "7d", "--event", "egress_deny", "--out", out}, home); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	body := readExport(t, out)
	if strings.Contains(body, "old.example") {
		t.Error("a record older than --since was exported")
	}
	if strings.Contains(body, `"event":"run"`) {
		t.Error("an event that --event did not name was exported")
	}
	if !strings.Contains(body, "recent.example") {
		t.Errorf("the record that matched both filters was not exported:\n%s", body)
	}
	if lines := strings.Count(strings.TrimSpace(body), "\n") + 1; lines != 1 {
		t.Errorf("exported %d lines, want 1", lines)
	}
}

// Both spellings of every valued flag. A parser that consumes the flag and not
// its value reads "7d" as the next flag, and the most likely outcome of that is
// an export of everything reported as a success.
func TestAuditExportAcceptsBothFlagSpellings(t *testing.T) {
	home := tempDir(t)
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	writeAuditLog(t, home, `{"time":"`+recent+`","pid":1,"event":"egress_deny","host":"a.example:443"}`)

	for _, args := range [][]string{
		{"--format", "json", "--out", filepath.Join(home, "a.json")},
		{"--format=json", "--out=" + filepath.Join(home, "b.json")},
	} {
		if code := runAuditExport(args, home); code != 0 {
			t.Fatalf("%v: exit code = %d, want 0", args, code)
		}
	}
	for _, name := range []string{"a.json", "b.json"} {
		var records []map[string]string
		if err := json.Unmarshal([]byte(readExport(t, filepath.Join(home, name))), &records); err != nil {
			t.Fatalf("%s is not JSON: %v", name, err)
		}
		if len(records) != 1 || records[0]["host"] != "a.example:443" {
			t.Fatalf("%s = %+v, want the one record", name, records)
		}
	}
}

// A missing value is an error rather than an empty one, because exporting
// everything and reporting success is the wrong answer to a typo.
func TestAuditExportRefusesAFlagWithNoValue(t *testing.T) {
	home := tempDir(t)
	writeAuditLog(t, home, `{"time":"2026-09-01T00:00:00Z","pid":1,"event":"egress_deny"}`)
	if code := runAuditExport([]string{"--since"}, home); code == 0 {
		t.Fatal("--since with no value exported successfully")
	}
	if code := runAuditExport([]string{"--format", "yaml", "--out", filepath.Join(home, "x")}, home); code == 0 {
		t.Fatal("an unknown format exported successfully")
	}
}

// CSV is read by its header row, so the leading columns are fixed and the rest
// are sorted. A column whose position depends on which events happened to be in
// range is not something a spreadsheet can be built on.
func TestAuditExportCSVHasAStableHeader(t *testing.T) {
	home := tempDir(t)
	writeAuditLog(t, home,
		// The escape is written as \u001b rather than as a raw byte. JSON forbids
		// an unescaped control character inside a string, so the raw version made
		// this line invalid JSON. The export counted it as malformed and exited 1
		// before any sanitising could be tested, which is the export behaving
		// correctly against a broken fixture. The decoded value still carries the
		// escape, which is the thing under test.
		`{"time":"2026-09-01T00:00:00Z","pid":1,"event":"egress_deny","host":"a.example:443"}`,
		`{"time":"2026-09-02T00:00:00Z","pid":2,"event":"trusted_tool_granted","tool":"eslint","project":"p"}`,
	)
	out := filepath.Join(home, "export.csv")
	if code := runAuditExport([]string{"--format", "csv", "--out", out}, home); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	rows, err := csv.NewReader(strings.NewReader(readExport(t, out))).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	want := []string{"time", "pid", "event", "cwd", "host", "project", "tool"}
	if strings.Join(rows[0], ",") != strings.Join(want, ",") {
		t.Fatalf("header = %v, want %v", rows[0], want)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows, want a header and two records", len(rows))
	}
	// Every value is a string, including pid, whatever the record held.
	if rows[1][1] != "1" {
		t.Errorf("pid rendered as %q, want \"1\"", rows[1][1])
	}
}

// A line that cannot be parsed is reported, and the export still says how much
// of the log it could read.
//
// `nvx audit` skips a torn line silently, which is right for a screen. An export
// is evidence, and evidence that quietly omits records is worse than none: the
// missing one is what an investigation is looking for.
func TestAuditExportFailsOnAMalformedLogAndSaysWhatWasReadable(t *testing.T) {
	home := tempDir(t)
	writeAuditLog(t, home,
		`{"time":"2026-09-01T00:00:00Z","pid":1,"event":"egress_deny","host":"a.example:443"}`,
		`{"time":"2026-09-02T00:00:00Z","pid":2,"even`,
		`{"time":"2026-09-03T00:00:00Z","pid":3,"event":"egress_deny","host":"b.example:443"}`,
	)

	entries, readable, malformed, err := readAuditEntriesCounted(home)
	if err != nil {
		t.Fatalf("readAuditEntriesCounted: %v", err)
	}
	if malformed != 1 {
		t.Errorf("malformed = %d, want 1", malformed)
	}
	if readable != 2 || len(entries) != 2 {
		t.Errorf("readable = %d (%d entries), want 2", readable, len(entries))
	}

	out := filepath.Join(home, "export.jsonl")
	if code := runAuditExport([]string{"--out", out}, home); code == 0 {
		t.Fatal("a damaged log exported with a success code; a consumer would treat the result as complete")
	}
	// The readable records are still exported. A failure that also throws away
	// the evidence is not an improvement.
	body := readExport(t, out)
	if !strings.Contains(body, "a.example") || !strings.Contains(body, "b.example") {
		t.Errorf("the readable records were not exported:\n%s", body)
	}
}

// A blank trailing line is the ordinary end of a file, not damage. Counting it
// would make every healthy log report as corrupt, and a warning that is always
// on is one nobody reads.
func TestATrailingNewlineIsNotAMalformedRecord(t *testing.T) {
	home := tempDir(t)
	writeAuditLog(t, home, `{"time":"2026-09-01T00:00:00Z","pid":1,"event":"egress_deny"}`)
	_, readable, malformed, err := readAuditEntriesCounted(home)
	if err != nil {
		t.Fatal(err)
	}
	if malformed != 0 || readable != 1 {
		t.Fatalf("readable = %d, malformed = %d, want 1 and 0", readable, malformed)
	}
}

func TestParseAuditSince(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"2w", now.Add(-14 * 24 * time.Hour)},
		{"12h", now.Add(-12 * time.Hour)},
		{"2026-09-01T00:00:00Z", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		got, err := parseAuditSince(tc.in, now)
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%s = %s, want %s", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "yesterday", "-3d", "7 days"} {
		if _, err := parseAuditSince(bad, now); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// A value from the contained process reaches a spreadsheet as readily as it
// reaches a terminal, and a newline in it would forge a record.
func TestAuditExportSanitisesValues(t *testing.T) {
	home := tempDir(t)
	writeAuditLog(t, home,
		`{"time":"2026-09-01T00:00:00Z","pid":1,"event":"egress_deny","host":"evil\ncontrol\u001b[2Jexample"}`,
	)
	out := filepath.Join(home, "export.jsonl")
	if code := runAuditExport([]string{"--out", out}, home); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var record map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(readExport(t, out))), &record); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if strings.ContainsAny(record["host"], "\n\x1b") {
		t.Fatalf("a control character survived into the export: %q", record["host"])
	}
}
