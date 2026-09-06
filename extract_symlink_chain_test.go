package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A tar entry cannot be written outside the destination by way of earlier
// symlinks, however many hops it takes.
//
// The symlink check resolved each link's target lexically against the
// directory of the entry's own path. That is correct for a single link and
// wrong as soon as the path to an entry passes through links created by
// earlier entries: `link` -> `sub`, `sub/up` -> `..`, then a symlink at
// `link/up/out` -> `..` resolves lexically to <dest>/link, inside, and in the
// filesystem to <dest>/.., outside. A file entry at `link/up/out/pwned` then
// lands in the destination's parent. Every individual target here passes the
// old check; the escape is the composition.
//
// A runtime archive is fetched from a release index and checksum-verified, so
// this needs a compromised release to matter -- which is exactly the situation
// the extraction is supposed to survive.
func TestATarSymlinkChainCannotEscapeTheDestination(t *testing.T) {
	root := tempDir(t)
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	// Symlink support is what this test is about; a host that cannot create
	// one (Windows without Developer Mode or elevation) cannot run it.
	probe := filepath.Join(root, "probe-link")
	if err := os.Symlink(root, probe); err != nil {
		t.Skipf("creating symlinks on Windows needs privilege or Developer Mode: %v", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, typeflag byte, link string, body []byte) {
		h := &tar.Header{Name: name, Typeflag: typeflag, Linkname: link, Mode: 0o755, Size: int64(len(body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if len(body) > 0 {
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("pkg/", tar.TypeDir, "", nil)
	add("pkg/sub/", tar.TypeDir, "", nil)
	add("pkg/sub/up", tar.TypeSymlink, "..", nil)       // -> pkg          (inside)
	add("pkg/link", tar.TypeSymlink, "sub", nil)        // -> pkg/sub      (inside)
	add("pkg/link/up/out", tar.TypeSymlink, "..", nil)  // lexically pkg/link; really pkg/.. = dest/..
	add("pkg/link/up/out/pwned", tar.TypeReg, "", []byte("escaped\n"))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "evil.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ExtractTarGz(archive, dest)

	// Whatever the extractor reported, nothing may exist outside dest.
	for _, outside := range []string{
		filepath.Join(root, "pwned"),        // dest/.. is root
		filepath.Join(root, "out"),          // the symlink itself, one level up
		filepath.Join(filepath.Dir(root), "pwned"),
	} {
		if _, statErr := os.Lstat(outside); statErr == nil {
			t.Fatalf("the archive wrote %s, outside the destination, through a chain of symlinks each of which resolved inside lexically", outside)
		}
	}
	if err == nil {
		t.Fatal("the extractor accepted an archive whose entries only resolve inside the destination lexically")
	}
	if errors.Is(err, os.ErrPermission) {
		t.Fatalf("the extractor failed on permissions rather than on the escape: %v", err)
	}
}

// And the shapes real runtime archives use keep working: a leaf symlink whose
// target climbs with ".." to a sibling tree, which is how node's bin/npm points
// at lib/node_modules/npm/bin/npm-cli.js.
func TestATarSymlinkClimbingToASiblingTreeStillExtracts(t *testing.T) {
	root := tempDir(t)
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(root, "probe-link")
	if err := os.Symlink(root, probe); err != nil {
		t.Skipf("creating symlinks on Windows needs privilege or Developer Mode: %v", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, typeflag byte, link string, body []byte) {
		h := &tar.Header{Name: name, Typeflag: typeflag, Linkname: link, Mode: 0o755, Size: int64(len(body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if len(body) > 0 {
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("node-v1/", tar.TypeDir, "", nil)
	add("node-v1/bin/", tar.TypeDir, "", nil)
	add("node-v1/lib/node_modules/npm/bin/", tar.TypeDir, "", nil)
	add("node-v1/lib/node_modules/npm/bin/npm-cli.js", tar.TypeReg, "", []byte("#!/usr/bin/env node\n"))
	add("node-v1/bin/npm", tar.TypeSymlink, "../lib/node_modules/npm/bin/npm-cli.js", nil)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "node.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ExtractTarGz(archive, dest); err != nil {
		t.Fatalf("a real archive's shape was refused: %v", err)
	}
	// The extractor strips the archive's top-level folder, as it does for a
	// real node-vX.Y.Z-<os>-<arch>/ tarball.
	target, err := os.Readlink(filepath.Join(dest, "bin", "npm"))
	if err != nil {
		t.Fatalf("bin/npm was not created as a symlink: %v", err)
	}
	if filepath.ToSlash(target) != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Fatalf("bin/npm points at %q", target)
	}
}
