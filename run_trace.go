package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A record of what nvx actually did, for reviewing later rather than reading in
// the moment.
//
// `~/.nvx/audit.log` already exists, but it only holds security decisions --
// egress allow/deny, grants, policy pins. Nothing records that a command ran at
// all, so questions that only appear over many runs have no evidence behind
// them: how often is something falling out of the sandbox, which warnings fire
// repeatedly and get scrolled past, what is slow.
//
// Prompted by a dogfooding run (2026-08-21) that ended fine but printed "a
// runtime dir is ahead of nvx's shim dir on PATH" partway through several
// screens of npm output. That warning is worth acting on and easy to miss once.
// Reviewed across a week of runs it is obvious.
//
// Local only. Nothing is sent anywhere, and this stays true: the file is on the
// user's own disk and there is no uploader. "Telemetry" in the usual sense --
// phoning home from a security tool -- would be the wrong trade for a product
// whose whole claim is about what code is allowed to reach.
//
// Records go in the same audit.log rather than a new file, because interleaving
// is the point: a run record sitting next to the egress denials it caused is the
// trace. Volume is not a concern -- these are human-typed commands.

// runTrace accumulates what one nvx invocation did. Zero value is inert, so a
// code path that never begins a trace costs nothing and cannot panic.
type runTrace struct {
	nvxHome string
	started time.Time
	command string
	action  string
	mode    string
	reason  string
	top     bool
}

// runModeRefused and friends name how the invocation was handled. Recorded as
// the answer to "was this contained", which is the question a review is for.
const (
	runModeSandboxed = "sandboxed"
	runModeDirect    = "direct"
	runModeRefused   = "refused"
)

// beginRunTrace starts timing an invocation.
//
// Only the outermost nvx in a process tree is traced. A single typed command
// routinely spawns a tree of further shimmed processes -- an npm lifecycle
// script alone can nest prepublishOnly -> build -> clean -> node -- and
// recording each would turn one run into a dozen records that all look like
// separate user actions.
func beginRunTrace(nvxHome, command string, args []string) *runTrace {
	return &runTrace{
		nvxHome: nvxHome,
		started: time.Now(),
		command: command,
		action:  firstAction(command, args),
		top:     isTopLevelShimInvocation(),
	}
}

// isTop reports whether this is the outermost nvx in the process tree, nil-safe
// so an untraced code path reads as "not top" rather than panicking.
func (t *runTrace) isTop() bool { return t != nil && t.top }

// note records how the invocation was handled, and why when that is not obvious.
func (t *runTrace) note(mode, reason string) {
	if t == nil {
		return
	}
	t.mode = mode
	t.reason = reason
}

// finish writes the record. Best-effort, like every other audit write: a run
// that already succeeded must not fail because its trace could not be stored.
func (t *runTrace) finish(exitCode int) {
	if t == nil || !t.top || t.nvxHome == "" {
		return
	}
	rotateAuditLog(t.nvxHome)

	fields := map[string]string{
		"command":     t.command,
		"mode":        t.mode,
		"exit":        strconv.Itoa(exitCode),
		"duration_ms": strconv.FormatInt(time.Since(t.started).Milliseconds(), 10),
	}
	if t.action != "" {
		fields["action"] = t.action
	}
	if t.reason != "" {
		fields["reason"] = t.reason
	}
	if w := collectedWarnings(); len(w) > 0 {
		fields["warnings"] = strings.Join(w, " | ")
	}
	auditLog(t.nvxHome, "run", fields)
}

// firstAction picks the subcommand out of the arguments -- "install", "trust",
// "run".
//
// Deliberately NOT the full argument list. Arguments carry registry tokens
// (`npm config set //registry/:_authToken=...`), file paths and package specs,
// and this file is exactly the thing a user would paste into a bug report. The
// subcommand answers what a review needs -- which kinds of command misbehave --
// without turning a log into a place secrets accumulate.
func firstAction(cmd string, args []string) string {
	// An ad-hoc tool runner has no subcommand -- its first positional IS the
	// package. `nvx npx -y acme-internal-deploy-2024` was recording a private
	// tool name as if it were a verb, which is the thing this function refuses
	// to do everywhere else.
	if executorCommands[strings.ToLower(cmd)] {
		return ""
	}
	for _, a := range args {
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Only step over flags known to take no value. Anything else may be
			// a value-taking flag, and its value is the next argument -- `node -e
			// "<script>"`, `npm --otp <code>`. Skipping an unrecognised flag
			// records that value as if it were a subcommand, which is how the
			// secret this function exists to avoid gets in anyway. Unknown flag
			// means stop.
			// Matched case-sensitively. Lowercasing first made `-D` (the map's
			// only uppercase key) never match, while making `-F`, `-G`, `-Q` and
			// `-Y` match their lowercase entries -- so `pnpm -F mypkg build`
			// stepped over -F and recorded the filter value as the subcommand.
			// Flags are case-sensitive to the tools themselves; -D and -d are
			// different flags.
			if valuelessFlags[a] || strings.Contains(a, "=") {
				continue
			}
			return ""
		}
		// A package spec is not a subcommand, and it is the argument most likely
		// to be a long private URL or a scoped internal name.
		if strings.ContainsAny(a, "/\\@:") {
			return ""
		}
		if len(a) > 32 {
			return ""
		}
		return a
	}
	return ""
}

// valuelessFlags are boolean flags common enough to sit before a subcommand.
//
// An allowlist rather than a list of value-taking flags to avoid: guessing wrong
// about a flag nvx has never heard of should end in recording nothing, not in
// recording the argument after it.
var valuelessFlags = map[string]bool{
	"-y": true, "--yes": true,
	"-q": true, "--quiet": true, "--silent": true,
	"-g": true, "--global": true,
	"-D": true, "--save-dev": true, "--save": true, "--no-save": true,
	"-f": true, "--force": true,
	"--dry-run": true, "--no-fund": true, "--no-audit": true, "--json": true,
}

// Warnings are collected as they are printed, so a trace can say which fired.
//
// A warning is the one output nvx produces that the user is expected to act on
// later rather than now, which makes it the thing most worth reviewing in
// aggregate and the thing least likely to survive a screen of npm output.
var (
	warningsMu   sync.Mutex
	seenWarnings []string
)

// maxTracedWarnings caps what one record holds. A run stuck in a loop emitting
// the same warning must not be able to grow the log without bound.
const maxTracedWarnings = 10

// maxWarningChars keeps a single long warning from dominating a record.
const maxWarningChars = 200

func recordWarning(text string) {
	text = flattenForLog(strings.TrimSpace(text))
	if text == "" {
		return
	}
	if len(text) > maxWarningChars {
		text = text[:maxWarningChars] + "..."
	}
	warningsMu.Lock()
	defer warningsMu.Unlock()
	for _, existing := range seenWarnings {
		if existing == text {
			return // the same warning twice in one run is one fact
		}
	}
	if len(seenWarnings) >= maxTracedWarnings {
		return
	}
	seenWarnings = append(seenWarnings, text)
}

// flattenForLog collapses newlines, control characters and the field separator
// into spaces.
//
// `nvx audit` prints a warning on its own indented line, so a warning containing
// a newline forges a complete extra row -- a fabricated "sandboxed" run in a
// listing whose entire purpose is telling you which runs were contained.
// Multi-line warnings are real (the typosquat prompt is built with embedded
// newlines), so this is not hypothetical.
//
// Fixed on write rather than on print: the log is read by other tools too, and a
// record that cannot be misread is worth more than a printer that copes.
func flattenForLog(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		// The reader splits the warnings field on " | ", so a warning containing
		// that sequence would otherwise count as two in the summary.
		if r < 0x20 || r == 0x7f || r == '|' {
			r = ' '
		}
		if r == ' ' {
			if lastSpace {
				continue
			}
			lastSpace = true
		} else {
			lastSpace = false
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func collectedWarnings() []string {
	warningsMu.Lock()
	defer warningsMu.Unlock()
	return append([]string(nil), seenWarnings...)
}

// maxAuditBytes bounds the audit log.
//
// It grew without limit before this file existed, which was tolerable when it
// only recorded egress decisions. A record per run makes it grow with use, so
// the cap arrives with the thing that causes the growth rather than later.
const maxAuditBytes = 4 << 20

// rotateAuditLog keeps one previous generation and starts a fresh file.
//
// One generation, not many: this is a dogfooding trace, and the value is in
// recent history. Someone who needs more can copy the file.
//
// The remove-then-rename it used to do could lose EVERYTHING when two nvx
// processes rotated at once: A removes .1, A renames log to .1, B removes .1
// (deleting the history A just rotated), B's rename fails silently because the
// source is gone. Both generations destroyed, no error, and nothing to notice it
// by.
//
// A single rename onto the destination is atomic on POSIX and, via
// MoveFileEx with MOVEFILE_REPLACE_EXISTING, on Windows too -- so the worst a
// concurrent pair can now do is rotate twice, which costs one generation rather
// than all of it. Go's os.Rename uses MoveFileEx with that flag on Windows.
func rotateAuditLog(nvxHome string) {
	path := filepath.Join(nvxHome, "audit.log")
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxAuditBytes {
		return
	}
	_ = os.Rename(path, path+".1")
}

// describeSandboxSkip turns a "not contained" decision into a reason worth
// reading in a trace.
//
// It re-asks the same questions shouldSandbox asks, in the same order, and only
// when the answer was already known to be false -- so it can report which one
// applied. Kept beside the decision rather than folded into it because
// shouldSandbox is called for its answer in security-relevant places and should
// stay a plain predicate.
func describeSandboxSkip(cmdName string, args []string, policy Policy, opts shimOptions) string {
	switch {
	case noSandboxFlag:
		return "--no-sandbox"
	case inSandboxSession():
		return "already inside a sandbox"
	// Only when it was actually honoured. shouldSandbox ignores NVX_SANDBOX when
	// it can prove the process is not inside a sandbox, so reporting it as the
	// cause named a reason nvx had explicitly rejected -- and let anything that
	// can set an environment variable, a postinstall script included, write a
	// false cause into the summary whose entire job is "why was this not
	// contained".
	case (os.Getenv("NVX_SANDBOX") == "1" || os.Getenv("NVX_SANDBOX") == "true") && !containmentDisproved():
		return "NVX_SANDBOX set"
	case !policy.Isolation.Enabled:
		return "isolation disabled by policy"
	}
	if !isWrappedCommand(cmdName) {
		return "not a wrapped command"
	}
	return fmt.Sprintf("%s at %s isolation", classifyInvocation(cmdName, args), policy.IsolationLevel())
}
