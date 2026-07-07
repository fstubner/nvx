package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// pathResolveExts returns the executable extensions to try when resolving a
// command name against PATH, mirroring how a shell would. On Windows the shim
// dir holds "<cmd>.cmd"/".ps1" and runtimes hold "<cmd>.exe"; the order matters
// only within a single directory (first directory on PATH always wins).
func pathResolveExts() []string {
	if runtime.GOOS == "windows" {
		return []string{".com", ".exe", ".bat", ".cmd", ".ps1", ""}
	}
	return []string{""}
}

// resolveCommandOnPath returns the absolute path a shell would execute for name,
// scanning pathEnv left to right. It does not consult or mutate the process
// environment, so it is safe to call for diagnosis. Returns "" if unresolved.
func resolveCommandOnPath(name, pathEnv string) string {
	exts := pathResolveExts()
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		for _, ext := range exts {
			cand := filepath.Join(dir, name+ext)
			info, err := os.Stat(cand)
			if err != nil || info.IsDir() {
				continue
			}
			if runtime.GOOS == "windows" || info.Mode()&0111 != 0 {
				return cand
			}
		}
	}
	return ""
}
