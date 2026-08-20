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
