package nvx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeBunCache(t *testing.T, nvxHome string, age time.Duration, versions ...string) {
	t.Helper()
	path := bunCachePath(nvxHome)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(githubReleaseCache{FetchedAt: time.Now().Add(-age), Versions: versions})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func offlineBun(t *testing.T) {
	t.Helper()
	orig := fetchBunReleaseList
	fetchBunReleaseList = func() ([]string, error) { return nil, errors.New("dial tcp: no route to host") }
	t.Cleanup(func() { fetchBunReleaseList = orig })
}

// A cached Bun release list does not stand in for the real one indefinitely.
//
// The cache exists to stay under GitHub's rate limit and is refreshed every six
// hours. When the fetch fails, any cache at all was accepted, with no upper
// bound on its age -- so `nvx install bun latest` on a machine that has been
// off the network for months installed whatever was newest back then, called it
// latest, and the only sign was one line saying the fetch had failed. Bun ships
// security fixes in patch releases; "latest" resolving to a months-old one is a
// wrong answer, not a degraded one.
//
// Past the bound it fails instead, and says how old the list is. An exact
// version still installs offline -- that path never consults this list.
func TestAMonthsOldBunCacheIsNotServedAsLatest(t *testing.T) {
	nvxHome := tempDir(t)
	writeBunCache(t, nvxHome, 90*24*time.Hour, "v1.0.0")
	offlineBun(t)

	_, err := fetchBunReleases(nvxHome)
	if err == nil {
		t.Fatal("a 90-day-old release list was served as the current one")
	}
	if !strings.Contains(err.Error(), "90 days") {
		t.Fatalf("the failure does not say how old the list is: %v", err)
	}
}

// Inside the bound the fallback still works, which is the point of having one:
// a laptop offline for the afternoon still resolves `latest`.
func TestARecentBunCacheStillServesWhenOffline(t *testing.T) {
	nvxHome := tempDir(t)
	writeBunCache(t, nvxHome, 48*time.Hour, "v1.2.19")
	offlineBun(t)

	got, err := fetchBunReleases(nvxHome)
	if err != nil {
		t.Fatalf("a two-day-old list should still serve while offline: %v", err)
	}
	if len(got) != 1 || got[0] != "v1.2.19" {
		t.Fatalf("unexpected versions: %v", got)
	}
}

// And the stale-fallback warning says the age, so the number in it is one the
// reader can act on rather than "cached".
func TestTheStaleBunCacheWarningSaysHowOldItIs(t *testing.T) {
	nvxHome := tempDir(t)
	writeBunCache(t, nvxHome, 48*time.Hour, "v1.2.19")
	offlineBun(t)

	out := captureStderrHere(t, func() { _, _ = fetchBunReleases(nvxHome) })
	if !strings.Contains(out, "2 days") {
		t.Fatalf("the warning does not say how old the cached list is:\n%s", out)
	}
}
