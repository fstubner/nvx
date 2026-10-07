//go:build windows

package nvx

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A batch file gets its arguments as they were given, inside the sandbox and
// outside it.
//
// npm's batch shims hand %* to node, so what node prints is what the batch
// file was given. cmd.exe reads & | < > ^ ( ) as syntax outside quotes and
// expands %VAR% inside them, and the launch line quoted only arguments with a
// space or a quote, and escaped a quote as \", which cmd.exe does not know.
// The uncontained launch used exec.Command, which escapes the same way.

// batchArgCases are arguments cmd.exe reads as syntax, or changes, when they
// reach it unescaped. The ones naming marker run a second command that writes
// it.
func batchArgCases(marker string) []string {
	return []string{
		"x&echo.INJECTED>" + marker,
		"x|echo.INJECTED>" + marker,
		`"&echo.INJECTED>` + marker,
		"a<b", "a>b", "a^b", "(a)", "a,b;c=d",
		"%PATH%", "!PATH!", "100%",
		`a"b`, `a" b`, `a\"b`, `C:\dir\`, `"`,
		"", "a b", "a\tb", "é日本",
	}
}

// hostNodeDir is the folder of the node.exe on PATH, asked of node itself
// because `node` on PATH can be nvx's own shim.
func hostNodeDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("node", "-p", "process.execPath").Output()
	if err != nil {
		t.Skip("node is not installed, and the batch file hands its arguments to node")
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// TestBatchFileArgumentsRoundTrip runs an npm batch shim with each of
// batchArgCases, by the command line a contained launch uses and by the
// uncontained launch, and has node print the arguments it was given.
func TestBatchFileArgumentsRoundTrip(t *testing.T) {
	nodeDir := hostNodeDir(t)
	project := tempDir(t)
	nvxHome := tempDir(t)
	batch := filepath.Join(project, "node_modules", ".bin", "argv.cmd")
	for p, content := range map[string]string{
		filepath.Join(project, "package.json"): `{"name":"batch-args","version":"1.0.0"}`,
		batch:                                  npmCmdShim(`..\argv\argv.js`),
		filepath.Join(project, "node_modules", "argv", "argv.js"): `process.stdout.write(JSON.stringify(process.argv.slice(2)))`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "PATH="+nodeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prevDir)

	launches := []struct {
		name  string
		build func(args []string) (*exec.Cmd, error)
	}{
		{"contained launch line", func(args []string) (*exec.Cmd, error) {
			exe, line, err := windowsBatchLaunch(batch, args)
			if err != nil {
				return nil, err
			}
			cmd := exec.Command(exe)
			cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
			return cmd, nil
		}},
		{"uncontained launch", func(args []string) (*exec.Cmd, error) {
			return directCommand("argv", args, nvxHome, false)
		}},
	}
	marker := filepath.Join(project, "injected")
	want := append(batchArgCases(marker), "last")
	for _, launch := range launches {
		cmd, err := launch.build(want)
		if err != nil {
			t.Fatalf("%s: %v", launch.name, err)
		}
		cmd.Env, cmd.Dir = env, project
		out, _ := cmd.Output()
		requireArgsRoundTrip(t, launch.name, want, out)
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("%s: an argument ran a command of its own, which wrote %s", launch.name, marker)
			os.Remove(marker)
		}
	}
	if _, _, err := windowsBatchLaunch(batch, []string{"a\nb"}); err == nil {
		t.Error("an argument with a line break was accepted, and cmd.exe drops everything after it")
	}
}

// requireArgsRoundTrip fails the test unless out is the JSON array want,
// naming each argument that came back changed.
func requireArgsRoundTrip(t *testing.T, launch string, want []string, out []byte) {
	t.Helper()
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Errorf("%s: node printed no argument list: %q", launch, out)
		return
	}
	for i, w := range want {
		switch {
		case i >= len(got):
			t.Errorf("%s: %q did not arrive", launch, w)
		case got[i] != w:
			t.Errorf("%s: %q arrived as %q", launch, w, got[i])
		}
	}
	if len(got) > len(want) {
		t.Errorf("%s: %d arguments became %d: %q", launch, len(want), len(got), got)
	}
}
