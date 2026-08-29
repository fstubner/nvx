package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A refusal must reach a waiting MCP client, driven through the code that
// refuses rather than through the reporter.
//
// Every other test in this area calls reportRefusalOverStdio directly, and all
// of them passed while the most common refusal in practice -- a project policy
// file nvx cannot parse -- went down a branch that never called it. The client
// saw a closed pipe and reported "Connection closed", which is the exact symptom
// the reporter was written to remove. Asserting on the reporter cannot detect
// that; only running the refusal can.
//
// A malformed .nvx-policy.json is the fixture because it is the shape people
// actually produce: it is what a byte-order mark looked like before nvx learned
// to strip one, and what any hand edit that drops a brace looks like now.
func TestARefusedRunAnswersTheClientThatIsWaiting(t *testing.T) {
	project := tempDir(t)
	nvxHome := filepath.Join(tempDir(t), ".nvx")
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"p","version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".nvx-policy.json"), []byte(`{"isolation":{}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	// The client's request, already sent -- an MCP client writes `initialize` as
	// soon as it has spawned the server.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinR.Close()
	go func() {
		_, _ = stdinW.WriteString(`{"jsonrpc":"2.0","id":41,"method":"initialize","params":{}}` + "\n")
		_ = stdinW.Close()
	}()
	prevStdin := refusalStdin
	refusalStdin = stdinR
	t.Cleanup(func() { refusalStdin = prevStdin })

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prevStdout := os.Stdout
	os.Stdout = outW
	code := runShim("node", []string{"-e", "0"}, nvxHome)
	os.Stdout = prevStdout
	_ = outW.Close()
	captured, _ := io.ReadAll(outR)
	_ = outR.Close()

	if code == 0 {
		t.Fatal("an unreadable policy did not refuse the run")
	}
	if len(captured) == 0 {
		t.Fatal("the refusal wrote nothing to standard output; an MCP client sees a closed pipe and reports \"Connection closed\"")
	}

	var resp struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Message string `json:"message"`
			Data    struct {
				Remedy string `json:"remedy"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(captured))), &resp); err != nil {
		t.Fatalf("what reached the client was not JSON-RPC: %v\n%s", err, captured)
	}
	// The wrong id is not an answer -- the client is waiting on this one.
	if string(resp.ID) != "41" {
		t.Fatalf("answered id %s, want 41", resp.ID)
	}
	if !strings.Contains(resp.Error.Message, "security policy") {
		t.Fatalf("message %q does not say why nvx refused", resp.Error.Message)
	}
	// The remedy has to match this refusal specifically. Approving prompts cannot
	// fix a policy nvx could not read, and saying so sends people to try
	// something that cannot work.
	if !strings.Contains(resp.Error.Data.Remedy, "nvx doctor") {
		t.Fatalf("remedy %q is not the one for a policy that could not be read", resp.Error.Data.Remedy)
	}
}
