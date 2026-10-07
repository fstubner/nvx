package nvx

import (
	"path/filepath"
	"strings"
	"testing"
)

// An exact version that is already installed is answered without the network.
//
// The release list was fetched before the installed check, so `nvx install
// 22.23.3` failed with "failed to fetch release list" on a machine that had
// 22.23.3, while `use`, `default`, `list` and the shims all worked offline.
// Measured 2026-10-07 with a dead proxy and a mirror nothing listens on.
func TestAnInstalledExactVersionNeedsNoNetwork(t *testing.T) {
	notQuiet(t)
	// Nothing listens here, so any request for the release list fails at once.
	t.Setenv("NVX_NODE_MIRROR", "http://127.0.0.1:1")
	nvxHome := tempDir(t)
	writeStubBinary(t, nodeBinaryPath(filepath.Join(nvxHome, "versions", "node", "v22.1.0")))

	for _, spelling := range []string{"22.1.0", "v22.1.0"} {
		var err error
		out := captureStderrHere(t, func() { err = NodeProvider{}.Install(spelling, nvxHome) })
		if err != nil {
			t.Errorf("install %s with the version on disk and no network: %v", spelling, err)
		}
		if !strings.Contains(out, "v22.1.0 is already installed") {
			t.Errorf("install %s did not say it was already installed:\n%s", spelling, out)
		}
	}

	// A version that is not on disk, and anything that is not a full version, still
	// needs the list to say what it means.
	for _, spelling := range []string{"22", "22.1", "lts", "22.2.0"} {
		if err := (NodeProvider{}).Install(spelling, nvxHome); err == nil {
			t.Errorf("install %s succeeded with no network and nothing on disk to match it", spelling)
		}
	}
}
