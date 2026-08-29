package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Telling an MCP client why nvx refused, instead of letting it look like a crash.
//
// A stdio MCP server is launched as a child process and talks JSON-RPC over its
// standard input and output. When nvx refuses to run one -- a blocked package, a
// typosquat nobody approved, a version published inside the cooling-off window --
// it writes the reason to standard error and exits non-zero. The client sees a
// process that closed the pipe without answering, and reports "Connection closed".
// The reason is on a stream most clients discard.
//
// That is indistinguishable from the server being broken, and it cost a real
// debugging session: a package published 7.3 hours earlier tripped the
// release-age window, and the failure was first taken for an unrelated transport
// bug that produces the identical message. It also resolves itself after 24
// hours, so the server "works again" without anyone learning what happened.
//
// The only channel that reaches the client's UI is standard output, and the only
// thing it will read there is JSON-RPC. So on the refusal path -- and only there
// -- nvx answers the request the client has already sent, with an error carrying
// the reason. "Connection closed" becomes something the person can act on.
//
// Deliberately narrow:
//
//   - Only when refusing. nvx never reads this input on a path that goes on to
//     launch the real server, so it cannot swallow the client's first request.
//   - Only when the pending input really is a JSON-RPC request. Anything else and
//     nvx exits exactly as before, so a non-MCP caller that pipes input and parses
//     output sees no new bytes.
//   - Only fixed, developer-authored text. A package specifier can be a URL with
//     credentials in it (see the note on LogWarn), and this goes somewhere a
//     client may log.
//   - Bounded. A client that has not sent anything yet is not waited on.

// refusalStdin is the stream a waiting client's request is read from, held in a
// variable so a test can prove the one invariant that cannot be observed
// otherwise: that nothing is read at all when nothing was refused. The request on
// that stream belongs to the server nvx is about to launch, and consuming it
// would break the very thing this file is trying to keep working.
var refusalStdin = os.Stdin

// mcpRefusalReadTimeout bounds the wait for the client's first request. An MCP
// client sends `initialize` as soon as it has spawned the server, so this only
// has to cover process startup; anything longer just delays an exit.
var mcpRefusalReadTimeout = 2 * time.Second

// jsonRPCRequest is the part of an incoming message nvx needs: enough to confirm
// this really is JSON-RPC, and the id to answer.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
}

// safePackageLabel returns pkg if it is plainly an npm package name, and ""
// otherwise.
//
// A package specifier can be a URL with credentials in it -- `npm install
// https://deploy:s3cr3t@git.internal/pkg.git` is a real shape, and the note on
// LogWarn exists because one reached the audit log that way. This message goes to
// a client that may log it, so anything that is not obviously a bare name is left
// out and the reader is pointed at stderr instead.
func safePackageLabel(pkg string) string {
	if pkg == "" || len(pkg) > 128 || strings.Contains(pkg, "://") {
		return ""
	}
	for i, r := range pkg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '.' || r == '_' || r == '/' || r == '@':
			// "@" only as a scope marker or a version separator, never as the
			// user:password@host divider a URL would use.
			if r == '@' && i != 0 && !strings.Contains(pkg[:i], "/") && i != strings.LastIndex(pkg, "@") {
				return ""
			}
		default:
			return ""
		}
	}
	return pkg
}

// reportRefusalOverStdio answers a waiting MCP client, and reports whether it
// did.
//
// reason must be a fixed, developer-authored string. pkg is included only if
// safePackageLabel accepts it.
func reportRefusalOverStdio(reason, pkg string) bool {
	// No reason means nothing was refused. Guarding here rather than only at the
	// call site makes the invariant that matters most testable: this must never
	// touch stdin on a path that goes on to launch the real server, because the
	// request it would consume is the one that server needs to answer.
	if reason == "" {
		return false
	}
	// A person at a terminal already has the reason on screen, and writing
	// JSON-RPC at them would be noise.
	if stdinIsInteractive() {
		return false
	}

	req, ok := readPendingJSONRPCRequest(refusalStdin, mcpRefusalReadTimeout)
	if !ok {
		return false
	}

	message := "nvx did not start this server: " + reason
	if label := safePackageLabel(pkg); label != "" {
		message += " (" + label + ")"
	}

	// -32000 is the JSON-RPC "server error" range, which is what an MCP client
	// displays rather than swallowing as a protocol fault.
	//
	// The remedy is carried with the reason because for a spawned server there is
	// no other moment to offer it: nvx's warnings are prompts, and a server started
	// by a client can never answer one, so what reads as "a warning" is in practice
	// a refusal with an override the person has no way to discover.
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"error": map[string]any{
			"code":    -32000,
			"message": message,
			"data": map[string]any{
				"remedy": "Set NVX_YES=true in this server's environment to approve nvx's warnings for it, " +
					"or pin the command to a version you have already used. " +
					"The full reason is on this server's error output and in nvx's audit log.",
			},
		},
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(os.Stdout, "%s\n", body); err != nil {
		return false
	}
	return true
}

// readPendingJSONRPCRequest reads one line and returns it if it is a JSON-RPC
// request with an id to answer.
//
// The read runs on its own goroutine because standard input has no deadline: a
// client that sends nothing would otherwise hold the process open for ever, which
// is the failure this whole file exists to avoid causing a second version of.
// The goroutine is abandoned rather than cancelled -- the process is exiting.
func readPendingJSONRPCRequest(r *os.File, timeout time.Duration) (jsonRPCRequest, bool) {
	type result struct {
		req jsonRPCRequest
		ok  bool
	}
	done := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		// One request line. MCP frames these newline-delimited; the cap keeps a
		// stream that never sends a newline from growing without bound.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		if !scanner.Scan() {
			done <- result{}
			return
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			done <- result{}
			return
		}
		// An id is required: a notification cannot be answered, and something that
		// is not JSON-RPC at all must not be replied to.
		if req.JSONRPC != "2.0" || req.Method == "" || len(req.ID) == 0 {
			done <- result{}
			return
		}
		done <- result{req: req, ok: true}
	}()

	select {
	case res := <-done:
		return res.req, res.ok
	case <-time.After(timeout):
		return jsonRPCRequest{}, false
	}
}

// firstPackageLabel names the packages a refusal was about, for the message.
// Only the first, and only if it is safe to echo: a list is rarely more useful
// than one name, and every extra specifier is another chance to print a URL.
func firstPackageLabel(pkgs []string) string {
	if len(pkgs) == 0 {
		return ""
	}
	return pkgs[0]
}
