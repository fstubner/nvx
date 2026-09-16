package nvx

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// `nvx audit export` hands the local record to something else.
//
// `nvx audit` prints for a person: aligned columns, a summary, one line per
// record. That is the wrong shape for the thing people actually need the log
// for, which is evidence -- a compliance review asking "show me every egress
// denial in the last quarter" wants a file it can load, not a terminal it can
// scroll. The answer until now was to grep the JSONL by hand, which needs `jq`
// on a platform where `jq` is not a safe assumption, and which silently skips
// any line that will not parse.
//
// Three formats because the consumers differ: jsonl for a log pipeline, json for
// anything that wants one document, csv for the spreadsheet a review is actually
// conducted in. Filtered by time and by event, because a whole log is not
// evidence of anything in particular.
//
// The field set is a documented contract -- docs/audit-log.md -- rather than
// whatever the writers happen to emit this release. Something built on top of
// this needs to know which parts will not move.

func runAuditExport(args []string, nvxHome string) int {
	var (
		since    time.Time
		events   []string
		format   = "jsonl"
		outPath  string
		hasSince bool
	)

	// Both spellings of every valued flag, folded to one before the switch reads
	// them. Writing the two forms into each case is how a parser ends up
	// consuming a flag and not its value, leaving the value to be read as the
	// next flag: --since 7d then exports everything and reports success.
	normalised, err := foldValuedFlags(args, []string{"--since", "--event", "--format", "--out"})
	if err != nil {
		LogError("nvx audit export: %v", err)
		LogInfo("Valid options: --since, --event, --format, --out")
		return 1
	}
	for _, arg := range normalised {
		name, v, _ := strings.Cut(arg, "=")
		switch name {
		case "--since":
			t, perr := parseAuditSince(v, time.Now())
			if perr != nil {
				LogError("--since %s: %v", v, perr)
				return 1
			}
			since, hasSince = t, true
		case "--event":
			// An empty name would select nothing and report success, which reads as
			// "there were no such events".
			if strings.TrimSpace(v) == "" {
				LogError("--event needs an event name, e.g. --event egress_deny")
				return 1
			}
			events = append(events, v)
		case "--format":
			switch strings.ToLower(v) {
			case "json", "jsonl", "csv":
				format = strings.ToLower(v)
			default:
				LogError("Unknown --format %s. Valid formats: json, jsonl, csv.", v)
				return 1
			}
		case "--out":
			if strings.TrimSpace(v) == "" {
				LogError("--out needs a path")
				return 1
			}
			outPath = v
		default:
			LogError("Unknown option for nvx audit export: %s", arg)
			LogInfo("Valid options: --since, --event, --format, --out")
			return 1
		}
	}

	entries, readable, malformed, err := readAuditEntriesCounted(nvxHome)
	if err != nil {
		LogError("Could not read the audit log: %v", err)
		return 1
	}

	selected := selectAuditEntries(entries, since, hasSince, events)
	rendered, err := renderAuditExport(selected, format)
	if err != nil {
		LogError("Could not render the export: %v", err)
		return 1
	}

	if outPath == "" {
		fmt.Print(rendered)
	} else if err := os.WriteFile(outPath, []byte(rendered), 0600); err != nil {
		LogError("Could not write %s: %v", outPath, err)
		return 1
	} else {
		LogSuccess("Wrote %d of %d records to %s", len(selected), readable, outPath)
	}

	// A damaged log is reported as a failure, having exported what could be read.
	//
	// Skipping the bad lines quietly is what `nvx audit` does, which is right for
	// a person reading a screen and wrong for an evidence pipeline: the export
	// would look complete while missing records, and a missing record is the one
	// an investigation is looking for. The count of what WAS readable goes with
	// it, so the output is still usable and its limits are known.
	if malformed > 0 {
		LogError("%d line(s) in the audit log could not be parsed; %d were readable and have been exported.", malformed, readable)
		return 1
	}
	return 0
}

// foldValuedFlags rewrites "--flag value" into "--flag=value" so the caller has
// one form to read. A named flag with no value left is an error rather than an
// empty string, because "--since" alone means the value went missing somewhere
// and exporting everything instead is a wrong answer given confidently.
func foldValuedFlags(args, valued []string) ([]string, error) {
	takesValue := map[string]bool{}
	for _, f := range valued {
		takesValue[f] = true
	}
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !takesValue[arg] {
			out = append(out, arg)
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("%s needs a value", arg)
		}
		i++
		out = append(out, arg+"="+args[i])
	}
	return out, nil
}

// parseAuditSince accepts an RFC3339 timestamp or a duration back from now.
//
// Durations take a day and a week suffix on top of Go's own, because "the last 7
// days" is the question this gets asked and 168h is not how anybody says it.
func parseAuditSince(value string, now time.Time) (time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, fmt.Errorf("empty value")
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if num, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(num)
		if err != nil || days < 0 {
			return time.Time{}, fmt.Errorf("not a number of days")
		}
		return now.Add(-time.Duration(days) * 24 * time.Hour), nil
	}
	if num, ok := strings.CutSuffix(v, "w"); ok {
		weeks, err := strconv.Atoi(num)
		if err != nil || weeks < 0 {
			return time.Time{}, fmt.Errorf("not a number of weeks")
		}
		return now.Add(-time.Duration(weeks) * 7 * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return time.Time{}, fmt.Errorf("use an RFC3339 timestamp, or a duration like 7d, 2w, 12h")
	}
	if d < 0 {
		return time.Time{}, fmt.Errorf("a negative duration would select the future")
	}
	return now.Add(-d), nil
}

// selectAuditEntries applies the filters, oldest first.
//
// A record whose time cannot be parsed is KEPT when --since is given. It is
// evidence of something, and a filter that drops what it cannot read quietly
// removes exactly the damaged records an investigation would want to see.
func selectAuditEntries(entries []map[string]string, since time.Time, hasSince bool, events []string) []map[string]string {
	wanted := map[string]bool{}
	for _, e := range events {
		wanted[strings.ToLower(strings.TrimSpace(e))] = true
	}
	out := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		if len(wanted) > 0 && !wanted[strings.ToLower(entry["event"])] {
			continue
		}
		if hasSince {
			if t, err := time.Parse(time.RFC3339, entry["time"]); err == nil && t.Before(since) {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

// auditExportLeadingColumns are the fields every record carries, in the order a
// reader expects them. Everything else follows, sorted, so a column's position
// does not depend on which events happened to be in range.
var auditExportLeadingColumns = []string{"time", "pid", "event", "cwd"}

// renderAuditExport writes the selected records in the requested format.
//
// Values are sanitised on the way out, exactly as the terminal renderer
// sanitises them. Several fields come from the contained process itself, the log
// is a plain file anything on the machine can append to, and a spreadsheet is no
// safer a destination for a control character than a terminal is. Every value is
// a string, which is the contract docs/audit-log.md states: the log's own writers
// store numbers as numbers in some places and strings in others, and a consumer
// should not have to care which.
func renderAuditExport(entries []map[string]string, format string) (string, error) {
	clean := make([]map[string]string, 0, len(entries))
	for _, e := range entries {
		clean = append(clean, flattenAuditEntry(e))
	}

	switch format {
	case "json":
		// An empty selection is an empty array, not null: a consumer should be
		// able to iterate the result without a nil check.
		if clean == nil {
			clean = []map[string]string{}
		}
		data, err := json.MarshalIndent(clean, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data) + "\n", nil
	case "jsonl":
		var b strings.Builder
		for _, e := range clean {
			data, err := json.Marshal(e)
			if err != nil {
				return "", err
			}
			b.Write(data)
			b.WriteByte('\n')
		}
		return b.String(), nil
	case "csv":
		return renderAuditCSV(clean)
	}
	return "", fmt.Errorf("unknown format %q", format)
}

func renderAuditCSV(entries []map[string]string) (string, error) {
	leading := map[string]bool{}
	for _, c := range auditExportLeadingColumns {
		leading[c] = true
	}
	extra := map[string]bool{}
	for _, e := range entries {
		for k := range e {
			if !leading[k] {
				extra[k] = true
			}
		}
	}
	rest := make([]string, 0, len(extra))
	for k := range extra {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	columns := append(append([]string{}, auditExportLeadingColumns...), rest...)

	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write(columns); err != nil {
		return "", err
	}
	for _, e := range entries {
		row := make([]string, len(columns))
		for i, c := range columns {
			row[i] = e[c]
		}
		if err := w.Write(row); err != nil {
			return "", err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}
