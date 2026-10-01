//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A contained run works under an NVX_HOME too long to hold an AF_UNIX socket.
//
// The sockets the sandbox reaches the parent through lived in the guest home,
// under NVX_HOME, and a socket path has to stay under 108 bytes. Measured
// 2026-10-01: a 132-character NVX_HOME needed a 174-byte egress socket path and
// every proxied run refused with exit 77. Windows profiles redirected to
// OneDrive and corporate home folders make paths that long ordinary.
//
// Through the built binary and a real AppContainer, because the fix moves the
// sockets to a folder the AppContainer is granted by Windows itself, and only a
// real container can say whether it reaches them there. --connect rides along
// so the tunnel sockets are exercised as well as the egress one.
func TestAContainedRunWorksUnderALongNvxHome(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (builds nvx and launches a real AppContainer)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the contained program needs it")
	}

	// 132 characters, the length measured failing.
	base := tempDir(t)
	nvxHome := filepath.Join(base, strings.Repeat("h", 132-len(base)-1))
	if err := os.MkdirAll(nvxHome, 0o700); err != nil {
		t.Fatal(err)
	}

	proj := tempDir(t)
	nvxExe := filepath.Join(tempDir(t), "nvx.exe")
	if out, err := exec.Command("go", "build", "-o", nvxExe, "github.com/fstubner/nvx/cmd/nvx").CombinedOutput(); err != nil {
		t.Fatalf("build nvx: %v\n%s", err, out)
	}

	host := startEchoService(t)
	script := `const net = require("net");
const s = net.connect(Number(process.env.` + connectEnvVar(host) + `), "127.0.0.1", () => s.write("ping"));
s.on("data", d => { console.log("PROBE echo=" + d); s.end(); });
s.on("error", e => { console.log("PROBE error=" + e.message); });`
	if err := os.WriteFile(filepath.Join(proj, "probe.js"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	// --strict contains a plain node script, and the default network mode is
	// proxy, which is the mode that needs the egress socket.
	cmd := exec.Command(nvxExe, "--strict", "--connect", strconv.Itoa(host), "shim", "node", "probe.js")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "NVX_HOME="+nvxHome)
	out, err := cmd.CombinedOutput()
	got := string(out)
	requireContainedRunLaunched(t, got)

	if !strings.Contains(got, "PROBE echo=ping") {
		t.Fatalf("the contained run under a %d-character NVX_HOME did not complete (%v):\n%s",
			len(nvxHome), err, got)
	}
	if err != nil {
		t.Errorf("the contained run printed its result but exited with %v:\n%s", err, got)
	}
}
