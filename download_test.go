package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractZipRejectsZipSlip(t *testing.T) {
	// After the first path component is stripped, this still escapes destDir.
	zp := writeZip(t, map[string]string{"pkg/../../evil.txt": "pwned"})
	base := t.TempDir()
	err := ExtractZip(zp, filepath.Join(base, "out"))
	if err == nil || !strings.Contains(err.Error(), "illegal") {
		t.Fatalf("expected zip-slip rejection, got %v", err)
	}
	if _, e := os.Stat(filepath.Join(base, "evil.txt")); e == nil {
		t.Fatal("zip-slip file escaped the destination")
	}
}

func TestExtractZipBenign(t *testing.T) {
	zp := writeZip(t, map[string]string{"root/sub/file.txt": "hi"})
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractZip(zp, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "sub", "file.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("benign extract failed: %q %v", got, err)
	}
}

type tarEntry struct {
	name, link, content string
	typ                 byte
}

func writeTarGz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644}
		if e.typ == tar.TypeSymlink {
			h.Linkname = e.link
		}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.content))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	return p
}

func TestExtractTarGzGuards(t *testing.T) {
	dest := func() string { return filepath.Join(t.TempDir(), "out") }

	if err := ExtractTarGz(writeTarGz(t, []tarEntry{{name: "pkg/../../evil", typ: tar.TypeReg, content: "x"}}), dest()); err == nil || !strings.Contains(err.Error(), "illegal") {
		t.Errorf("tar-slip not rejected: %v", err)
	}
	if err := ExtractTarGz(writeTarGz(t, []tarEntry{{name: "pkg/link", typ: tar.TypeSymlink, link: "/etc/passwd"}}), dest()); err == nil || !strings.Contains(err.Error(), "absolute symlink") {
		t.Errorf("absolute symlink not rejected: %v", err)
	}
	if err := ExtractTarGz(writeTarGz(t, []tarEntry{{name: "pkg/link", typ: tar.TypeSymlink, link: "../../etc"}}), dest()); err == nil || !strings.Contains(err.Error(), "outside destination") {
		t.Errorf("escaping symlink not rejected: %v", err)
	}
	if err := ExtractTarGz(writeTarGz(t, []tarEntry{{name: "pkg/sub/f", typ: tar.TypeReg, content: "ok"}}), dest()); err != nil {
		t.Errorf("benign tar failed: %v", err)
	}
}

func TestComputeSHA256(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ComputeSHA256(p)
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err != nil || !strings.EqualFold(got, want) {
		t.Fatalf("ComputeSHA256 = %q, want %q (%v)", got, want, err)
	}
}
