package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// binResolveCache memoizes system-PATH command lookups. exec.LookPath is very
// slow on Windows with a long PATH (a stat per directory per PATHEXT), and the
// shim path resolves the same command on every invocation. The cache is keyed
// by a hash of the current PATH so it invalidates automatically when PATH
// changes, and each hit is re-validated by a stat plus a check that the target
// still lies on PATH (see cachedPathIsResolvable -- the PathHash alone is not a
// trust boundary, because whoever can write this file can also read PATH).
type binResolveCache struct {
	PathHash string            `json:"path_hash"`
	Commands map[string]string `json:"commands"`
}

func binCachePath(nvxHome string) string {
	return filepath.Join(nvxHome, "cache", "bin-resolve.json")
}

func currentPathHash() string {
	sum := sha256.Sum256([]byte(os.Getenv("PATH")))
	return hex.EncodeToString(sum[:])
}

// lookupBinCache returns a cached path for cmdName when the PATH is unchanged
// and the cached binary still exists, else "".
func lookupBinCache(nvxHome, cmdName string) string {
	data, err := os.ReadFile(binCachePath(nvxHome))
	if err != nil {
		return ""
	}
	var c binResolveCache
	if json.Unmarshal(data, &c) != nil {
		return ""
	}
	if c.PathHash != currentPathHash() {
		return ""
	}
	p := c.Commands[cmdName]
	if p == "" {
		return ""
	}
	if !cachedPathIsResolvable(p, nvxHome) {
		return ""
	}
	if info, err := os.Stat(p); err != nil || info.IsDir() {
		return ""
	}
	return p
}

// cachedPathIsResolvable reports whether p is a path that
// lookPathSkippingNvxShimsUncached could actually have produced for the current
// PATH -- meaning p's directory is one of the directories that resolver searches.
//
// This exists because a cache hit was previously validated by only two things:
// the stored PathHash matched, and the target existed. Neither says anything about
// WHERE the target is. Since this cache maps bare command names to absolute paths
// and nvx executes the result as the unsandboxed parent process, anything able to
// write this one JSON file could obtain arbitrary code execution as the user on
// the next node/npm invocation -- and the PathHash is no obstacle, being a hash of
// a PATH the writer can read. On macOS the Seatbelt profile grants a contained
// process write access to all of ~/.nvx, which is exactly that capability.
//
// Checking the directory rather than maintaining a fixed allowlist is what keeps
// this correct: a legitimate entry is by definition something PATH resolution
// produced, so any path off PATH is not a stale entry but a forged one.
func cachedPathIsResolvable(p, nvxHome string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	dir := filepath.Dir(p)
	shimDir := filepath.Join(nvxHome, "bin")

	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		// The resolver skips the shim dir so it finds the real runtime instead of
		// nvx's own wrapper, so a genuine entry can never point there. It is also
		// the one PATH directory a contained process may be able to write into.
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(shimDir)) {
			continue
		}
		if dirsEqual(entry, dir) {
			return true
		}
	}
	return false
}

// storeBinCache records cmdName -> resolvedPath, best-effort. Writes are atomic
// (temp + rename) so a concurrent shim reading the file never sees a partial
// write; a lost update just causes a future cache miss.
func storeBinCache(nvxHome, cmdName, resolvedPath string) {
	if nvxHome == "" || resolvedPath == "" {
		return
	}
	hash := currentPathHash()
	c := binResolveCache{PathHash: hash, Commands: map[string]string{}}
	if data, err := os.ReadFile(binCachePath(nvxHome)); err == nil {
		var existing binResolveCache
		if json.Unmarshal(data, &existing) == nil && existing.PathHash == hash && existing.Commands != nil {
			c.Commands = existing.Commands
		}
	}
	c.Commands[cmdName] = resolvedPath

	if err := os.MkdirAll(filepath.Dir(binCachePath(nvxHome)), 0700); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp := binCachePath(nvxHome) + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	_ = os.Rename(tmp, binCachePath(nvxHome))
}
