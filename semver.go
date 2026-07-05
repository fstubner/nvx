package main

import (
	"fmt"
	"strconv"
	"strings"
)

// semver is a parsed semantic version. Build metadata is parsed but ignored for
// precedence, per semver.org §10.
type semver struct {
	major, minor, patch int
	prerelease          []string // empty means a normal release
}

// parseSemver parses "1.2.3", "v1.2.3", "1.2", "1", "1.2.3-rc.1", "1.2.3+build".
// Missing minor/patch default to 0 (so "18" == "18.0.0").
func parseSemver(s string) (semver, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return semver{}, fmt.Errorf("empty version")
	}
	// Strip build metadata (ignored for precedence).
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre []string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		preStr := s[i+1:]
		s = s[:i]
		if preStr != "" {
			pre = strings.Split(preStr, ".")
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return semver{}, fmt.Errorf("invalid version %q", s)
	}
	nums := [3]int{0, 0, 0}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("invalid version component %q", p)
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], prerelease: pre}, nil
}

// compareSemver returns -1, 0, or 1 (a<b, a==b, a>b) with full prerelease
// precedence per semver.org §11.
func compareSemver(a, b semver) int {
	if c := cmpInt(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpInt(a.minor, b.minor); c != 0 {
		return c
	}
	if c := cmpInt(a.patch, b.patch); c != 0 {
		return c
	}
	// A release outranks a prerelease of the same core version.
	aPre, bPre := len(a.prerelease) > 0, len(b.prerelease) > 0
	if aPre && !bPre {
		return -1
	}
	if !aPre && bPre {
		return 1
	}
	if !aPre && !bPre {
		return 0
	}
	return comparePrerelease(a.prerelease, b.prerelease)
}

func comparePrerelease(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		ai, aErr := strconv.Atoi(a[i])
		bi, bErr := strconv.Atoi(b[i])
		switch {
		case aErr == nil && bErr == nil: // both numeric
			if c := cmpInt(ai, bi); c != 0 {
				return c
			}
		case aErr == nil && bErr != nil: // numeric < alphanumeric
			return -1
		case aErr != nil && bErr == nil:
			return 1
		default: // both alphanumeric
			if a[i] < b[i] {
				return -1
			}
			if a[i] > b[i] {
				return 1
			}
		}
	}
	return cmpInt(len(a), len(b)) // more identifiers > fewer
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// semverSatisfies reports whether version satisfies a range expression.
// Supported: exact ("1.2.3"), caret (^1.2.3), tilde (~1.2.3), comparators
// (>=, >, <=, <, =), x-ranges (1, 1.x, 1.2.x, *), space-separated AND, and
// "||" OR. This mirrors the npm/node engines subset nvx needs.
func semverSatisfies(version, rangeExpr string) bool {
	v, err := parseSemver(version)
	if err != nil {
		return false
	}
	rangeExpr = strings.TrimSpace(rangeExpr)
	if rangeExpr == "" || rangeExpr == "*" || rangeExpr == "x" || rangeExpr == "latest" {
		return true
	}
	for _, orClause := range strings.Split(rangeExpr, "||") {
		if satisfiesAND(v, strings.Fields(orClause)) {
			return true
		}
	}
	return false
}

// satisfiesAND is true when v matches every comparator in the AND clause.
func satisfiesAND(v semver, comparators []string) bool {
	if len(comparators) == 0 {
		return false
	}
	for _, c := range comparators {
		if !satisfiesOne(v, c) {
			return false
		}
	}
	return true
}

func satisfiesOne(v semver, comp string) bool {
	comp = strings.TrimSpace(comp)
	if comp == "" || comp == "*" || comp == "x" {
		return true
	}
	switch {
	case strings.HasPrefix(comp, ">="):
		return cmpTo(v, comp[2:]) >= 0
	case strings.HasPrefix(comp, "<="):
		return cmpTo(v, comp[2:]) <= 0
	case strings.HasPrefix(comp, ">"):
		return cmpTo(v, comp[1:]) > 0
	case strings.HasPrefix(comp, "<"):
		return cmpTo(v, comp[1:]) < 0
	case strings.HasPrefix(comp, "^"):
		return satisfiesCaret(v, comp[1:])
	case strings.HasPrefix(comp, "~"):
		return satisfiesTilde(v, comp[1:])
	case strings.HasPrefix(comp, "="):
		return matchesXRange(v, comp[1:])
	default:
		return matchesXRange(v, comp)
	}
}

func cmpTo(v semver, s string) int {
	b, err := parseSemver(strings.TrimPrefix(strings.TrimSpace(s), "="))
	if err != nil {
		return -2 // never satisfies
	}
	return compareSemver(v, b)
}

// matchesXRange handles exact and x-range forms: "1", "1.2", "1.2.3", "1.x", "1.2.x".
func matchesXRange(v semver, s string) bool {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	if s == "" || s == "*" || s == "x" {
		return true
	}
	// Strip prerelease/build for x-range core matching.
	core := s
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	comps := []int{v.major, v.minor, v.patch}
	for i := 0; i < len(parts) && i < 3; i++ {
		p := parts[i]
		if p == "x" || p == "X" || p == "*" {
			return true // wildcard from here on
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		if comps[i] != n {
			return false
		}
	}
	// If a full 3-part version with prerelease was given, require exact match.
	if len(parts) == 3 && strings.IndexAny(s, "-") >= 0 {
		want, err := parseSemver(s)
		return err == nil && compareSemver(v, want) == 0
	}
	return true
}

// satisfiesCaret implements ^x.y.z: allows changes that don't modify the
// left-most non-zero component.
func satisfiesCaret(v semver, s string) bool {
	b, err := parseSemver(s)
	if err != nil {
		return false
	}
	if compareSemver(v, b) < 0 {
		return false
	}
	switch {
	case b.major > 0:
		return v.major == b.major
	case b.minor > 0:
		return v.major == 0 && v.minor == b.minor
	default:
		return v.major == 0 && v.minor == 0 && v.patch == b.patch
	}
}

// satisfiesTilde implements ~x.y.z: allows patch-level changes if a minor is
// specified, or minor-level changes if only a major is specified.
func satisfiesTilde(v semver, s string) bool {
	b, err := parseSemver(s)
	if err != nil {
		return false
	}
	if compareSemver(v, b) < 0 {
		return false
	}
	specified := strings.Count(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".") + 1
	if specified >= 2 {
		return v.major == b.major && v.minor == b.minor
	}
	return v.major == b.major
}

// isRangeExpr reports whether q looks like a range rather than a plain version
// or dist-tag (so ResolveVersion knows to run the range matcher).
func isRangeExpr(q string) bool {
	return strings.ContainsAny(q, "^~<>|*") ||
		strings.Contains(q, " ") ||
		strings.Contains(q, ".x") ||
		strings.Contains(q, ".X")
}

// maxSatisfyingVersion returns the highest version in the list satisfying the
// range, or "" if none. Input versions may be "v"-prefixed.
func maxSatisfyingVersion(versions []string, rangeExpr string) string {
	best := ""
	var bestSV semver
	for _, raw := range versions {
		if !semverSatisfies(raw, rangeExpr) {
			continue
		}
		sv, err := parseSemver(raw)
		if err != nil {
			continue
		}
		if best == "" || compareSemver(sv, bestSV) > 0 {
			best = raw
			bestSV = sv
		}
	}
	return best
}
