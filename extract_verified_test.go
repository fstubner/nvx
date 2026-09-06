package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// What is extracted is what was verified.
//
// Both installers downloaded to a fixed path under ~/.nvx/downloads, hashed
// the file at that path, and then extracted from that path: two opens, and
// whatever the file held at the second one is what was installed. A same-user
// process that changes the file between the two -- another nvx installing the
// same version into the same fixed name is the ordinary way that happens --
// gets its bytes installed under the first one's verified checksum.
//
// The hook stands in for that window. The archive is rewritten in place after
// verification; the extracted content must still be the verified content.
func TestWhatIsExtractedIsWhatWasVerified(t *testing.T) {
	good := zipWithMarker(t, "good\n")
	evil := zipWithMarker(t, "evil\n")
	sum := sha256.Sum256(good)
	expected := hex.EncodeToString(sum[:])

	dir := tempDir(t)
	archive := filepath.Join(dir, "bun.zip")
	if err := os.WriteFile(archive, good, 0o600); err != nil {
		t.Fatal(err)
	}

	orig := archiveVerifyHook
	archiveVerifyHook = func() {
		// In place, as a concurrent writer to the same path would.
		if err := os.WriteFile(archive, evil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { archiveVerifyHook = orig })

	dest := filepath.Join(dir, "out")
	if err := extractVerifiedArchive(archive, expected, dest, true); err != nil {
		t.Fatalf("extraction of a verified archive failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "marker.txt"))
	if err != nil {
		t.Fatalf("the extracted marker is missing: %v", err)
	}
	if string(got) != "good\n" {
		t.Fatalf("extracted %q: the bytes installed are not the bytes that were verified", got)
	}
}

// And a checksum mismatch is still refused before anything is extracted.
func TestAnArchiveWithTheWrongChecksumIsNotExtracted(t *testing.T) {
	data := zipWithMarker(t, "anything\n")
	dir := tempDir(t)
	archive := filepath.Join(dir, "x.zip")
	if err := os.WriteFile(archive, data, 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	if err := extractVerifiedArchive(archive, hex.EncodeToString(make([]byte, 32)), dest, true); err == nil {
		t.Fatal("an archive whose checksum did not match was extracted")
	}
	if _, err := os.Stat(filepath.Join(dest, "marker.txt")); err == nil {
		t.Fatal("the mismatched archive's contents were written")
	}
}

// zipWithMarker builds a zip in the runtime-archive shape -- one top-level
// folder, which the extractor strips -- holding marker.txt with the content.
func zipWithMarker(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("top/marker.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
