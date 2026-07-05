package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// binResolveCache memoizes system-PATH command lookups. exec.LookPath is very
// slow on Windows with a long PATH (a stat per directory per PATHEXT), and the
// shim path resolves the same command on every invocation. The cache is keyed
// by a hash of the current PATH so it invalidates automatically when PATH
// changes, and each hit is re-validated with a single stat.
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
	if info, err := os.Stat(p); err != nil || info.IsDir() {
		return ""
	}
	return p
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
