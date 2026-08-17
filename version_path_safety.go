package main

import (
	"fmt"
	"strings"
)

// safeVersionComponent validates that a resolved version string can be used as a
// single path segment under ~/.nvx/versions, returning an error naming the version
// if it cannot.
//
// The install and uninstall paths build a directory as
// filepath.Join(nvxHome, "versions", <runtime>, <version>) and then call
// os.RemoveAll on it. A version containing ".." or a path separator would resolve
// that directory outside the versions tree, turning an install into a deletion of
// whatever it landed on. Nothing validated the string, and nvx had no
// version-validation helper at all.
//
// Version strings are not simply what the user typed. They come from release
// indexes fetched over the network, from directory names discovered while
// importing an existing nvm or volta installation, and from project files -- a
// .nvmrc, a .bun-version, or an engines field in a package.json, any of which
// arrives with a cloned repository.
//
// The check is an allowlist rather than a search for bad sequences: real versions
// are alphanumerics plus the small punctuation set semver uses (1.2.3, v20.0.0,
// 1.0.0-rc.1, 1.0.0+build.5), so anything outside it is rejected without needing to
// enumerate every dangerous encoding.
func safeVersionComponent(version string) error {
	if version == "" {
		return fmt.Errorf("empty version string")
	}
	if len(version) > 128 {
		return fmt.Errorf("version string is implausibly long (%d characters)", len(version))
	}
	// "." and ".." are valid under the allowlist below but are exactly the traversal
	// this exists to stop.
	if version == "." || version == ".." {
		return fmt.Errorf("invalid version %q", version)
	}
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == '+':
			continue
		}
		return fmt.Errorf("invalid character %q in version %q", r, version)
	}
	// Belt and braces: a separator would already have failed the allowlist, but
	// state the property being relied on so a future widening of the character set
	// cannot silently reintroduce traversal.
	if strings.ContainsAny(version, `/\:`) {
		return fmt.Errorf("version %q must not contain a path separator", version)
	}
	return nil
}
