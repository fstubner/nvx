//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// Inside the sandbox corepack's pnpm.cmd and yarn.cmd become node.exe on the
// script, with the flags every contained node gets.
func TestTheSandboxStartsCorepacksLaunchersAsNode(t *testing.T) {
	dir := tempDir(t)
	writeStubBinary(t, filepath.Join(dir, "node.exe"))
	for _, name := range []string{"yarn", "pnpm"} {
		writeStubBinary(t, filepath.Join(dir, "node_modules", "corepack", "dist", name+".js"))
		launcher := `"%~dp0\node_modules\corepack\dist\` + name + `.js" %*` + "\r\n"
		if err := os.WriteFile(filepath.Join(dir, name+".CMD"), []byte(launcher), 0o600); err != nil {
			t.Fatal(err)
		}

		path, args := rewriteWindowsNodeCommand(filepath.Join(dir, name+".CMD"), []string{"add", "react@^18.2.0"}, "")
		script := filepath.Join(dir, "node_modules", "corepack", "dist", name+".js")
		want := append(append([]string{}, nodeSandboxPreserveFlags...), script, "add", "react@^18.2.0")
		if path != filepath.Join(dir, "node.exe") || len(args) != len(want) {
			t.Fatalf("%s.CMD became %q %q, want node.exe %q", name, path, args, want)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Errorf("%s.CMD argument %d is %q, want %q", name, i, args[i], want[i])
			}
		}
	}
}
