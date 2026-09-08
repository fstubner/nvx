package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every outbound dial in the package is one of a named, explained set.
//
// The egress allowlist judges a NAME; a connection reaches an ADDRESS. Anything
// that does those as two steps has a window between them, and both link-local
// holes of 2026-09-04/05 were that window: a path that ended in net.Dial with a
// hostname, so a second DNS answer went unjudged. Both were in fallback branches
// of the one function meant to be the choke point.
//
// After the second fix there is exactly one place that dials a destination the
// policy judged, dialVetted, and it dials an IP. That is a sound design ON THE
// CONDITION that nothing else ever dials by name -- and until this test, that
// condition held by convention. The whole program is one package, so package
// visibility cannot enforce it. This does.
//
// Every allowed site says why it is not an egress-by-name path. Adding a dial
// anywhere else fails here, which is the point: a new dial site is a security
// decision, and this makes it one that has to be written down.
func TestEveryOutboundDialIsANamedChokePoint(t *testing.T) {
	// (file, distinctive substring of the line) -> why it may dial.
	allowed := []struct{ file, line, why string }{
		{"egress_resolve.go", `net.Dial("tcp", net.JoinHostPort(ip.String()`,
			"dialVetted: THE choke point. Dials an address the policy already judged, never a name."},
		{"sandbox_connect_windows.go", `net.DialTimeout("tcp", c.hostAddr`,
			"--connect: reaches a host-side service at a literal loopback address the user named in the policy. Not resolved from a sandboxed process's request."},
		{"sandbox_connect_windows.go", `net.DialTimeout("unix", sock`,
			"--connect tunnel plumbing: a UNIX socket nvx itself created."},
		{"sandbox_expose_windows.go", `net.DialTimeout("unix", sock`,
			"--expose tunnel plumbing: a UNIX socket nvx itself created."},
		{"sandbox_expose_windows.go", `net.DialTimeout("tcp", local`,
			"--expose: the contained server's own loopback port, configured by the user. Not a name."},
		{"sandbox_connect_darwin.go", `net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(r.hostPort)`,
			"--connect on macOS: the same decision as the Windows site above, reached without a tunnel because " +
				"the sandbox shares the host's loopback there. A literal loopback address the user named on the " +
				"command line or in the policy; nothing a sandboxed process asked for is resolved here."},
		{"sandbox_loopback_redirect_linux.go", `net.DialTimeout("tcp", target, connectDialTimeout)`,
			"network.mode loopback: the parent dialling the host service a redirected connection was for. " +
				"THE enforcement point for that mode -- the address comes from the kernel's SO_ORIGINAL_DST, " +
				"never from a name, and the four lines above this refuse anything that is not loopback, because " +
				"the socket carrying the request sits where the contained process can reach it."},
		{"sandbox_loopback_redirect_linux.go", `net.Dial("unix", r.sock)`,
			"network.mode loopback plumbing: a UNIX socket nvx itself created in this run's guest home."},
		{"sandbox_loopback_redirect_linux.go", `d := net.Dialer{`,
			"network.mode loopback: reaching a service the SANDBOX is running, inside its own namespace. " +
				"Marked so the redirect rules skip it; it cannot leave the namespace, which is the point of trying " +
				"it before the host."},
		{"sandbox_connect_linux.go", `net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(hostPort)`,
			"--connect on Linux: the parent's half, outside the namespace. A literal loopback address the user " +
				"named; the contained side can only ask for the tunnel, never for a destination."},
		{"sandbox_connect_linux.go", `d.DialContext(ctx, "unix", sock)`,
			"--connect tunnel plumbing: a UNIX socket nvx itself created in this run's guest home."},
		{"sandbox_relay.go", `d.DialContext(ctx, "unix", sockPath)`,
			"in-container relay to the egress proxy over a UNIX socket nvx created; the proxy then applies the allowlist."},
	}

	dial := regexp.MustCompile(`\bnet\.Dial(Timeout)?\(|net\.Dialer\{|&net\.Dialer|\.DialContext\(`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(src), "\n") {
			code := line
			if i := strings.Index(code, "//"); i >= 0 {
				code = code[:i] // a comment naming net.Dial is not a dial
			}
			if !dial.MatchString(code) {
				continue
			}
			matched := -1
			for i, a := range allowed {
				if a.file == file && strings.Contains(line, a.line) {
					matched = i
					break
				}
			}
			if matched < 0 {
				t.Errorf("%s:%d dials outward and is not a named choke point:\n    %s\n"+
					"Every outbound connection must be one of the sites listed in this test, each with a reason "+
					"it cannot reach a destination the egress allowlist did not judge. If this dial is legitimate, "+
					"add it to the list WITH that reason; if it takes a hostname, route it through dialVetted instead.",
					file, n+1, strings.TrimSpace(line))
				continue
			}
			seen[matched] = true
		}
	}

	// The list must describe reality, not history: an entry whose site is gone
	// is a reason nobody can check.
	for i, a := range allowed {
		if !seen[i] {
			t.Errorf("allowed dial site no longer exists: %s containing %q. Remove the entry so the list stays true.",
				a.file, a.line)
		}
	}
}
