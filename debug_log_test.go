package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A credential in a URL argument does not go into the capture verbatim.
//
// This is the same leak that shaped audit.log: `nvx npm install
// https://deploy:s3cr3t@git.internal/pkg.git` once put a live password on disk,
// which is why LogWarn records format strings and run_trace keeps argv out. The
// debug capture records argv on purpose -- it is the first thing anyone needs --
// so the one shape of secret that can be recognised is removed.
//
// A mitigation, not a guarantee: a secret passed as a bare argument is
// indistinguishable from a package name, and the file header says so.
func TestACredentialInAURLIsNotWrittenToTheCapture(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://deploy:s3cr3t@git.internal/pkg.git", "https://<redacted>@git.internal/pkg.git"},
		{"http://user:pw@host/x", "http://<redacted>@host/x"},
		// Nothing to redact: left exactly alone.
		{"https://registry.npmjs.org/", "https://registry.npmjs.org/"},
		{"install", "install"},
		{"user@example.com", "user@example.com"},
	} {
		if got := redactURLCredentials(tc.in); got != tc.want {
			t.Errorf("redactURLCredentials(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if strings.Contains(redactURLCredentials("https://deploy:s3cr3t@h/p"), "s3cr3t") {
		t.Error("the password survived redaction")
	}
}

// Off unless asked for, and the ways of saying "off" all work.
func TestTheCaptureIsOffUnlessTurnedOn(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false}, {"0", false}, {"false", false}, {"no", false},
		{"1", true}, {"true", true}, {"yes", true},
	} {
		t.Setenv(debugLogEnvVar, tc.value)
		if got := debugLogEnabled(); got != tc.want {
			t.Errorf("NVX_DEBUG=%q: enabled = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// The file says what it holds, in the file.
//
// It carries rendered text, unlike audit.log, so someone about to paste it into
// an issue needs to be told before they do -- and the place that reaches them is
// the file itself, not documentation they did not read.
func TestTheCaptureWarnsAboutItsOwnContents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NVX_HOME", dir)
	t.Setenv(debugLogEnvVar, "1")

	// openDebugLog is guarded by a sync.Once in normal use; call it directly so
	// this test does not depend on whether another test opened it first.
	openDebugLog()
	t.Cleanup(func() {
		if debugLogFile != nil {
			_ = debugLogFile.Close()
			debugLogFile = nil
		}
	})

	body, err := os.ReadFile(filepath.Join(dir, "debug.log"))
	if err != nil {
		t.Fatalf("no capture file was created: %v", err)
	}
	for _, want := range []string{"RENDERED", "before sending it"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the capture header does not mention %q:\n%s", want, body)
		}
	}
}
