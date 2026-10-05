package nvx

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
)

// Holding a lockfile entry to the registry's record of its name and version.
//
// npm fetches a locked package from the entry's `resolved` URL and checks the
// download against its `integrity` hash. The checks read only the entry's name
// and version. So an entry naming left-pad@1.3.0 whose URL and hash were
// is-number's was checked as left-pad and installed is-number, measured
// 2026-10-01. The registry publishes the tarball URL and hash of every version,
// and the entry has to agree with them.

// npmDist is the registry's record of where a version's tarball is and what
// it hashes to.
type npmDist struct {
	tarball, integrity, shasum string
}

// lockEntryMismatch says how a lockfile entry disagrees with the registry's
// record of its name and version, or returns "" when it does not.
//
// A matching hash settles it whatever the URL: npm refuses a download that
// does not match the hash, so a mirror serving the same bytes is fine. With no
// hash to compare, the URL must be the registry's own.
func lockEntryMismatch(t verifyTarget, d npmDist) string {
	if d == (npmDist{}) {
		return "the registry has no tarball for that version"
	}
	if t.integrity != "" {
		switch compareIntegrity(t.integrity, d) {
		case integrityMatches:
			return ""
		case integrityDiffers:
			return "its integrity hash is not the registry's hash for that version"
		}
	}
	if t.resolved != "" && !sameTarball(t.resolved, d.tarball) {
		return "it is fetched from a URL that is not the registry's tarball for that version, and has no matching integrity hash"
	}
	return ""
}

type integrityResult int

const (
	integrityUnknown integrityResult = iota
	integrityMatches
	integrityDiffers
)

// compareIntegrity compares a lockfile's Subresource Integrity string with the
// registry's hashes, algorithm by algorithm. Old lockfiles carry sha1, which
// the registry publishes as a hex shasum.
func compareIntegrity(lock string, d npmDist) integrityResult {
	known := map[string]map[string]bool{}
	addHash := func(alg, digest string) {
		if known[alg] == nil {
			known[alg] = map[string]bool{}
		}
		known[alg][digest] = true
	}
	for _, tok := range strings.Fields(d.integrity) {
		if alg, digest, ok := splitSRI(tok); ok {
			addHash(alg, digest)
		}
	}
	if raw, err := hex.DecodeString(d.shasum); err == nil && len(raw) > 0 {
		addHash("sha1", base64.StdEncoding.EncodeToString(raw))
	}

	result := integrityUnknown
	for _, tok := range strings.Fields(lock) {
		alg, digest, ok := splitSRI(tok)
		if !ok || known[alg] == nil {
			continue
		}
		if !known[alg][digest] {
			return integrityDiffers
		}
		result = integrityMatches
	}
	return result
}

// splitSRI splits "sha512-<base64>[?options]".
func splitSRI(tok string) (alg, digest string, ok bool) {
	alg, digest, ok = strings.Cut(tok, "-")
	if !ok || digest == "" {
		return "", "", false
	}
	digest, _, _ = strings.Cut(digest, "?")
	return strings.ToLower(alg), digest, true
}

// sameTarball compares two tarball URLs as npm's registry serves them, with
// registry.yarnpkg.com read as the registry it fronts.
func sameTarball(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil || ub.Host == "" {
		return false
	}
	host := func(u *url.URL) string {
		h := strings.ToLower(u.Hostname())
		if isNpmRegistryHost(h) {
			return "registry.npmjs.org"
		}
		return h
	}
	path := func(u *url.URL) string {
		p, err := url.PathUnescape(u.EscapedPath())
		if err != nil {
			return u.EscapedPath()
		}
		return p
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && host(ua) == host(ub) && path(ua) == path(ub)
}
