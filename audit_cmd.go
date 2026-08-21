package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// `nvx audit` reads back what run_trace.go and the security-decision callers
// wrote.
//
// The log is JSON lines and an engineer could grep it, but a trace nobody reads
// is not a trace -- and on the platform nvx is most used on, `jq` is not
// something you can assume is installed. The reader is small on purpose: it
// prints, filters, and counts. Anything more analytical belongs in whatever
// tool the user pipes the raw lines into.

func runAuditCommand(args []string, nvxHome string) int {
	limit := 25
	onlyRuns := false
	onlyFailures := false
	summarize := false

	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--runs":
			onlyRuns = true
		case arg == "--failures":
			onlyFailures = true
			onlyRuns = true
		case arg == "--summary":
			summarize = true
		case arg == "--all":
			limit = 0
		case strings.HasPrefix(arg, "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit="))
			if err != nil || n < 1 {
				LogError("--limit needs a positive number")
				return 1
			}
			limit = n
		default:
			LogError("Unknown option for nvx audit: %s", arg)
			return 1
		}
	}

	entries, err := readAuditEntries(nvxHome)
	if err != nil {
		LogError("Could not read the audit log: %v", err)
		return 1
	}
	if len(entries) == 0 {
		LogInfo("No audit records yet. Security decisions are recorded as you use nvx.")
		if !runTraceEnabled() {
			LogInfo("Per-run records are off. Set %s=1 to record what each command did.", nvxTraceEnvVar)
		}
		return 0
	}

	filtered := entries[:0:0]
	for _, e := range entries {
		if onlyRuns && e["event"] != "run" {
			continue
		}
		if onlyFailures && e["exit"] == "0" {
			continue
		}
		filtered = append(filtered, e)
	}
	if len(filtered) == 0 {
		LogInfo("Nothing matched.")
		// Otherwise `nvx audit --runs` on a machine that never enabled tracing
		// reads as "you have run nothing", which is the wrong conclusion.
		if onlyRuns && !runTraceEnabled() {
			LogInfo("Per-run records are off. Set %s=1 to record what each command did.", nvxTraceEnvVar)
		}
		return 0
	}

	if summarize {
		printAuditSummary(filtered)
		return 0
	}

	shown := filtered
	if limit > 0 && len(shown) > limit {
		shown = shown[len(shown)-limit:]
	}
	for _, e := range shown {
		fmt.Println(formatAuditEntry(e))
	}
	if len(shown) < len(filtered) {
		LogInfo("Showing the last %d of %d records. Use --limit=N or --all.", len(shown), len(filtered))
	}
	return 0
}

// readAuditEntries loads the current log and the one rotated generation before
// it, oldest first.
//
// Values are flattened to strings: every field auditLog writes is already a
// string except the numbers this file writes as strings anyway, and a
// map[string]string keeps the filtering and formatting below free of type
// switches.
func readAuditEntries(nvxHome string) ([]map[string]string, error) {
	if nvxHome == "" {
		return nil, fmt.Errorf("no nvx home")
	}
	path := filepath.Join(nvxHome, "audit.log")
	var entries []map[string]string
	// Oldest first, so the rotated generation precedes the live file.
	for _, p := range []string{path + ".1", path} {
		batch, err := readAuditFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		entries = append(entries, batch...)
	}
	return entries, nil
}

func readAuditFile(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []map[string]string
	// bufio.Reader rather than bufio.Scanner: a Scanner fails permanently on a
	// line longer than its buffer, so one oversized or corrupted line would make
	// the rest of the log unreadable. Records are appended by concurrent
	// processes, so a torn or absurd line is a thing that happens, and losing
	// the history after it is a worse outcome than losing the line.
	reader := bufio.NewReader(f)
	for {
		line, err := readBoundedLine(reader)
		if line == "" && err != nil {
			break
		}
		var raw map[string]any
		if jerr := json.Unmarshal([]byte(strings.TrimSpace(line)), &raw); jerr != nil || raw == nil {
			// A torn line from a crashed write is not a reason to fail. `null`
			// parses cleanly into a nil map, which would otherwise become an
			// empty record printing as a blank row.
			if err != nil {
				break
			}
			continue
		}
		entry := make(map[string]string, len(raw))
		for k, v := range raw {
			switch t := v.(type) {
			case string:
				entry[k] = t
			case float64:
				entry[k] = strconv.FormatFloat(t, 'f', -1, 64)
			default:
				entry[k] = fmt.Sprint(t)
			}
		}
		entries = append(entries, entry)
		if err != nil {
			break // last line, unterminated
		}
	}
	return entries, nil
}

// maxRecordBytes bounds one record. A real one is a few hundred bytes; anything
// vastly larger is damage, not data.
const maxRecordBytes = 1 << 20

// readBoundedLine reads one line without ever holding more than maxRecordBytes
// of it, discarding the remainder of an over-long line as it goes.
//
// ReadSlice rather than ReadString: ReadString assembles the entire line before
// returning, so a 300 MB line allocated 300 MB before any length check could
// reject it -- the check read as a bound but only ever acted as a filter.
// ReadSlice hands back at most one buffer at a time and reports ErrBufferFull,
// which is what makes discarding possible at all.
//
// An over-long line returns "" so the caller skips it, having consumed it.
func readBoundedLine(r *bufio.Reader) (string, error) {
	var b strings.Builder
	overLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !overLong {
			if b.Len()+len(chunk) > maxRecordBytes {
				overLong = true
				b.Reset()
			} else {
				b.Write(chunk)
			}
		}
		if err == bufio.ErrBufferFull {
			continue // more of this same line to come; keep consuming it
		}
		if overLong {
			return "", err
		}
		return b.String(), err
	}
}

func formatAuditEntry(e map[string]string) string {
	when := shortTime(e["time"])
	if e["event"] != "run" {
		return fmt.Sprintf("%s  %-24s %s", when, e["event"], securityEventDetail(e))
	}

	line := fmt.Sprintf("%s  %-8s %s", when, e["mode"], strings.TrimSpace(e["command"]+" "+e["action"]))
	if e["exit"] != "0" {
		line += fmt.Sprintf("  exit=%s", e["exit"])
	}
	if ms := e["duration_ms"]; ms != "" {
		line += fmt.Sprintf("  %ss", formatSeconds(ms))
	}
	if r := e["reason"]; r != "" && e["mode"] != runModeSandboxed {
		line += fmt.Sprintf("  (%s)", r)
	}
	if w := e["warnings"]; w != "" {
		line += "\n            ⚠ " + strings.ReplaceAll(w, " | ", "\n            ⚠ ")
	}
	return line
}

// securityEventDetail renders whatever a non-run record carries. The events are
// written by several callers with different fields, so this names the ones that
// exist rather than assuming a shape.
func securityEventDetail(e map[string]string) string {
	var parts []string
	for _, k := range []string{"host", "tool", "project", "path", "mode"} {
		if v := e[k]; v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

func shortTime(v string) string {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return "                "
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatSeconds(ms string) string {
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return "?"
	}
	return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64)
}

// printAuditSummary answers the questions a periodic review is actually for:
// how often is nvx falling out of the sandbox, and which warnings keep firing.
func printAuditSummary(entries []map[string]string) {
	modes := map[string]int{}
	reasons := map[string]int{}
	warnings := map[string]int{}
	failures := 0
	runs := 0

	for _, e := range entries {
		if e["event"] != "run" {
			continue
		}
		runs++
		modes[e["mode"]]++
		if e["exit"] != "0" {
			failures++
		}
		if r := e["reason"]; r != "" && e["mode"] != runModeSandboxed {
			reasons[r]++
		}
		for _, w := range strings.Split(e["warnings"], " | ") {
			if strings.TrimSpace(w) != "" {
				warnings[w]++
			}
		}
	}

	if runs == 0 {
		LogInfo("No run records in range.")
		return
	}

	fmt.Printf("%d runs, %d failed\n", runs, failures)
	fmt.Println("\nContainment:")
	for _, kv := range rankCounts(modes) {
		fmt.Printf("  %-10s %d\n", kv.key, kv.n)
	}
	if len(reasons) > 0 {
		fmt.Println("\nWhy not contained:")
		for _, kv := range rankCounts(reasons) {
			fmt.Printf("  %4d  %s\n", kv.n, kv.key)
		}
	}
	if len(warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, kv := range rankCounts(warnings) {
			fmt.Printf("  %4d  %s\n", kv.n, kv.key)
		}
	}
}

type countedKey struct {
	key string
	n   int
}

// rankCounts orders by frequency, then by name so equal counts do not shuffle
// between runs of the same command.
func rankCounts(m map[string]int) []countedKey {
	out := make([]countedKey, 0, len(m))
	for k, n := range m {
		out = append(out, countedKey{k, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].key < out[j].key
	})
	return out
}
