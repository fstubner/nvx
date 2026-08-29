//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Remembering which paths already carry a sandbox grant.
//
// Granting is already idempotent: appContainerHasGrant reads the ACL first and
// skips the expensive write when the ACE is present. After a project's first
// contained run every grant takes that path, so the steady-state cost is not the
// grants at all -- it is the CHECKS, one `icacls <path>` process per path per
// launch.
//
// Measured on Windows 11, a warm `nvx --strict shim node -e 0`: 17 icacls
// launches, ~20ms each, ~350ms of a ~410ms sandbox setup. The ACL work itself is
// trivial; the cost is spawning the process. About ten of those paths are
// identical on every run for the same project and runtime -- the ancestor
// traverse chains, the runtime directory and its node.exe, the supervisor
// directory -- so the same answer is bought seventeen times a command, for ever.
//
// Only positive answers are cached, and that asymmetry is what makes this safe.
// A cached "already granted" that has gone stale makes nvx skip a grant it
// needed, so the launch fails; it can never make nvx grant more than it meant to.
// The failure direction is a broken command, not a widened sandbox.
//
// Negative answers are deliberately NOT cached. "Not granted yet" is the state a
// launch is about to change, and remembering it would mean skipping the grant
// that fixes it.
const grantCacheTTL = 7 * 24 * time.Hour

var (
	grantCacheMu      sync.Mutex
	grantCacheLoaded  bool
	grantCacheEntries map[string]time.Time
)

func grantCachePath() string {
	return filepath.Join(GetHomeDir(), "grant-cache.json")
}

func grantCacheKey(sidStr, path string) string {
	return strings.ToLower(sidStr + "|" + filepath.Clean(path))
}

// loadGrantCacheLocked fills the in-process map once. The caller holds the mutex.
//
// Read once per process rather than per lookup: a launch asks about a dozen
// paths, and re-reading the file for each would trade process spawns for file
// reads instead of removing the work.
func loadGrantCacheLocked() {
	if grantCacheLoaded {
		return
	}
	grantCacheLoaded = true
	grantCacheEntries = map[string]time.Time{}

	data, err := os.ReadFile(grantCachePath())
	if err != nil {
		return
	}
	var raw map[string]string
	if json.Unmarshal(data, &raw) != nil {
		// Corrupt means "nothing is known", which costs a re-check rather than
		// skipping a grant.
		return
	}
	for k, ts := range raw {
		at, err := time.Parse(time.RFC3339, ts)
		if err != nil || time.Since(at) > grantCacheTTL {
			continue
		}
		grantCacheEntries[k] = at
	}
}

func saveGrantCacheLocked() {
	raw := map[string]string{}
	for k, at := range grantCacheEntries {
		raw[k] = at.UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	home := GetHomeDir()
	if os.MkdirAll(home, 0o700) != nil {
		return
	}
	// Best-effort: this is a cache, and failing to write it restores exactly the
	// behaviour that existed before it.
	_ = os.WriteFile(grantCachePath(), data, 0o600)
}

// grantCacheHas reports whether this grant was verified recently enough to skip
// re-reading the ACL.
func grantCacheHas(sidStr, path string) bool {
	grantCacheMu.Lock()
	defer grantCacheMu.Unlock()
	loadGrantCacheLocked()
	_, ok := grantCacheEntries[grantCacheKey(sidStr, path)]
	return ok
}

// grantCacheRecord notes that path carries an ACE for sidStr.
func grantCacheRecord(sidStr, path string) {
	grantCacheMu.Lock()
	defer grantCacheMu.Unlock()
	loadGrantCacheLocked()
	key := grantCacheKey(sidStr, path)
	if _, had := grantCacheEntries[key]; had {
		return // already known; no need to rewrite the file
	}
	grantCacheEntries[key] = time.Now()
	saveGrantCacheLocked()
}

// grantCacheForgetUnder drops the entry for root and for everything beneath it.
//
// The entries nvx writes are inheritable, so withdrawing one on a directory also
// removes the access its descendants had through it. Forgetting only the exact
// path left those descendants cached as granted: nvx logged that it had granted
// them, skipped the grant because the cache said so, and the sandbox got EPERM on
// a directory the policy still named -- for the full seven-day cache lifetime,
// with the log saying the opposite. Measured with a parent and child both named
// in a policy, the parent then dropped.
func grantCacheForgetUnder(sidStr, root string) {
	grantCacheMu.Lock()
	defer grantCacheMu.Unlock()
	loadGrantCacheLocked()
	prefix := grantCacheKey(sidStr, root)
	changed := false
	for key := range grantCacheEntries {
		// Either the directory itself, or something below it. The separator check
		// keeps "C:\ab" from matching an entry for "C:\abc".
		if key == prefix || strings.HasPrefix(key, prefix+string(filepath.Separator)) {
			delete(grantCacheEntries, key)
			changed = true
		}
	}
	if changed {
		saveGrantCacheLocked()
	}
}

// invalidateGrantCache forgets everything, so the next launch re-reads every ACL.
//
// Called when an AppContainer launch fails. If an ACE was removed behind nvx's
// back -- by a repair tool, a profile reset, someone tidying permissions -- the
// cache would otherwise keep nvx skipping the grant that would fix it, for a
// week, and the only cure would be knowing this file exists. Same reasoning as
// the ancestor-skip TTL next door: recovery should not require having read the
// source.
//
// Throwing the whole cache away rather than one entry is deliberate. A launch
// failure does not say which path was at fault, and re-checking a dozen paths
// once costs a few hundred milliseconds on a command that has already failed.
func invalidateGrantCache() {
	grantCacheMu.Lock()
	defer grantCacheMu.Unlock()
	grantCacheEntries = map[string]time.Time{}
	grantCacheLoaded = true
	_ = os.Remove(grantCachePath())
}
