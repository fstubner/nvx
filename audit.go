package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// auditLog appends a single JSON-lines record to ~/.nvx/audit.log. It is
// best-effort: any failure is reported once and never blocks the caller.
//
// Every value is flattened before it is stored. Several of these fields come
// from the contained process itself -- a SOCKS5 request carries an arbitrary
// byte string as the destination host, and that host is what `egress_deny`
// records -- so a newline in one forged an entire extra row in `nvx audit`
// output, and an escape sequence could clear the screen above a denial. A tool
// whose job is reporting which runs were contained could be made to print that
// an uncontained one was.
//
// Sanitising here rather than at each print site because the log is read by
// other things too, and because the previous fix covered only the `warnings`
// field while `host`, `tool`, `project`, `path` and `action` all reach the same
// renderer. Found by acceptance review.
func auditLog(nvxHome, event string, fields map[string]string) {
	if nvxHome == "" || event == "" {
		return
	}
	// Rotated here rather than only on the run-record path, where it was: with
	// NVX_TRACE off -- the default -- no cap applied at all and security events
	// grew without limit, exactly as they had before the cap was announced.
	rotateAuditLog(nvxHome)
	entry := map[string]interface{}{
		"time":  time.Now().UTC().Format(time.RFC3339),
		"pid":   os.Getpid(),
		"event": flattenForLog(event),
	}
	if cwd, err := os.Getwd(); err == nil {
		entry["cwd"] = flattenForLog(cwd)
	}
	// flattenAuditEntry rather than flattening each value directly, because the
	// warnings field is a " | "-separated list and flattenForLog maps `|` to a
	// space -- a rule added to stop one warning counting as two, which applied
	// to the joined string destroyed the separator it was protecting. Two
	// warnings then arrived as one run-on string and `--summary` counted them
	// wrong. Same helper on both sides now, so write and read cannot disagree.
	for k, v := range flattenAuditEntry(fields) {
		entry[k] = v
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(filepath.Join(nvxHome, "audit.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
