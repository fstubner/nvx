package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The download-verification path is the product's core supply-chain promise and
// had 0% coverage: ComputeSHA256, VerifyNodeChecksum, VerifyChecksumFromShasums,
// verifyExpectedSHA256, ExtractZip, ExtractZipFlat, extractZip, ExtractTarGz and
// copyArchiveFile were all at 0.0%, while parsing archives and manifests fetched
// over the network. The traversal helpers were tested, but nothing exercised them
// through an actual extraction.
//
// These tests are built around fixtures created in-process, so they need no
// network and no committed binary blobs.

func writeTempFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// --- hashing -----------------------------------------------------------------

func TestComputeSHA256MatchesKnownDigest(t *testing.T) {
	dir := tempDir(t)
	content := []byte("nvx checksum fixture\n")
	path := writeTempFile(t, dir, "blob.bin", content)

	want := sha256.Sum256(content)
	got, err := ComputeSHA256(path)
	if err != nil {
		t.Fatalf("ComputeSHA256: %v", err)
	}
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("ComputeSHA256 = %q, want %q", got, hex.EncodeToString(want[:]))
	}
}

func TestComputeSHA256ReportsMissingFile(t *testing.T) {
	if _, err := ComputeSHA256(filepath.Join(tempDir(t), "absent.bin")); err == nil {
		t.Error("hashing a missing file must fail rather than return an empty digest")
	}
}

func TestVerifyExpectedSHA256(t *testing.T) {
	dir := tempDir(t)
	content := []byte("payload")
	path := writeTempFile(t, dir, "payload.bin", content)
	sum := sha256.Sum256(content)
	correct := hex.EncodeToString(sum[:])

	t.Run("matching digest passes", func(t *testing.T) {
		if err := verifyExpectedSHA256(path, correct); err != nil {
			t.Errorf("expected the correct digest to verify, got %v", err)
		}
	})

	t.Run("uppercase digest passes", func(t *testing.T) {
		// Published manifests are not consistent about case.
		if err := verifyExpectedSHA256(path, strings.ToUpper(correct)); err != nil {
			t.Errorf("digest comparison must be case-insensitive, got %v", err)
		}
	})

	t.Run("surrounding whitespace tolerated", func(t *testing.T) {
		if err := verifyExpectedSHA256(path, "  "+correct+"\n"); err != nil {
			t.Errorf("expected trimming, got %v", err)
		}
	})

	t.Run("altered payload is rejected", func(t *testing.T) {
		tampered := writeTempFile(t, dir, "tampered.bin", []byte("payloaD"))
		if err := verifyExpectedSHA256(tampered, correct); err == nil {
			t.Error("a file whose contents changed must fail verification")
		}
	})

	t.Run("empty expected digest is rejected", func(t *testing.T) {
		// Fail-closed: an absent expectation must never be treated as "no
		// verification required".
		if err := verifyExpectedSHA256(path, ""); err == nil {
			t.Error("an empty expected digest must be an error, not a pass")
		}
		if err := verifyExpectedSHA256(path, "   "); err == nil {
			t.Error("a whitespace-only expected digest must be an error, not a pass")
		}
	})
}

// --- manifest verification ----------------------------------------------------

func TestVerifyChecksumFromShasums(t *testing.T) {
	dir := tempDir(t)
	content := []byte("node binary stand-in")
	archive := writeTempFile(t, dir, "node-v20.0.0-linux-x64.tar.gz", content)
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	serve := func(body string, status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			_, _ = io.WriteString(w, body)
		}))
	}

	t.Run("correct manifest entry verifies", func(t *testing.T) {
		srv := serve(digest+"  node-v20.0.0-linux-x64.tar.gz\n", http.StatusOK)
		defer srv.Close()
		if err := VerifyChecksumFromShasums(srv.URL, archive, "node-v20.0.0-linux-x64.tar.gz"); err != nil {
			t.Errorf("expected verification to succeed, got %v", err)
		}
	})

	t.Run("wrong digest is rejected", func(t *testing.T) {
		srv := serve(strings.Repeat("00", 32)+"  node-v20.0.0-linux-x64.tar.gz\n", http.StatusOK)
		defer srv.Close()
		err := VerifyChecksumFromShasums(srv.URL, archive, "node-v20.0.0-linux-x64.tar.gz")
		if err == nil {
			t.Fatal("a manifest digest that does not match the file must fail")
		}
		if !strings.Contains(err.Error(), "checksum verification failed") {
			t.Errorf("error should name the mismatch, got %v", err)
		}
	})

	t.Run("filename absent from manifest is rejected", func(t *testing.T) {
		// Fail-closed: an unlisted artifact must not be treated as unverified-but-ok.
		srv := serve(digest+"  some-other-file.tar.gz\n", http.StatusOK)
		defer srv.Close()
		if err := VerifyChecksumFromShasums(srv.URL, archive, "node-v20.0.0-linux-x64.tar.gz"); err == nil {
			t.Error("a manifest without an entry for this file must fail")
		}
	})

	t.Run("unreachable manifest is rejected", func(t *testing.T) {
		srv := serve("", http.StatusNotFound)
		defer srv.Close()
		if err := VerifyChecksumFromShasums(srv.URL, archive, "node-v20.0.0-linux-x64.tar.gz"); err == nil {
			t.Error("a manifest that cannot be fetched must fail, not skip verification")
		}
	})

	t.Run("empty manifest is rejected", func(t *testing.T) {
		srv := serve("", http.StatusOK)
		defer srv.Close()
		if err := VerifyChecksumFromShasums(srv.URL, archive, "node-v20.0.0-linux-x64.tar.gz"); err == nil {
			t.Error("an empty manifest must fail")
		}
	})
}

// --- extraction size cap ------------------------------------------------------

// copyArchiveFile enforces the decompression-bomb budget. It cannot be reached
// end-to-end in a test (the real cap is 2 GiB), but it takes the budget as a
// parameter, so the boundary is exercised directly.
func TestCopyArchiveFileEnforcesBudget(t *testing.T) {
	t.Run("within budget is copied and decrements", func(t *testing.T) {
		remaining := int64(100)
		var out bytes.Buffer
		if err := copyArchiveFile(&out, strings.NewReader("0123456789"), &remaining, "a.txt"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.String() != "0123456789" {
			t.Errorf("copied %q, want the full input", out.String())
		}
		if remaining != 90 {
			t.Errorf("remaining = %d, want 90", remaining)
		}
	})

	t.Run("exactly the budget is allowed", func(t *testing.T) {
		remaining := int64(10)
		var out bytes.Buffer
		if err := copyArchiveFile(&out, strings.NewReader("0123456789"), &remaining, "a.txt"); err != nil {
			t.Errorf("a file exactly filling the budget must be allowed, got %v", err)
		}
		if remaining != 0 {
			t.Errorf("remaining = %d, want 0", remaining)
		}
	})

	t.Run("one byte over the budget is rejected", func(t *testing.T) {
		remaining := int64(10)
		var out bytes.Buffer
		err := copyArchiveFile(&out, strings.NewReader("0123456789X"), &remaining, "bomb.txt")
		if err == nil {
			t.Fatal("exceeding the budget must fail; this is the decompression-bomb guard")
		}
		if !strings.Contains(err.Error(), "limit exceeded") {
			t.Errorf("error should name the limit, got %v", err)
		}
		if remaining != 0 {
			t.Errorf("remaining = %d, want 0 so later members cannot proceed", remaining)
		}
	})

	t.Run("an exhausted budget rejects before reading", func(t *testing.T) {
		remaining := int64(0)
		var out bytes.Buffer
		if err := copyArchiveFile(&out, strings.NewReader("x"), &remaining, "next.txt"); err == nil {
			t.Error("with no budget left the next member must be refused")
		}
		if out.Len() != 0 {
			t.Errorf("nothing should have been written, got %q", out.String())
		}
	})
}

// --- tar.gz extraction --------------------------------------------------------

type tarEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

func buildTarGz(t *testing.T, dir, name string, entries []tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     0o755,
			Size:     int64(len(e.body)),
			Linkname: e.linkname,
		}
		if e.typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTempFile(t, dir, name, buf.Bytes())
}

func TestExtractTarGzExtractsAndStripsWrapperDir(t *testing.T) {
	dir := tempDir(t)
	archive := buildTarGz(t, dir, "node.tar.gz", []tarEntry{
		{name: "node-v20.0.0/", typeflag: tar.TypeDir},
		{name: "node-v20.0.0/bin/", typeflag: tar.TypeDir},
		{name: "node-v20.0.0/bin/node", typeflag: tar.TypeReg, body: "#!/bin/sh\necho node\n"},
		{name: "node-v20.0.0/README.md", typeflag: tar.TypeReg, body: "docs"},
	})

	dest := filepath.Join(dir, "out")
	if err := ExtractTarGz(archive, dest); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}

	// The leading wrapper directory must be stripped.
	got, err := os.ReadFile(filepath.Join(dest, "bin", "node"))
	if err != nil {
		t.Fatalf("expected bin/node with the wrapper stripped: %v", err)
	}
	if !strings.Contains(string(got), "echo node") {
		t.Errorf("bin/node content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "node-v20.0.0")); err == nil {
		t.Error("the wrapper directory should not survive stripping")
	}
}

func TestExtractTarGzRejectsPathTraversal(t *testing.T) {
	dir := tempDir(t)
	// After the wrapper segment is stripped this still climbs out of destDir.
	archive := buildTarGz(t, dir, "evil.tar.gz", []tarEntry{
		{name: "pkg/../../escaped.txt", typeflag: tar.TypeReg, body: "owned"},
	})

	dest := filepath.Join(dir, "out")
	err := ExtractTarGz(archive, dest)
	if err == nil {
		t.Fatal("a member escaping the destination must be rejected")
	}
	if !strings.Contains(err.Error(), "illegal file path") {
		t.Errorf("error should name the illegal path, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "escaped.txt")); statErr == nil {
		t.Error("the traversal target was written outside the destination")
	}
}

// TestExtractTarGzRejectsEscapingSymlink covers the guard at download.go:417. A
// symlink is the subtler traversal vector: the member path is legal, and only the
// link target escapes. This runs everywhere because the guard rejects before any
// symlink is created, so it needs no symlink privilege.
func TestExtractTarGzRejectsEscapingSymlink(t *testing.T) {
	dir := tempDir(t)
	dest := filepath.Join(dir, "out")

	for _, tc := range []struct {
		name     string
		linkname string
	}{
		{"relative escape", "../../../../etc/passwd"},
		{"absolute target", "/etc/passwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := buildTarGz(t, dir, "link-"+tc.name+".tar.gz", []tarEntry{
				{name: "pkg/link", typeflag: tar.TypeSymlink, linkname: tc.linkname},
			})
			err := ExtractTarGz(archive, dest)
			if err == nil {
				t.Fatalf("a symlink pointing outside the destination must be rejected (%s)", tc.linkname)
			}
			if !strings.Contains(err.Error(), "illegal") {
				t.Errorf("error should identify the illegal symlink, got %v", err)
			}
		})
	}
}

func TestExtractTarGzAllowsInternalSymlink(t *testing.T) {
	dir := tempDir(t)
	archive := buildTarGz(t, dir, "ok-link.tar.gz", []tarEntry{
		{name: "pkg/bin/", typeflag: tar.TypeDir},
		{name: "pkg/bin/node", typeflag: tar.TypeReg, body: "real"},
		{name: "pkg/bin/nodejs", typeflag: tar.TypeSymlink, linkname: "node"},
	})

	dest := filepath.Join(dir, "out")
	err := ExtractTarGz(archive, dest)
	if err != nil {
		if runtime.GOOS == "windows" && strings.Contains(err.Error(), "failed to create symlink") {
			t.Skip("creating symlinks on Windows needs privilege or Developer Mode")
		}
		t.Fatalf("a symlink staying inside the destination must be allowed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "bin", "nodejs")); err != nil {
		t.Errorf("expected the internal symlink to be created: %v", err)
	}
}

// --- zip extraction -----------------------------------------------------------

func buildZip(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for path, body := range files {
		w, err := zw.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTempFile(t, dir, name, buf.Bytes())
}

func TestExtractZipStripsWrapperAndFlatDoesNot(t *testing.T) {
	dir := tempDir(t)
	archive := buildZip(t, dir, "tool.zip", map[string]string{
		"tool-v1/bin/tool": "binary",
	})

	stripped := filepath.Join(dir, "stripped")
	if err := ExtractZip(archive, stripped); err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stripped, "bin", "tool")); err != nil {
		t.Errorf("ExtractZip should strip the wrapper directory: %v", err)
	}

	flat := filepath.Join(dir, "flat")
	if err := ExtractZipFlat(archive, flat); err != nil {
		t.Fatalf("ExtractZipFlat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(flat, "tool-v1", "bin", "tool")); err != nil {
		t.Errorf("ExtractZipFlat should preserve the archive layout: %v", err)
	}
}

func TestExtractZipRejectsPathTraversal(t *testing.T) {
	dir := tempDir(t)
	archive := buildZip(t, dir, "evil.zip", map[string]string{
		"pkg/../../escaped.txt": "owned",
	})

	dest := filepath.Join(dir, "out")
	if err := ExtractZip(archive, dest); err == nil {
		t.Fatal("a zip member escaping the destination must be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Error("the traversal target was written outside the destination")
	}
}
