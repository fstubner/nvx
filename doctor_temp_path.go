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
	return underDir(path, os.TempDir())
}

// underDir is the comparison, with the directory passed in.
//
// Separated from os.TempDir() so it can be tested against a directory spelled
// both ways. os.TempDir() ends with a separator on macOS and does not on Windows,
// which is not a detail a test should have to know: the first version of this
// test built its "sibling that merely starts with the same name" case as
// os.TempDir()+"2", which is a sibling on Windows and a CHILD on macOS, and duly
// passed here and failed macOS CI.
func underDir(path, dir string) bool {
	if dir == "" || path == "" {
		return false
	}
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	if strings.EqualFold(path, dir) {
		return true
	}
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix))
}
