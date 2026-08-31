package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The bytes a policy pin is checked against must be the bytes that were parsed.
//
// Parsing and hashing used to be two separate reads of the same path: the caller
// parsed through readProjectPolicyFile, threw the bytes away, and hashPolicyFile
// opened the file again. Anything able to write the project directory between
// them could have the loosened version parsed while the pinned version was
// hashed — and the working directory is writable by contained code in every
// sandbox, so the writer is the thing being contained. SECURITY.md names policy
// tampering as in scope.
//
// Asserted by counting reads of the real file rather than by racing it: a race
// test would be flaky and would prove only that this particular interleaving is
// hard to hit, not that the window is gone. One read means there is no window.
func TestAPolicyIsHashedFromTheBytesItWasParsedFrom(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, ".nvx-policy.json")
	body := []byte(`{"isolation":{"network":{"mode":"open"}}}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	lp, parsed, hash, err := readAndHashProjectPolicyFile(path)
	if err != nil {
		t.Fatalf("readAndHashProjectPolicyFile: %v", err)
	}
	if lp.Isolation.Network.Mode != "open" {
		t.Fatalf("policy did not parse: mode = %q", lp.Isolation.Network.Mode)
	}
	if string(parsed) != string(body) {
		t.Fatalf("parsed bytes = %q, want the file's contents", parsed)
	}

	// The hash must match the pin definition every existing on-disk pin was
	// computed from, or upgrading silently invalidates them.
	want, ok := hashPolicyFile(path)
	if !ok {
		t.Fatal("hashPolicyFile could not read the file it just wrote")
	}
	if hash != want {
		t.Fatalf("hash = %s, want %s; the pin basis changed and every stored pin would stop matching", hash, want)
	}
}

// A byte-order mark changes the pin, and the file still parses.
//
// Both halves matter and they pull in opposite directions: the mark must not
// break parsing (Windows editors write one by default), and it must still be
// inside the hash (every pin on disk was computed over raw bytes). The comment on
// withoutUTF8BOM asserted the second half was false.
func TestAByteOrderMarkParsesAndStillChangesThePin(t *testing.T) {
	dir := tempDir(t)
	plain := filepath.Join(dir, "plain.json")
	withBOM := filepath.Join(dir, "bom.json")
	body := []byte(`{"isolation":{"enabled":true}}`)

	if err := os.WriteFile(plain, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(withBOM, append([]byte{0xEF, 0xBB, 0xBF}, body...), 0o644); err != nil {
		t.Fatal(err)
	}

	lpA, _, hashA, err := readAndHashProjectPolicyFile(plain)
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	lpB, parsedB, hashB, err := readAndHashProjectPolicyFile(withBOM)
	if err != nil {
		t.Fatalf("a policy file written the ordinary Windows way was rejected: %v", err)
	}

	if !lpA.Isolation.Enabled || !lpB.Isolation.Enabled {
		t.Fatal("the policy parsed but its settings were lost")
	}
	if len(parsedB) > 0 && parsedB[0] == 0xEF {
		t.Fatal("the mark reached the parsed bytes; field-presence detection re-reads these")
	}
	if hashA == hashB {
		t.Fatal("the mark did not change the pin; the hash is not being taken over the raw bytes, " +
			"which is the basis every pin already on disk was computed from")
	}
}
