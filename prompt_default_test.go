package main

import (
	"errors"
	"testing"
)

// Every prompt nvx asks is a question about whether to permit something —
// trusting a project policy that loosens settings, approving an outbound
// connection, granting a trusted tool, proceeding past active vulnerabilities.
// None is a routine confirmation, so the default answer has to be no.
//
// It was yes. The prompt read `[Y/n]` and \r and \n were treated as approval, so
// a stray newline in the console buffer approved whatever was being asked. That
// was the one step on this path that did not fail closed: non-interactive denies,
// -y / --agent-mode / NVX_YES deliberately do not approve, and only NVX_TRUST_YES
// does.
func TestOnlyAnExplicitYApprovesAPrompt(t *testing.T) {
	approves := []struct {
		name string
		in   string
	}{
		{"lowercase y", "y"},
		{"uppercase Y", "Y"},
		{"y then newline", "y\r\n"},
		{"yes", "yes\n"},
	}
	for _, tc := range approves {
		t.Run("approves/"+tc.name, func(t *testing.T) {
			if !promptAnswerApproves([]byte(tc.in), len(tc.in), nil) {
				t.Errorf("%q must approve; a prompt nobody can say yes to is not a prompt", tc.in)
			}
		})
	}

	denies := []struct {
		name string
		in   string
	}{
		{"bare Enter (CR)", "\r"},
		{"bare Enter (LF)", "\n"},
		{"CRLF", "\r\n"},
		{"n", "n"},
		{"N", "N"},
		{"no", "no\n"},
		{"a stray space", " "},
		{"an unrelated key", "q"},
		{"escape sequence", "\x1b[A"},
	}
	for _, tc := range denies {
		t.Run("denies/"+tc.name, func(t *testing.T) {
			if promptAnswerApproves([]byte(tc.in), len(tc.in), nil) {
				t.Errorf("%q must NOT approve; the default answer to a security question is no", tc.in)
			}
		})
	}
}

// A read that fails or returns nothing is not an answer, and must not be taken
// for one. Closing the console, a zero-length read and an error all land here.
func TestAFailedPromptReadDenies(t *testing.T) {
	if promptAnswerApproves([]byte("y"), 1, errors.New("read failed")) {
		t.Error("a read error approved; an unreadable console must deny")
	}
	if promptAnswerApproves([]byte("y"), 0, nil) {
		t.Error("a zero-length read approved")
	}
	if promptAnswerApproves(nil, 1, nil) {
		t.Error("an empty buffer approved")
	}
}
