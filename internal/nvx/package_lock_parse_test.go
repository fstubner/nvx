package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// A lockfileVersion 3 file, which is what npm has written by default since
// npm 9. The shape that matters is `packages[].dependencies`, whose values are
// semver range STRINGS rather than nested objects.
//
// This is the case that broke. packageLockFile typed `packages` and the legacy
// top-level `dependencies` with one Go type, so decoding any real v2 or v3
// lockfile failed with "cannot unmarshal string into Go struct field
// packageLockPackage.packages.dependencies", the whole file was discarded, and
// the caller silently fell back to package.json. That names direct
// dependencies only and carries ranges rather than resolved versions, so
// verification ran against a fraction of the tree while looking healthy.
const lockV3 = `{
  "name": "example",
  "lockfileVersion": 3,
  "packages": {
    "": {
      "name": "example",
      "dependencies": { "left-pad": "^1.3.0" }
    },
    "node_modules/left-pad": {
      "version": "1.3.0",
      "dependencies": { "right-pad": "^2.0.0" }
    },
    "node_modules/right-pad": {
      "version": "2.0.0"
    }
  }
}`

// lockfileVersion 1, where the top-level `dependencies` really does nest
// objects. Both shapes have to keep working, which is the reason for two types
// rather than one relaxed one.
const lockV1 = `{
  "name": "example",
  "lockfileVersion": 1,
  "dependencies": {
    "left-pad": {
      "version": "1.3.0",
      "dependencies": {
        "right-pad": { "version": "2.0.0" }
      }
    }
  }
}`

func inDirWithLock(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestPackagesFromLockfileV3(t *testing.T) {
	inDirWithLock(t, lockV3)

	got := packagesFromPackageLock()
	want := []string{"left-pad@1.3.0", "right-pad@2.0.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// The root entry is keyed "" and has no version. Naming it would send an empty
// or bogus package name to the verification checks.
func TestPackagesFromLockfileV3SkipsTheRootEntry(t *testing.T) {
	inDirWithLock(t, lockV3)

	for _, p := range packagesFromPackageLock() {
		if p == "@" || p == "example@" || p[0] == '@' && len(p) == 1 {
			t.Fatalf("root entry leaked into %v", packagesFromPackageLock())
		}
	}
}

func TestPackagesFromLockfileV1StillNests(t *testing.T) {
	inDirWithLock(t, lockV1)

	got := packagesFromPackageLock()
	want := []string{"left-pad@1.3.0", "right-pad@2.0.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// npm walks up from the working directory to the nearest package.json and
// installs from there, so `cd src/deep && npm ci` is an ordinary invocation.
// The verification readers read `./package-lock.json` and `./package.json`
// literally, found nothing in the subdirectory, and handed the checks an
// empty list. The blocklist, typosquat, OSV and release-age gates then passed
// by having nothing to look at, with no warning. The sandbox still ran, so the
// install looked normal. Reproduced with the real binary on 2026-09-17.
func TestVerificationFindsTheLockfileFromASubdirectory(t *testing.T) {
	inDirWithLock(t, lockV3)
	if err := os.WriteFile("package.json", []byte(`{"name":"example","dependencies":{"left-pad":"^1.3.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("src", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join("src", "deep")); err != nil {
		t.Fatal(err)
	}

	got := detectShimPackagesForVerification("npm", []string{"ci"})
	if len(got) == 0 {
		t.Fatal("npm ci from a subdirectory resolved no packages, so every pre-install check is skipped")
	}
	want := []string{"left-pad@1.3.0", "right-pad@2.0.0"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}
