package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Placing archive entries by REAL path.
//
// The tar extractor checked each symlink's target lexically, against the
// directory of the entry's own path. That is correct for a single link and
// wrong as soon as the path to an entry passes through links created by
// earlier entries: `link` -> `sub`, `sub/up` -> `..`, then a symlink at
// `link/up/out` -> `..` resolves lexically to <dest>/link, inside, and in the
// filesystem to <dest>/.., outside. A file entry at `link/up/out/pwned` then
// lands in the destination's parent. Measured: it did, on Linux, with every
// individual target passing the old check.
//
// So the parent of every entry is resolved through the filesystem as it
// stands when the entry is written -- each existing component that is a
// symlink is followed, and the result must stay inside the destination at
// every step -- and a symlink's target is walked the same way from that real
// parent. Components that do not exist yet are taken as they are; they cannot
// be symlinks.

// resolveWithin walks rel from start, a real path inside realDest, following
// any existing symlink it meets, and returns the real path reached. It fails
// the moment any step leaves realDest.
func resolveWithin(realDest, start, rel string) (string, error) {
	inside := func(p string) bool {
		return p == realDest || strings.HasPrefix(p, realDest+string(os.PathSeparator))
	}
	current := filepath.Clean(start)
	if !inside(current) {
		return "", fmt.Errorf("%s is outside %s", current, realDest)
	}
	for _, c := range strings.Split(filepath.ToSlash(rel), "/") {
		switch c {
		case "", ".":
			continue
		case "..":
			current = filepath.Dir(current)
		default:
			current = filepath.Join(current, c)
			if fi, err := os.Lstat(current); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(current)
				if err != nil {
					return "", fmt.Errorf("resolve %s: %w", current, err)
				}
				current = resolved
			}
		}
		if !inside(current) {
			return "", fmt.Errorf("%q resolves to %s, outside the destination", rel, current)
		}
	}
	return current, nil
}

// extractTargetWithin returns where an entry that the lexical check placed at
// fpath (under destDir) must actually be written: its parent resolved through
// the filesystem, plus its own name.
func extractTargetWithin(realDest, destDir, fpath string) (string, error) {
	rel, err := filepath.Rel(destDir, fpath)
	if err != nil {
		return "", fmt.Errorf("entry %s is not under the destination: %w", fpath, err)
	}
	realParent, err := resolveWithin(realDest, realDest, filepath.Dir(rel))
	if err != nil {
		return "", fmt.Errorf("illegal entry path %s: %w", fpath, err)
	}
	return filepath.Join(realParent, filepath.Base(rel)), nil
}
