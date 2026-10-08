package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// bunx and a project's own programs run on a Node they start through PATH, and
// printed the warning even though nvx has installed that Node and knows its
// version. Measured 2026-10-08 with Node 22.23.2 and Bun 1.4.2, `bunx cowsay hi`
// and a program in node_modules/.bin run with `nvx --strict shim` each ended with
// "[UNDICI-EHPA] Warning". The flag went only to a command inside a Node nvx
// installed.

// installedNodeOnPath puts a Node of this version in a scratch nvx home and
// puts its directory on PATH, as an active shell has it.
func installedNodeOnPath(t *testing.T, version string) (home, binDir string) {
	t.Helper()
	home = tempDir(t)
	binDir = filepath.Join(home, "versions", "node", version)
	name := "node.exe"
	if runtime.GOOS != "windows" {
		binDir = filepath.Join(binDir, "bin")
		name = "node"
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, name), []byte("fixture"), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home, binDir
}

func TestTheWarningFlagReachesProgramsThatRunOnNvxsNode(t *testing.T) {
	tsc := filepath.Join(tempDir(t), "node_modules", ".bin", "tsc")
	cases := []struct {
		name    string
		version string
		cmd     func(home string) string
		want    bool
	}{
		{"a project's own program on Node 22.23.3", "v22.23.3", func(string) string { return tsc }, true},
		{"bunx with Node 22.23.3", "v22.23.3", func(home string) string {
			return filepath.Join(home, "versions", "bun", "v1.4.2", "bunx.exe")
		}, true},
		{"bunx with Node 24", "v24.14.1", func(home string) string {
			return filepath.Join(home, "versions", "bun", "v1.4.2", "bunx.exe")
		}, true},
		{"a program on Node 22.20.9, which does not read the variable", "v22.20.9", func(string) string { return tsc }, false},
		{"a program on Node 18, which refuses the flag", "v18.20.4", func(string) string { return tsc }, false},
		{"bunx with Node 20", "v20.11.0", func(home string) string {
			return filepath.Join(home, "versions", "bun", "v1.4.2", "bunx.exe")
		}, false},
		// No Node of nvx's behind the command. Whatever `node` it finds is
		// unknown, and a Node that does not know the flag refuses to start.
		{"a program with no Node of nvx's behind it", "", func(string) string { return tsc }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := tempDir(t)
			if tc.version != "" {
				home, _ = installedNodeOnPath(t, tc.version)
			}
			env := withEnvProxyWarningSilenced([]string{"PATH=/bin"}, testProxyForEnv(), tc.cmd(home), home)
			got := nodeOptions(env)
			if tc.want {
				if len(got) != 1 || got[0] != envProxyWarningFlag {
					t.Fatalf("NODE_OPTIONS = %q, want exactly [%s]", got, envProxyWarningFlag)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("NODE_OPTIONS = %q, want none", got)
			}
		})
	}
}
