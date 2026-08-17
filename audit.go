package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// auditLog appends a single JSON-lines record to ~/.nvx/audit.log. It is
// best-effort: any failure is reported once and never blocks the caller.
func auditLog(nvxHome, event string, fields map[string]string) {
	if nvxHome == "" || event == "" {
		return
	}
	entry := map[string]interface{}{
		"time":  time.Now().UTC().Format(time.RFC3339),
		"pid":   os.Getpid(),
		"event": event,
	}
	if cwd, err := os.Getwd(); err == nil {
		entry["cwd"] = cwd
	}
	for k, v := range fields {
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
