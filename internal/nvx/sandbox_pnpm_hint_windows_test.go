//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pnpm's two failures in the Windows sandbox name no sandbox, so a failed
// contained pnpm run gets a note after its own output. See notePnpmLimits.

func TestAPnpmRunIsRecognisedHoweverItIsStarted(t *testing.T) {
	for _, c := range []struct {
		command string
		args    []string
		exit    int
		want    bool
	}{
		{"pnpm", []string{"install"}, 1, true},
		{"pnpm", []string{"install"}, 127, true},
		{"corepack", []string{"pnpm", "install"}, 1, true},
		{"node", []string{`C:\g\node_modules\pnpm\bin\pnpm.cjs`, "install"}, 1, true},
		{"pnpm", []string{"install"}, 0, false},
		{"pnpm", []string{"install"}, 130, false}, // interrupted
		{"npm", []string{"install"}, 1, false},
		{"yarn", []string{"install"}, 1, false},
		{"node", []string{"build.js"}, 1, false},
	} {
		if got := pnpmFailedContained(c.command, c.args, c.exit); got != c.want {
			t.Errorf("pnpmFailedContained(%s %v, exit %d) = %v, want %v", c.command, c.args, c.exit, got, c.want)
		}
	}
}

// Through the built binary and a real AppContainer, with a stand-in for pnpm 12
// that stops the way it does. The run had no explanation, and now has one.
func TestProbeAFailedContainedPnpmRunIsExplained(t *testing.T) {
	f := newBatchProbeFixture(t)
	script := filepath.Join(f.home, "versions", "node", "v22.0.0", "npm_global", "node_modules", "pnpm", "bin", "pnpm.cjs")
	stopsLikePnpm12 := "console.error('Error: x canonicalizing the `--dir` argument: C:\\p\n  Access is denied. (os error 5)'); process.exit(1)"
	if err := os.WriteFile(script, []byte(stopsLikePnpm12), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := f.run(t, "--strict", "shim", "pnpm", "--version")
	if err == nil {
		t.Fatalf("the stand-in exited 0:\n%s", out)
	}
	for _, want := range []string{"os error 5", "pnpm stopped inside the Windows sandbox", "Failed to get source volume info", "nvx --no-sandbox pnpm"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}

	// A pnpm that works gets no note.
	if err := os.WriteFile(script, []byte("console.log('PROBE pnpm fine')"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = f.run(t, "--strict", "shim", "pnpm", "--version")
	if err != nil || strings.Contains(out, "pnpm stopped inside") {
		t.Errorf("a pnpm run that worked was explained (%v):\n%s", err, out)
	}
}
