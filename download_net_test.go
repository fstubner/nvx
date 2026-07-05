package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "payload-bytes")
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := DownloadFile(srv.URL+"/ok", dest); err != nil {
		t.Fatalf("DownloadFile ok: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "payload-bytes" {
		t.Errorf("downloaded %q, want payload-bytes", got)
	}

	if err := DownloadFile(srv.URL+"/missing", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("expected error on HTTP 404")
	}
}

func TestVerifyNodeChecksum(t *testing.T) {
	// Archive whose SHA-256 we control.
	archive := filepath.Join(t.TempDir(), "node-v20.0.0-linux-x64.tar.gz")
	if err := os.WriteFile(archive, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	const helloSHA = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	name := "node-v20.0.0-linux-x64.tar.gz"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v20.0.0/SHASUMS256.txt":
			fmt.Fprintf(w, "%s  %s\n%s  other-file\n", helloSHA, name, "deadbeef")
		case "/vbad/SHASUMS256.txt":
			fmt.Fprintf(w, "%s  %s\n", "0000000000000000000000000000000000000000000000000000000000000000", name)
		case "/vmissing/SHASUMS256.txt":
			fmt.Fprint(w, "deadbeef  some-other-archive\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	old := nodeDistBaseURL
	nodeDistBaseURL = srv.URL
	defer func() { nodeDistBaseURL = old }()

	if err := VerifyNodeChecksum("v20.0.0", archive, name); err != nil {
		t.Errorf("matching checksum should verify: %v", err)
	}
	if err := VerifyNodeChecksum("vbad", archive, name); err == nil {
		t.Error("mismatched checksum should fail")
	}
	if err := VerifyNodeChecksum("vmissing", archive, name); err == nil {
		t.Error("missing checksum entry should fail")
	}
}
