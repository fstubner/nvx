package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeCacheEntry forges the on-disk cache the way a process that can write into
// ~/.nvx would: a matching PathHash (derivable from the attacker's own
// environment) plus an arbitrary absolute path.
func writeCacheEntry(t *testing.T, nvxHome, cmdName, target string) {
	t.Helper()
	c := binResolveCache{
		PathHash: currentPathHash(),
		Commands: map[string]string{cmdName: target},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	p := binCachePath(nvxHome)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func touchExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// TestLookupBinCacheRejectsPathsOffPATH pins F64. lookupBinCache validated a hit
// with exactly two checks -- the stored PathHash matches, and the target exists --
// and never checked WHERE the target was. The cache maps bare command names to
// absolute paths, and nvx executes the result as the unsandboxed parent, so a
// process able to write that one JSON file gained arbitrary code execution as the
// user on the next node/npm invocation. (On macOS F22 grants exactly that write.)
//
// A legitimate entry can only ever be something lookPathSkippingNvxShimsUncached
// could have produced, so the invariant is: the target's directory must be a
// directory that resolver would search.
func TestLookupBinCacheRejectsPathsOffPATH(t *testing.T) {
	nvxHome := tempDir(t)
	pathDir := tempDir(t)
	elsewhere := tempDir(t) // deliberately NOT on PATH

	legit := filepath.Join(pathDir, exeName("node"))
	touchExecutable(t, legit)

	evil := filepath.Join(elsewhere, exeName("evil"))
	touchExecutable(t, evil)

	t.Setenv("PATH", pathDir)

	// Control: a target in a PATH directory is still a cache hit, so the check
	// cannot be passing merely by rejecting everything.
	writeCacheEntry(t, nvxHome, "node", legit)
	if got := lookupBinCache(nvxHome, "node"); got != legit {
		t.Fatalf("legitimate cached path was rejected: got %q, want %q", got, legit)
	}

	// The finding: a target outside PATH must not be returned, even though it
	// exists and the PathHash matches.
	writeCacheEntry(t, nvxHome, "node", evil)
	if got := lookupBinCache(nvxHome, "node"); got != "" {
		t.Errorf("cached path outside PATH was returned: %q -- nvx would execute this unsandboxed", got)
	}
}

// TestLookupBinCacheRejectsShimDir covers the subtler variant: the shim directory
// IS on PATH, so a bare "is it on PATH" check would accept it -- but the uncached
// resolver deliberately excludes the shim dir (it would resolve nvx's own wrapper
// instead of the real runtime), so a real entry can never point there. It is also
// the one PATH directory a contained process may be able to write to.
func TestLookupBinCacheRejectsShimDir(t *testing.T) {
	nvxHome := tempDir(t)
	shimDir := filepath.Join(nvxHome, "bin")
	planted := filepath.Join(shimDir, exeName("node"))
	touchExecutable(t, planted)

	t.Setenv("PATH", shimDir)

	writeCacheEntry(t, nvxHome, "node", planted)
	if got := lookupBinCache(nvxHome, "node"); got != "" {
		t.Errorf("cached path inside the shim dir was returned: %q", got)
	}
}

// TestLookupBinCacheStillRoundTrips guards the cache's actual purpose: the
// validation must not break a normal store-then-load cycle.
func TestLookupBinCacheStillRoundTrips(t *testing.T) {
	nvxHome := tempDir(t)
	pathDir := tempDir(t)
	bin := filepath.Join(pathDir, exeName("npm"))
	touchExecutable(t, bin)
	t.Setenv("PATH", pathDir)

	storeBinCache(nvxHome, "npm", bin)
	if got := lookupBinCache(nvxHome, "npm"); got != bin {
		t.Fatalf("round-trip failed: got %q, want %q", got, bin)
	}
}
