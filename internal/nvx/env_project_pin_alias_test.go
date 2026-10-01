package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installedLTSLayout writes stub versions with the markers an install records,
// and returns the path of the binary of the named one.
func installedLTSLayout(t *testing.T, nvxHome, running string) string {
	t.Helper()
	for v, codename := range map[string]string{
		"v24.1.0":  "",
		"v22.11.0": "Jod",
		"v20.18.0": "Iron",
	} {
		dir := filepath.Join(nvxHome, "versions", "node", v)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if codename != "" {
			if err := os.WriteFile(filepath.Join(dir, ltsMarkerName), []byte(codename), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	bin := filepath.Join(nvxHome, "versions", "node", running, "node.exe")
	if err := os.WriteFile(bin, []byte("stub"), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	return bin
}

// A .nvmrc saying `lts/*` or `lts/<codename>` is satisfied by the matching
// installed version, and then the shim says nothing.
//
// versionSatisfies could not read LTS aliases, so every node, npm and npm run
// in such a project warned that shell integration was not active while the
// right version was running. Measured 2026-10-01.
func TestAnLTSAliasInNvmrcIsSatisfiedByTheMatchingVersion(t *testing.T) {
	nvxHome := tempDir(t)
	proj := tempDir(t)
	inProjectDir(t, proj)
	running := installedLTSLayout(t, nvxHome, "v22.11.0")
	node := runtimeForShim("node")

	for _, tc := range []struct {
		nvmrc    string
		wantWarn bool
	}{
		{"lts/*", false},
		{"lts", false},
		{"lts/jod", false},
		{"lts/Jod", false},
		// Installed, but not the line running.
		{"lts/iron", true},
		// Nothing installed for it.
		{"lts/hydrogen", true},
	} {
		if err := os.WriteFile(filepath.Join(proj, ".nvmrc"), []byte(tc.nvmrc+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got := captureStderrHere(t, func() {
			warnIfProjectPinsAnotherVersion(nvxHome, node, projectPinFor(node), "v22.11.0", running)
		})
		if warned := strings.TrimSpace(got) != ""; warned != tc.wantWarn {
			t.Errorf(".nvmrc %q running v22.11.0: warned=%v, want %v\n%s", tc.nvmrc, warned, tc.wantWarn, got)
		}
	}

	// The newest LTS is what `lts/*` means, so an older LTS running is not it.
	running = filepath.Join(nvxHome, "versions", "node", "v20.18.0", "node.exe")
	if err := os.WriteFile(running, []byte("stub"), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".nvmrc"), []byte("lts/*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := captureStderrHere(t, func() {
		warnIfProjectPinsAnotherVersion(nvxHome, node, projectPinFor(node), "v20.18.0", running)
	}); strings.TrimSpace(got) == "" {
		t.Error("lts/* is v22.11.0 here and v20.18.0 ran, with no warning")
	}
}
