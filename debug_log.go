package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// What nvx said during a run, kept on disk so a problem can be looked at after
// the fact instead of being reconstructed from memory.
//
// The gap this fills: audit.log records 16 kinds of event, and everything else
// nvx prints goes to stderr and is gone when the terminal scrolls. So a run that
// warned "Containment removed 96 environment variables" left no trace, and the
// only way to find out what happened was to reproduce it while watching. For
// anything running unattended -- an agent harness, a CI step, a build someone
// else ran -- that is not available, which is exactly when the question gets
// asked.
//
// Off unless NVX_DEBUG is set, matching NVX_TRACE. On costs one append per line.
//
// THIS FILE HOLDS RENDERED TEXT, and audit.log deliberately does not. LogWarn
// records the format string precisely because rendering once wrote a live
// password into audit.log -- `nvx npm install https://deploy:s3cr3t@host/p.git`
// reaching a "%s" -- and run_trace.go keeps argv out for the same reason. That
// invariant is untouched: this is a separate file, written only when someone
// turns it on, because a debug log with the values redacted out of it does not
// answer the question it was turned on to answer. The file says so in its own
// header, and `nvx report` says so before handing it over.

const (
	debugLogEnvVar = "NVX_DEBUG"
	// debugLogMaxBytes caps the file. Reached, it rotates once and starts again,
	// so a machine left with NVX_DEBUG set cannot fill its disk.
	debugLogMaxBytes = 5 << 20
)

var (
	debugLogOnce sync.Once
	debugLogFile *os.File
	debugLogMu   sync.Mutex
)

// debugLogEnabled reports whether this run should capture what it prints.
func debugLogEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(debugLogEnvVar)))
	return v != "" && v != "0" && v != "false" && v != "no"
}

// debugLogPath is where the capture goes.
func debugLogPath(nvxHome string) string {
	return filepath.Join(nvxHome, "debug.log")
}

// debugCapture appends one rendered line, if capture is on.
//
// Failures here are swallowed on purpose: this is a debugging aid, and a command
// that stopped working because its log file could not be opened would be a worse
// bug than the one being chased.
func debugCapture(level, rendered string) {
	if !debugLogEnabled() {
		return
	}
	debugLogOnce.Do(openDebugLog)

	debugLogMu.Lock()
	defer debugLogMu.Unlock()
	if debugLogFile == nil {
		return
	}
	line := fmt.Sprintf("%s pid=%d %-7s %s\n",
		time.Now().UTC().Format(time.RFC3339), os.Getpid(), level, flattenForLog(rendered))
	_, _ = debugLogFile.WriteString(line)
}

// openDebugLog opens the capture file, rotating it first if it has grown past
// the cap, and writes a header saying what the file may contain.
func openDebugLog() {
	nvxHome := GetHomeDir()
	if err := os.MkdirAll(nvxHome, 0o755); err != nil { // #nosec G301 -- nvxHome is not secret
		return
	}
	path := debugLogPath(nvxHome)

	if info, err := os.Stat(path); err == nil && info.Size() > debugLogMaxBytes {
		// One generation. Two would be a retention policy nobody asked for.
		_ = os.Remove(path + ".old")
		_ = os.Rename(path, path+".old")
	}

	fresh := false
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fresh = true
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	if fresh {
		_, _ = f.WriteString(
			"# nvx debug capture. Written only while NVX_DEBUG is set.\n" +
				"# Unlike audit.log, these lines are RENDERED, so they can contain paths,\n" +
				"# hostnames, package names and anything else nvx printed -- including a\n" +
				"# credential embedded in a URL you passed on the command line.\n" +
				"# Read it before sending it to anyone.\n")
	}
	debugLogFile = f

	// The run itself is context the individual lines do not carry.
	cwd, _ := os.Getwd()
	_, _ = f.WriteString(fmt.Sprintf("%s pid=%d run     argv=%s cwd=%s\n",
		time.Now().UTC().Format(time.RFC3339), os.Getpid(),
		flattenForLog(strings.Join(redactArgsForDebug(os.Args), " ")), flattenForLog(cwd)))
}

// redactArgsForDebug removes the obvious secret from a command line: a URL with
// credentials in it, which is the exact case that put a password in audit.log.
//
// It is a mitigation, not a guarantee, and the file's header says so. A secret
// passed as a bare argument is indistinguishable from a package name.
func redactArgsForDebug(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, redactURLCredentials(a))
	}
	return out
}

// redactURLCredentials replaces the userinfo in a scheme://user:pass@host URL.
func redactURLCredentials(s string) string {
	at := strings.LastIndex(s, "@")
	scheme := strings.Index(s, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return s
	}
	return s[:scheme+3] + "<redacted>" + s[at:]
}
