package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScanVersionDirs(t *testing.T) {
	tempDir := tempDir(t)

	// Create dummy version directories
	os.MkdirAll(filepath.Join(tempDir, "v20.11.0"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "18.16.0"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "node-v16.20.0"), 0755)
	os.WriteFile(filepath.Join(tempDir, "regular_file.txt"), []byte("test"), 0644)

	discovered := make(map[string]string)
	scanVersionDirs(tempDir, "test_source", discovered)

	expectedVersions := []string{"20.11.0", "18.16.0", "16.20.0"}
	for _, expected := range expectedVersions {
		src, exists := discovered[expected]
		if !exists {
			t.Errorf("Expected version %s to be discovered, but was not found", expected)
		}
		if src != "test_source" {
			t.Errorf("Expected source for %s to be 'test_source', got '%s'", expected, src)
		}
	}

	if _, exists := discovered["regular_file.txt"]; exists {
		t.Errorf("Did not expect non-directory file to be discovered")
	}
}

// A source nvx does not know is an error, not an empty result.
//
// `nvx import bogus` printed "No previous Node.js installations found for source
// 'bogus'." and exited 0. A typo therefore read as success, and worse, as
// evidence that the named manager had nothing installed -- a fact nvx had never
// checked, because no scanner matched the name. Every other unknown-argument
// path in the CLI exits 1.
//
// Only the unknown-source case is driven here. A known source cannot be, because
// on a machine that has nvm or fnm this function downloads and installs every
// version it finds, which is not something a unit test may do.
func TestAnUnknownImportSourceIsAnError(t *testing.T) {
	nvxHome := filepath.Join(tempDir(t), ".nvx")

	if code := runImport("bogus", nvxHome); code == 0 {
		t.Fatal("an unknown import source reported success; a typo reads as 'that manager had nothing'")
	}
	// Case and padding must not turn a known source into an unknown one, or the
	// check above would be a new way to fail on valid input.
	for _, ok := range []string{"nvm", "NVM", " volta ", "all", ""} {
		norm := normalizeImportSource(ok)
		if norm != "all" && !containsFold(importSources, norm) {
			t.Errorf("%q is a source nvx supports but would be rejected", ok)
		}
	}
}

// fnm keeps each version under <base>/node-versions/vX.Y.Z/installation. The
// import scanned the base directory itself and found only fnm's own
// subdirectories, so on a standard fnm install it reported nothing to import.
func TestImportFindsFnmsNodeVersions(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FNM_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	base := filepath.Join(home, ".local", "share", "fnm")
	if runtime.GOOS == "windows" {
		base = filepath.Join(home, "AppData", "Roaming", "fnm")
	}
	for _, d := range []string{
		filepath.Join(base, "node-versions", "v20.11.0", "installation"),
		filepath.Join(base, "node-versions", "v22.3.0", "installation"),
		filepath.Join(base, "aliases", "default"),
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	discovered := map[string]string{}
	importFnm(discovered)
	for _, v := range []string{"20.11.0", "22.3.0"} {
		if discovered[v] != "fnm" {
			t.Errorf("fnm's %s was not found; discovered %v", v, discovered)
		}
	}
	if len(discovered) != 2 {
		t.Errorf("found %d versions, want exactly the 2 in node-versions: %v", len(discovered), discovered)
	}

	// FNM_DIR overrides the default base, as it does for fnm.
	custom := filepath.Join(home, "custom-fnm")
	if err := os.MkdirAll(filepath.Join(custom, "node-versions", "v18.20.4", "installation"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FNM_DIR", custom)
	discovered = map[string]string{}
	importFnm(discovered)
	if discovered["18.20.4"] != "fnm" {
		t.Errorf("a version under FNM_DIR was not found: %v", discovered)
	}
}
