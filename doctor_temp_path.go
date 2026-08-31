package main

import (
	"os"
	"path/filepath"
	"strings"
)

// underTempDir reports whether path lives inside the OS temporary directory.
//
// Used to refuse writing a temporary location into a persistent setting. It
// compares cleaned, case-folded paths and requires a separator after the prefix,
// so a sibling named like the temp directory with a suffix -- `...\Temp2` against
// `...\Temp` -- is not mistaken for something inside it.
//
// Case-insensitive on every platform rather than only on Windows: the one caller
// is Windows-only, and a comparison that changes shape by platform is a thing to
// get wrong later for no benefit here.
func underTempDir(path string) bool {
	tmp := os.TempDir()
	if tmp == "" || path == "" {
		return false
	}
	tmp = filepath.Clean(tmp)
	path = filepath.Clean(path)
	if strings.EqualFold(path, tmp) {
		return true
	}
	prefix := tmp
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix))
}
