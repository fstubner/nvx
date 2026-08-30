package main

import (
	"os"
	"path/filepath"
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
