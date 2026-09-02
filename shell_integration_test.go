package main

// Helpers and assertions for the PowerShell integration text, which envScript
// produces on every platform -- so these live outside the windows-only file.
//
// They were in it at first, and doctor_test.go (which has no build tag) calls
// decodedPathsContain. That compiles on Windows and nowhere else, and `go build`
// does not compile test files, so a three-platform build check passed while the
// macOS and Linux unit jobs could not build at all. Use `go vet` to check another
// platform; it compiles the tests.

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

var psBase64Path = regexp.MustCompile(`FromBase64String\('([A-Za-z0-9+/=]*)'\)`)

// powershellDecodedPaths returns the paths a PowerShell snippet decodes at
// runtime, so a test can assert the VALUE the shell ends up with rather than how
// it happens to be spelled on the wire.
func powershellDecodedPaths(script string) []string {
	var out []string
	for _, m := range psBase64Path.FindAllStringSubmatch(script, -1) {
		if raw, err := base64.StdEncoding.DecodeString(m[1]); err == nil {
			out = append(out, string(raw))
		}
	}
	return out
}

func decodedPathsContain(script, want string) bool {
	for _, p := range powershellDecodedPaths(script) {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

// A shim directory under a home with an accent in it must reach the shell intact.
//
// PowerShell decodes a native command's stdout with [Console]::OutputEncoding,
// a legacy OEM codepage by default, while nvx writes UTF-8. Measured 2026-09-02
// on Windows PowerShell 5.1 and pwsh 7 (both ibm850): the U+00E4 in the emitted
// path arrived as U+251C U+00F1, Test-Path on the result was False, and `node`
// was then not found -- with no error at any point. Any account named Müller,
// José or Łukasz got an nvx that never intercepted anything.
//
// So the emitted script must be pure ASCII: bytes no codepage can disagree about.
func TestPowerShellIntegrationSurvivesANonASCIIPath(t *testing.T) {
	const home = `C:\Users\Müller\.nvx\bin`
	const exe = `C:\Users\Müller\.nvx\bin\nvx.exe`
	script := envScript("powershell", exe, home)

	for i := 0; i < len(script); i++ {
		if script[i] > 127 {
			t.Fatalf("the PowerShell script carries a non-ASCII byte at offset %d, so a console "+
				"codepage that is not UTF-8 will corrupt it and the shim dir will name a "+
				"directory that does not exist. Script:\n%s", i, script)
		}
	}

	// Pure ASCII is only half of it: the script must still yield the real paths.
	if !decodedPathsContain(script, `Müller`) {
		t.Errorf("the script is ASCII but no longer carries the real path; it decodes to %v",
			powershellDecodedPaths(script))
	}
	if !decodedPathsContain(script, `nvx.exe`) {
		t.Errorf("the nvx binary path is not recoverable from the script: %v",
			powershellDecodedPaths(script))
	}
}
