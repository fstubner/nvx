package nvx

import (
	"os"
	"strings"
	"testing"
)

// A refusal exits with its own code, not the command's 1.
//
// Every refusal returned 1, which is also what npm returns when an install
// fails on its own terms. A script or an agent loop that ran `npm install -g`
// through the shim saw "exit 1" either way and could not tell nvx saying no
// (fix: --no-sandbox, or npx) from a registry timeout (fix: retry) without
// parsing English off stderr. The audit log already distinguishes the two
// (mode=refused, with a reason); this is that distinction at the one layer a
// program reads.
//
// Driven through the real dispatcher with a provider nvx cannot honour, which
// is the cheapest refusal to arrange and goes through sandboxDidNotStart like
// every other one.
func TestARefusalExitsWithItsOwnCode(t *testing.T) {
	var code int
	captureStderrHere(t, func() {
		code = runSandbox(SandboxConfig{
			NvxHome:            t.TempDir(),
			Command:            "does-not-matter",
			FilesystemProvider: "definitely-not-a-provider",
		})
	})
	if code == 1 {
		t.Fatal("a refusal exited 1, the same code a command that ran and failed returns; " +
			"a caller cannot tell nvx declining from npm failing")
	}
	if code != exitRefused {
		t.Fatalf("a refusal exited %d, want %d", code, exitRefused)
	}
}

// The refusal code is not one a wrapped command plausibly returns itself.
//
// 1 and 2 are the everyday failure codes of every tool nvx wraps, and 127 and
// 129 already mean "command not found" and "parent hung up" here. If the
// refusal code collided with any of them the whole point would be lost.
func TestTheRefusalCodeIsDistinctFromTheCodesCommandsUse(t *testing.T) {
	for _, taken := range []int{0, 1, 2, 127, exitParentHungUp} {
		if exitRefused == taken {
			t.Fatalf("exitRefused is %d, which already means something else", taken)
		}
	}
}

// The global-install refusal -- the one agents actually hit -- carries the code
// through runShim, which is the path a shim invocation really takes.
func TestAGlobalInstallRefusalExitsWithTheRefusalCode(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	// The guidance lines are LogInfo, which -q suppresses, and quietFlag is
	// package state another test may have left set. Measured in the full suite:
	// exit 77 as expected, but 77 bytes of stderr -- the LogError line alone.
	prevQuiet := quietFlag
	quietFlag = false
	t.Cleanup(func() { quietFlag = prevQuiet })

	var code int
	out := captureStderrHere(t, func() {
		code = runShim("npm", []string{"install", "-g", "left-pad"}, home)
	})
	if !strings.Contains(out, "nvx refused") {
		t.Fatalf("the refusal does not say it is nvx refusing:\n%s", out)
	}
	if !strings.Contains(out, "npx") {
		t.Fatalf("the refusal does not name the contained alternative an agent should prefer:\n%s", out)
	}
	if code != exitRefused {
		t.Fatalf("`npm install -g` refused with exit %d, want %d", code, exitRefused)
	}
}
