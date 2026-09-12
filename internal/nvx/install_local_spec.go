package nvx

import "strings"

// nonRegistrySpecKind names what a package spec is when it is not a registry
// package name, and returns "" when it is one.
//
// npm accepts far more than names: a directory, a tarball on disk, a `file:`
// spec, a tarball over http, a git URL, and the `user/repo` and `github:`
// shorthands. nvx treated every one of them as a name and asked
// registry.npmjs.org for it. The registry answered 404, and nvx asked whether
// to proceed without metadata checks -- a question that denies when there is no
// terminal to answer it, so installing a local tarball under an agent or in CI
// failed, blaming registry metadata for a package that was never in a registry.
//
// The checks that follow are all registry-shaped: a blocklist of published
// names, an edit-distance comparison against popular names, an advisory lookup
// by name and version, and the age of a published release. None has anything to
// say about a path. Skipping them is what they already do in substance; saying
// so is the part that was missing, so nobody reads a clean run as "this was
// audited".
func nonRegistrySpecKind(spec string) string {
	s := strings.TrimSpace(spec)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)

	switch {
	case strings.HasPrefix(lower, "file:"):
		return "a file: spec"
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return "a URL"
	case strings.HasPrefix(lower, "git://"), strings.HasPrefix(lower, "git+"), strings.HasPrefix(lower, "ssh://"):
		return "a git URL"
	case strings.HasPrefix(lower, "github:"), strings.HasPrefix(lower, "gitlab:"),
		strings.HasPrefix(lower, "bitbucket:"), strings.HasPrefix(lower, "gist:"):
		return "a hosted-repository spec"
	case strings.HasPrefix(s, "./"), strings.HasPrefix(s, "../"), strings.HasPrefix(s, ".\\"),
		strings.HasPrefix(s, "..\\"), strings.HasPrefix(s, "/"), strings.HasPrefix(s, "~"):
		return "a local path"
	case strings.Contains(s, `\`):
		// A Windows path, relative or absolute. No registry name contains a
		// backslash.
		return "a local path"
	case len(s) >= 3 && isASCIILetter(s[0]) && s[1] == ':' && (s[2] == '/' || s[2] == '\\'):
		return "a local path"
	case strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar.gz"):
		return "a tarball"
	case strings.Contains(s, "/") && !strings.HasPrefix(s, "@"):
		// npm's `user/repo` shorthand for GitHub. A registry name carries a
		// slash only when it is scoped, and a scoped name starts with "@".
		return "a hosted-repository spec"
	}
	return ""
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
