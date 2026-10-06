package nvx

import (
	"net/url"
	"strings"
)

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

	// An npm alias, alias@npm:target, installs target from the registry, so
	// target is what gets classified. Read whole, `alias@npm:@scope/pkg` looked
	// like the user/repo shorthand and skipped every check.
	if i := strings.Index(lower, "@npm:"); i > 0 && !strings.Contains(lower[:i], ":") {
		return nonRegistrySpecKind(s[i+len("@npm:"):])
	}

	// name@<spec> installs <spec> under that name, and it is the spec that says
	// where it comes from. Read whole, `left-pad@file:lib` looked like a
	// registry name with an odd version and was sent to the registry.
	if name, rest, ok := splitNamedSpec(s); ok && name != "" {
		if kind := nonRegistrySpecKind(rest); kind != "" {
			return kind
		}
	}

	switch {
	case strings.HasPrefix(lower, "file:"):
		return "a file: spec"
	case strings.HasPrefix(lower, "link:"):
		return "a link: spec"
	case strings.HasPrefix(lower, "workspace:"):
		return "a workspace: spec"
	case strings.HasPrefix(lower, "portal:"):
		return "a portal: spec"
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return "a URL"
	case strings.HasPrefix(lower, "git://"), strings.HasPrefix(lower, "git+"), strings.HasPrefix(lower, "ssh://"):
		return "a git URL"
	case strings.HasPrefix(lower, "github:"), strings.HasPrefix(lower, "gitlab:"),
		strings.HasPrefix(lower, "bitbucket:"), strings.HasPrefix(lower, "gist:"):
		return "a hosted-repository spec"
	// "~/" is a home-directory path. A bare "~" starts a tilde range such as
	// ~1.2.3, which comes from the registry.
	case strings.HasPrefix(s, "./"), strings.HasPrefix(s, "../"), strings.HasPrefix(s, ".\\"),
		strings.HasPrefix(s, "..\\"), strings.HasPrefix(s, "/"), strings.HasPrefix(s, "~/"), strings.HasPrefix(s, "~\\"):
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

// isRemoteSourceSpec reports a spec npm fetches from somewhere other than the
// registry or the local disk: a URL, a git URL or a hosted-repository shorthand.
func isRemoteSourceSpec(spec string) bool {
	switch nonRegistrySpecKind(spec) {
	case "a URL", "a git URL", "a hosted-repository spec":
		return true
	}
	return false
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// splitNamedSpec splits name@rest, where name is a package name. ok is false
// when spec has no such name in front, as with a bare URL or path.
func splitNamedSpec(spec string) (name, rest string, ok bool) {
	if len(spec) < 2 {
		return "", "", false
	}
	i := strings.Index(spec[1:], "@")
	if i < 0 {
		return "", "", false
	}
	name, rest = spec[:i+1], spec[i+2:]
	if rest == "" || strings.ContainsAny(name, ":\\") {
		return "", "", false
	}
	// git@github.com:user/repo is a git address, and "git" is its user.
	if host, _, isSCP := strings.Cut(rest, ":"); isSCP && strings.Contains(host, ".") && !strings.Contains(host, "/") {
		return "", "", false
	}
	// A registry name has a slash only after a scope.
	if strings.Contains(name, "/") && (!strings.HasPrefix(name, "@") || strings.Count(name, "/") != 1) {
		return "", "", false
	}
	return name, rest, true
}

// declaredPackageName is the package name a non-registry spec installs under,
// or "" when the spec does not say. `left-pad@github:user/repo` installs as
// left-pad. A tarball URL on the registry host names its package in the path.
func declaredPackageName(spec string) string {
	if name, _, ok := splitNamedSpec(strings.TrimSpace(spec)); ok {
		return name
	}
	return registryTarballName(spec)
}

// registryTarballName reads the package name out of a tarball URL on the npm
// registry, /<name>/-/<file>.tgz, or returns "" for any other URL.
func registryTarballName(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !isNpmRegistryHost(u.Hostname()) {
		return ""
	}
	before, _, found := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/-/")
	if !found || before == "" {
		return ""
	}
	name, err := url.PathUnescape(before)
	if err != nil {
		return ""
	}
	return name
}

// isNpmRegistryHost reports the public registry's host names. yarn's is the
// same registry under another name.
func isNpmRegistryHost(host string) bool {
	switch strings.ToLower(host) {
	case "registry.npmjs.org", "registry.yarnpkg.com":
		return true
	}
	return false
}
