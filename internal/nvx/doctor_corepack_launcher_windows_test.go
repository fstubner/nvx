//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// After `corepack enable`, yarn.cmd and pnpm.cmd in the Node's folder are
// corepack's launchers, and a launcher fetches the package manager the first time
// it runs. Doctor starts what the shim would start with no network, so it failed
// yarn there while `nvx --strict yarn --version` printed 1.22.22, exit 0.
//
// Through the built binary and a real AppContainer, with a launcher that stops
// the way corepack does when it needs the network.
func TestProbeDoctorDoesNotFailACorepackLauncher(t *testing.T) {
	f := newBatchProbeFixture(t)
	versionDir := filepath.Join(f.home, "versions", "node", "v22.0.0")
	launcher := `@IF EXIST "%~dp0\node.exe" (` + "\r\n" + `  "%~dp0\node.exe"  "%~dp0\node_modules\corepack\dist\yarn.js" %*` + "\r\n" + `)` + "\r\n"
	files := map[string]string{
		filepath.Join(versionDir, "yarn.cmd"):                                    launcher,
		filepath.Join(versionDir, "node_modules", "corepack", "dist", "yarn.js"): `console.error("Error: connect ECONNREFUSED: corepack needs the network to fetch yarn"); process.exit(1)`,
	}
	for p, content := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out, _ := f.run(t, "doctor")
	if strings.Contains(out, "[FAIL] yarn") {
		t.Errorf("doctor failed corepack's launcher for needing the network:\n%s", out)
	}
	if !strings.Contains(out, "[--]   yarn is corepack's launcher") {
		t.Errorf("doctor does not say why it left corepack's launcher alone:\n%s", out)
	}
}
