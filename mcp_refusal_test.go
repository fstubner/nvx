package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The invariant that matters most: a run that is NOT refused must never have its
// standard input touched. That input carries the client's `initialize` request,
// and the server nvx is about to launch is the thing that has to answer it.
//
// Asserting only that the call returns false does not test this -- it returns
// false for several reasons, so it passed with the guard deleted. The assertion
// has to be that the request is still there afterwards.
func TestNothingIsReadFromStdinWhenNothingWasRefused(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	const request = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	go func() { _, _ = w.WriteString(request); _ = w.Close() }()

	prev := refusalStdin
	refusalStdin = r
	t.Cleanup(func() { refusalStdin = prev })

	if reportRefusalOverStdio("", "some-package") {
		t.Fatal("a refusal was reported when nothing was refused")
	}

	// The request must still be waiting for the server that is about to start.
	got, ok := readPendingJSONRPCRequest(r, 2*time.Second)
	if !ok {
		t.Fatal("the client's request was consumed on a path that refused nothing; the server nvx launches would never receive it")
	}
	if string(got.ID) != "1" {
		t.Fatalf("id = %s, want the untouched request's 1", got.ID)
	}
}

// Only a real JSON-RPC request gets an answer. Anything else and nvx must exit
// exactly as it did before, so a non-MCP caller that pipes input and parses
// output never sees a byte it did not expect.
func TestOnlyAJSONRPCRequestIsAnswered(t *testing.T) {
	cases := map[string]string{
		"not JSON at all":        "hello\n",
		"JSON but not JSON-RPC":  `{"hello":"world"}` + "\n",
		"a notification, no id":  `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n",
		"wrong protocol version": `{"jsonrpc":"1.0","id":1,"method":"initialize"}` + "\n",
		"an id but no method":    `{"jsonrpc":"2.0","id":1}` + "\n",
		"nothing at all":         "",
	}
	for name, input := range cases {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		go func() { _, _ = w.WriteString(input); _ = w.Close() }()
		if _, ok := readPendingJSONRPCRequest(r, 2*time.Second); ok {
			t.Errorf("%s: was treated as a request to answer", name)
		}
		_ = r.Close()
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	go func() {
		_, _ = w.WriteString(`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{}}` + "\n")
		_ = w.Close()
	}()
	req, ok := readPendingJSONRPCRequest(r, 2*time.Second)
	_ = r.Close()
	if !ok {
		t.Fatal("a genuine initialize request was not recognised, so the client would still see only a closed pipe")
	}
	if string(req.ID) != "7" {
		t.Fatalf("id = %s, want 7: an answer with the wrong id is not an answer", req.ID)
	}
}

// A client that has sent nothing must not hold the process open. This file exists
// because a failure was invisible; hanging instead would be worse.
func TestASilentClientDoesNotHoldTheProcessOpen(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer w.Close()
	defer r.Close()

	start := time.Now()
	if _, ok := readPendingJSONRPCRequest(r, 200*time.Millisecond); ok {
		t.Fatal("a request was reported from a stream that sent nothing")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("waited %s on a silent stream; the read is not bounded", elapsed)
	}
}

// A package specifier can be a URL with credentials in it. The message goes
// somewhere a client may log, so only a plain name is ever echoed.
func TestOnlyAPlainPackageNameIsEchoed(t *testing.T) {
	for _, safe := range []string{"cowsay", "cowsay@1.6.0", "@scope/pkg", "@scope/pkg@1.2.3", "some.pkg_name-1"} {
		if safePackageLabel(safe) != safe {
			t.Errorf("%q is a plain package name and was withheld", safe)
		}
	}
	for _, unsafe := range []string{
		"https://deploy:s3cr3t@git.internal/pkg.git",
		"git+ssh://git@host/repo.git",
		"user:password@host",
		"pkg; rm -rf /",
		strings.Repeat("a", 200),
	} {
		if got := safePackageLabel(unsafe); got != "" {
			t.Errorf("%q was echoed as %q; it is not a plain package name", unsafe, got)
		}
	}
}

// The advice has to match the refusal. A policy decision has no prompt to
// approve, so telling someone to set NVX_YES sends them to try something that
// cannot work.
//
// The "could not be read" reason contains the words "security policy" as well, so
// its branch was unreachable behind the policy case and people were told to edit
// a policy nvx could not read.
func TestTheRemedyMatchesTheRefusal(t *testing.T) {
	cases := map[string]string{
		"its security policy could not be read":                                     "nvx doctor",
		"the security policy blocks one of its packages":                            "policy decision",
		"the security policy disallows package install scripts":                     "policy decision",
		"a global install cannot be run inside the sandbox":                         "outside the sandbox",
		"a package version was published inside the release-age cooling-off window": "NVX_YES",
		"a package looked like a typosquat and the warning was not approved":        "NVX_YES",
	}
	for reason, want := range cases {
		got := remedyFor(reason)
		if !strings.Contains(got, want) {
			t.Errorf("reason %q got remedy %q, which does not mention %q", reason, got, want)
		}
	}

	// A policy decision must never be answered with "approve the prompt".
	for _, reason := range []string{
		"the security policy blocks one of its packages",
		"the security policy disallows package install scripts",
		"its security policy could not be read",
	} {
		if strings.Contains(remedyFor(reason), "NVX_YES") {
			t.Errorf("reason %q was answered with NVX_YES, which cannot affect a policy decision", reason)
		}
	}
}
