//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An MCP server must be able to complete its handshake while contained.
//
// This is the claim `docs/superpowers/specs/2026-08-20-mcp-server-containment-design.md`
// rests on, and it was measured once by hand against a real server. PRODUCT.md's
// honesty condition says a containment claim is either backed by a test that fails
// when it stops holding, or listed as a limitation -- so this is that test.
//
// What it verifies, stated narrowly because the wider claim was checked and did
// not hold: a contained server completes a real handshake end to end, through the
// real binary, with containment confirmed to have been applied. A test that
// launched the server and checked it was alive would pass against a server that
// never answers, which is why this speaks the protocol and reads the reply.
//
// What it does NOT guard, despite the obvious guess: the STARTF_USESTDHANDLES /
// bInheritHandles machinery in launchAppContainerProcessOnce. That was the F46
// fix, and it is tempting to assume an MCP handshake exercises it. Measured
// 2026-08-20 on this machine, it does not -- disabling those flags, and then
// disabling prepareInheritableStdio entirely, left both this test AND the
// dedicated TestPipedStdioReachesRealAppContainerChild passing. Stdio reaches a
// contained child here by some other route (console inheritance is the likely
// candidate, unconfirmed). So neither test discriminates that mechanism on this
// host, and claiming otherwise in a comment would be the kind of unbacked
// assertion PRODUCT.md's honesty condition exists to prevent.
//
// The server is written here rather than installed from npm on purpose: a test
// that needs the network to prove a local property fails for reasons that have
// nothing to do with the property.
func TestContainedMcpServerCompletesHandshake(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and creates a real AppContainer)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available on this host")
	}

	project := tempDir(t)
	if err := os.WriteFile(filepath.Join(project, "package.json"),
		[]byte(`{"name":"nvx-mcp-test","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "server.js"), []byte(minimalMcpServerJS), 0o600); err != nil {
		t.Fatal(err)
	}

	nvxExe := filepath.Join(tempDir(t), "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}

	// --strict so the server itself is contained, not just treated as project code.
	cmd := exec.Command(nvxExe, "-y", "--strict", "shim", "node", "server.js")
	cmd.Dir = project
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start contained server: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// MCP's stdio transport is newline-delimited JSON, not LSP-style
	// Content-Length framing. A server handed the wrong framing never answers,
	// which is indistinguishable from a hang -- that cost a run during the
	// investigation this test came out of.
	send := func(v any) {
		b, merr := json.Marshal(v)
		if merr != nil {
			t.Fatal(merr)
		}
		if _, werr := stdin.Write(append(b, '\n')); werr != nil {
			t.Fatalf("write to contained server: %v (stderr: %s)", werr, tail(stderr.String()))
		}
	}

	replies := make(chan map[string]any, 4)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				replies <- m
			}
		}
		readErr <- sc.Err()
	}()

	await := func(what string, wantID float64) map[string]any {
		// Generous: the first contained launch on freshly written files pays a
		// one-time cold cost while the filesystem and AV caches fill (~6s measured
		// on a 2,552-file tree; this project is two files, so far less). A tight
		// deadline here would make the test flaky for a reason that is not a defect.
		deadline := time.After(90 * time.Second)
		for {
			select {
			case m := <-replies:
				if id, ok := m["id"].(float64); ok && id == wantID {
					return m
				}
			case err := <-readErr:
				t.Fatalf("contained server closed stdout before answering %s: %v (stderr: %s)",
					what, err, tail(stderr.String()))
			case <-deadline:
				t.Fatalf("no %s reply from the contained server within 90s (stderr: %s)",
					what, tail(stderr.String()))
			}
		}
	}

	send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "nvx-test", "version": "0"},
		},
	})
	initReply := await("initialize", 1)
	result, _ := initReply["result"].(map[string]any)
	if result == nil {
		t.Fatalf("initialize returned no result: %v", initReply)
	}
	if got, _ := result["protocolVersion"].(string); got == "" {
		t.Errorf("initialize result carries no protocolVersion: %v", result)
	}

	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})

	toolsReply := await("tools/list", 2)
	toolsResult, _ := toolsReply["result"].(map[string]any)
	tools, _ := toolsResult["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("tools/list returned no tools through the sandbox: %v", toolsReply)
	}

	// The handshake completing proves stdio crosses the boundary in both
	// directions. Confirm the process really was contained, so a regression that
	// silently stopped containing would not pass as success.
	if !strings.Contains(stderr.String(), "AppContainer isolation active") {
		t.Errorf("the server answered but was not reported as contained; stderr: %s", tail(stderr.String()))
	}
}

func tail(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\r\n", " ")
	if len(s) > 400 {
		return "..." + s[len(s)-400:]
	}
	return s
}

// A minimal MCP server: enough of the protocol to prove the transport works.
const minimalMcpServerJS = `
'use strict';
let buf = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (d) => {
  buf += d;
  for (;;) {
    const i = buf.indexOf('\n');
    if (i === -1) return;
    const line = buf.slice(0, i).trim();
    buf = buf.slice(i + 1);
    if (!line) continue;
    let msg;
    try { msg = JSON.parse(line); } catch (e) { continue; }
    if (msg.method === 'initialize') {
      reply({
        jsonrpc: '2.0', id: msg.id,
        result: {
          protocolVersion: '2024-11-05',
          capabilities: { tools: {} },
          serverInfo: { name: 'nvx-minimal-mcp', version: '0.0.1' },
        },
      });
    } else if (msg.method === 'tools/list') {
      reply({
        jsonrpc: '2.0', id: msg.id,
        result: { tools: [{ name: 'ping', description: 'test tool', inputSchema: { type: 'object' } }] },
      });
    }
  }
});
function reply(o) { process.stdout.write(JSON.stringify(o) + '\n'); }
`
