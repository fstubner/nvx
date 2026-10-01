package nvx

import (
	"path/filepath"
	"testing"
)

// Alpine is detected by its loader, and a glibc system that also carries musl
// is not Alpine.
//
// nvx installs the glibc build, which on Alpine unpacks, reports success and
// then fails with a misleading "fork/exec ...: no such file or directory".
// This pins the detection on a fake root. It was not run against a real
// Alpine or glibc host.
func TestMuslIsDetectedByItsLoaderAndTheAbsenceOfGlibcs(t *testing.T) {
	touch := func(root, rel string) {
		t.Helper()
		path := filepath.Join(root, rel)
		mkdirT(t, filepath.Dir(path))
		writeFileT(t, path, "")
	}

	empty := tempDir(t)
	if hostIsMusl(empty) {
		t.Error("an empty root was called musl")
	}

	alpine := tempDir(t)
	touch(alpine, "lib/ld-musl-x86_64.so.1")
	if !hostIsMusl(alpine) {
		t.Error("a root with only a musl loader was not called musl")
	}

	debianWithMusl := tempDir(t)
	touch(debianWithMusl, "lib/ld-musl-x86_64.so.1")
	touch(debianWithMusl, "lib64/ld-linux-x86-64.so.2")
	if hostIsMusl(debianWithMusl) {
		t.Error("a glibc system with the musl package was called musl")
	}

	arm := tempDir(t)
	touch(arm, "usr/lib/ld-linux-aarch64.so.1")
	if hostIsMusl(arm) {
		t.Error("a glibc arm64 root was called musl")
	}
}
