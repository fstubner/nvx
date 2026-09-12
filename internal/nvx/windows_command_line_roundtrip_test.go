//go:build windows

package nvx

import (
	"syscall"
	"testing"
	"unsafe"
)

// Every argument nvx puts on a Windows command line comes back out of the
// system's parser exactly as it went in.
//
// CreateProcess takes one string, and the child's runtime splits it again
// with the rules CommandLineToArgvW implements. Those rules are asymmetric
// about backslashes: a backslash is literal unless it precedes a double
// quote, in which case each backslash in the run must be doubled and the quote
// escaped. quoteWindowsArg doubled a trailing run correctly and did not double
// a run before a quote, so `a\"b` was rendered as "a\\"b" -- which the parser
// reads as `a\` followed by an unquoted b. An argument to a contained tool that
// contained a backslash before a quote -- a Windows path inside a quoted JSON
// string, say -- reached the tool changed.
//
// Asserted against the real parser rather than a hand-written expectation, so
// the test cannot agree with the code by construction.
func TestWindowsCommandLineRoundTripsThroughTheSystemParser(t *testing.T) {
	for _, arg := range []string{
		`plain`,
		`has space`,
		`C:\dir\file.txt`,
		`C:\dir\`,      // trailing backslash, unquoted
		`C:\dir with\`, // trailing backslash, quoted
		`a\"b`,         // one backslash before a quote
		`a\\"b`,        // two backslashes before a quote
		`a\\\"b`,       // three
		`"quoted"`,
		`{"path":"C:\\x"}`, // JSON with an escaped backslash before a quote
		`tab	here`,
		``,
		`\`,
		`\\`,
	} {
		t.Run(arg, func(t *testing.T) {
			line := buildWindowsArgString([]string{`C:\exe.exe`, arg})
			got := parseWithSystem(t, line)
			if len(got) != 2 {
				t.Fatalf("%q rendered as %q, which the system parser split into %d arguments: %q", arg, line, len(got), got)
			}
			if got[1] != arg {
				t.Fatalf("%q rendered as %q and came back as %q", arg, line, got[1])
			}
		})
	}
}

func parseWithSystem(t *testing.T, line string) []string {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(line)
	if err != nil {
		t.Fatal(err)
	}
	var argc int32
	argv, err := syscall.CommandLineToArgv(p, &argc)
	if err != nil {
		t.Fatalf("CommandLineToArgv(%q): %v", line, err)
	}
	defer syscall.LocalFree(syscall.Handle(uintptr(unsafe.Pointer(argv))))
	out := make([]string, 0, argc)
	for i := 0; i < int(argc); i++ {
		out = append(out, syscall.UTF16ToString(argv[i][:]))
	}
	return out
}
