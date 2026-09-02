package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Version ranges, for resolving what to use against what is installed.
//
// nvx used to reduce a range to its lower bound and treat that as the answer.
// CleanEngineRange(">=18 <25") returned "18", so a project declaring that in
// package.json made nvx want to install exactly 18 -- on a machine with 20, 22
// and 24 installed, every one of which satisfies it. "^20 || ^22" picked 20 and
// ignored the 22 sitting there. And `lts` resolved only against the download
// list, so `nvx install lts` worked while `nvx use lts` said "no installed
// version matches query 'lts'" about a version it had just installed.
//
// A DELIBERATE SUBSET of the npm range grammar, with an error for the rest.
// Chosen over a fuller implementation because the failure modes are not
// symmetric: a matcher that does not understand something and says so costs the
// user one clear message, while one that quietly mis-parses picks a plausible
// wrong version and is discovered later, somewhere else. What is supported is
// everything the audit found in real projects; what is not is named in the error.
//
// Supported:
//
//	22            22.23         22.23.2        v22.23.2     (exact or prefix)
//	22.x          22.*          22.23.x
//	^22.1.0       ~22.1.0       ^0.2.3         (caret, tilde)
//	>=18  >18  <=24  <25  =22                  (comparators)
//	>=18 <25                                   (AND: space-separated)
//	^20 || ^22                                 (OR)
//	*             (empty)                      (anything)
//
// Not supported, and reported rather than guessed at: hyphen ranges
// ("18 - 24"), prerelease identifiers ("22.0.0-rc.1"), and build metadata.

type semver struct{ major, minor, patch int }

func (v semver) compare(o semver) int {
	switch {
	case v.major != o.major:
		return v.major - o.major
	case v.minor != o.minor:
		return v.minor - o.minor
	default:
		return v.patch - o.patch
	}
}

// parseSemver reads "v22.23.2", "22.23" or "22". Missing parts are zero, and how
// many were present is returned so a prefix can be widened into a range.
func parseSemver(s string) (v semver, parts int, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "v")
	if s == "" {
		return v, 0, fmt.Errorf("empty version")
	}
	if strings.ContainsAny(s, "-+") {
		return v, 0, fmt.Errorf("prerelease and build metadata are not supported in %q", s)
	}
	fields := strings.Split(s, ".")
	if len(fields) > 3 {
		return v, 0, fmt.Errorf("%q has more than three version parts", s)
	}
	out := []int{0, 0, 0}
	for i, f := range fields {
		// "x" and "*" mean "anything here", which the caller turns into a range;
		// they end the significant part of the version.
		if f == "x" || f == "*" {
			return semver{out[0], out[1], out[2]}, i, nil
		}
		n, convErr := strconv.Atoi(f)
		if convErr != nil || n < 0 {
			return v, 0, fmt.Errorf("%q is not a version number", s)
		}
		out[i] = n
	}
	return semver{out[0], out[1], out[2]}, len(fields), nil
}

type comparator struct {
	op string // one of >=, >, <=, <, =
	v  semver
}

func (c comparator) allows(v semver) bool {
	d := v.compare(c.v)
	switch c.op {
	case ">=":
		return d >= 0
	case ">":
		return d > 0
	case "<=":
		return d <= 0
	case "<":
		return d < 0
	default:
		return d == 0
	}
}

// versionRange is an OR of AND-groups, which is how npm ranges are shaped.
type versionRange struct{ or [][]comparator }

func (r versionRange) allows(v semver) bool {
	for _, group := range r.or {
		ok := true
		for _, c := range group {
			if !c.allows(v) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// matchesAnything reports a range that places no constraint at all, so callers
// can fall back to "newest installed" rather than filtering by it.
func (r versionRange) matchesAnything() bool {
	return len(r.or) == 1 && len(r.or[0]) == 0
}

// parseVersionRange parses the supported subset, or explains what it could not
// read. The error is shown to the user, so it names the offending text.
func parseVersionRange(expr string) (versionRange, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "*" || strings.EqualFold(expr, "latest") {
		return versionRange{or: [][]comparator{{}}}, nil
	}
	if strings.Contains(expr, " - ") {
		return versionRange{}, fmt.Errorf("hyphen ranges are not supported (%q); write it as '>=%s'",
			expr, strings.TrimSpace(strings.SplitN(expr, " - ", 2)[0]))
	}

	var out versionRange
	for _, alt := range strings.Split(expr, "||") {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			return versionRange{}, fmt.Errorf("empty alternative in %q", expr)
		}
		var group []comparator
		for _, token := range strings.Fields(alt) {
			cs, err := parseComparator(token)
			if err != nil {
				return versionRange{}, err
			}
			group = append(group, cs...)
		}
		if len(group) == 0 {
			return versionRange{}, fmt.Errorf("nothing to match in %q", alt)
		}
		out.or = append(out.or, group)
	}
	return out, nil
}

// parseComparator turns one token into the comparators it stands for. A caret,
// tilde or partial version becomes a pair bounding the range it means.
func parseComparator(token string) ([]comparator, error) {
	switch {
	// A partial version inside a comparator is an X-range, and the two
	// upper-bound forms shift to the next part rather than reading the missing
	// parts as zero: "<=22" means every 22.x, not "at most 22.0.0", and ">22"
	// means from 23 rather than anything above 22.0.0. Reading them as zeros made
	// "<=22" exclude v22.23.2 -- the newest thing that obviously satisfies it.
	case strings.HasPrefix(token, ">="), strings.HasPrefix(token, "<"):
		op := token[:1]
		rest := token[1:]
		if strings.HasPrefix(token, ">=") || strings.HasPrefix(token, "<=") {
			op, rest = token[:2], token[2:]
		}
		v, parts, err := parseSemver(rest)
		if err != nil {
			return nil, err
		}
		if op == "<=" && parts > 0 && parts < 3 {
			return []comparator{{"<", nextAfterPrefix(v, parts)}}, nil
		}
		if parts == 0 {
			return []comparator{}, nil // ">=x" constrains nothing
		}
		return []comparator{{op, v}}, nil
	case strings.HasPrefix(token, ">"):
		v, parts, err := parseSemver(token[1:])
		if err != nil {
			return nil, err
		}
		if parts == 0 {
			return []comparator{}, nil
		}
		if parts < 3 {
			return []comparator{{">=", nextAfterPrefix(v, parts)}}, nil
		}
		return []comparator{{">", v}}, nil
	case strings.HasPrefix(token, "^"):
		v, _, err := parseSemver(token[1:])
		if err != nil {
			return nil, err
		}
		return []comparator{{">=", v}, {"<", caretCeiling(v)}}, nil
	case strings.HasPrefix(token, "~"):
		v, parts, err := parseSemver(token[1:])
		if err != nil {
			return nil, err
		}
		return []comparator{{">=", v}, {"<", tildeCeiling(v, parts)}}, nil
	case strings.HasPrefix(token, "="):
		v, parts, err := parseSemver(token[1:])
		if err != nil {
			return nil, err
		}
		return prefixComparators(v, parts), nil
	default:
		v, parts, err := parseSemver(token)
		if err != nil {
			return nil, err
		}
		return prefixComparators(v, parts), nil
	}
}

// prefixComparators turns a bare or partial version into what it means: "22" is
// every 22.x, "22.23" is every 22.23.x, and a full version is itself.
func prefixComparators(v semver, parts int) []comparator {
	switch parts {
	case 0:
		return []comparator{}
	case 1:
		return []comparator{{">=", v}, {"<", semver{v.major + 1, 0, 0}}}
	case 2:
		return []comparator{{">=", v}, {"<", semver{v.major, v.minor + 1, 0}}}
	default:
		return []comparator{{"=", v}}
	}
}

// nextAfterPrefix is the version just past everything a partial version covers:
// "22" covers up to but not including 23.0.0, "22.11" up to 22.12.0.
func nextAfterPrefix(v semver, parts int) semver {
	if parts <= 1 {
		return semver{v.major + 1, 0, 0}
	}
	return semver{v.major, v.minor + 1, 0}
}

// caretCeiling is npm's caret rule: the leftmost non-zero part is what is held.
func caretCeiling(v semver) semver {
	switch {
	case v.major > 0:
		return semver{v.major + 1, 0, 0}
	case v.minor > 0:
		return semver{0, v.minor + 1, 0}
	default:
		return semver{0, 0, v.patch + 1}
	}
}

// tildeCeiling holds the minor when one was given, the major otherwise.
func tildeCeiling(v semver, parts int) semver {
	if parts <= 1 {
		return semver{v.major + 1, 0, 0}
	}
	return semver{v.major, v.minor + 1, 0}
}

// highestMatching returns the newest of versions that the expression allows.
// versions are "vX.Y.Z" strings as they appear on disk; unparseable ones are
// skipped rather than failing the lookup.
func highestMatching(expr string, versions []string) (string, error) {
	r, err := parseVersionRange(expr)
	if err != nil {
		return "", err
	}
	best, bestV, found := "", semver{}, false
	for _, name := range versions {
		v, _, perr := parseSemver(name)
		if perr != nil {
			continue
		}
		if !r.matchesAnything() && !r.allows(v) {
			continue
		}
		if !found || v.compare(bestV) > 0 {
			best, bestV, found = name, v, true
		}
	}
	if !found {
		return "", fmt.Errorf("no version matches %q", expr)
	}
	return best, nil
}

// isUnsupportedRange distinguishes "I could not read this expression" from "I
// read it and nothing matched". The two need different messages: the first is a
// syntax the user should rewrite, the second is a version they should install.
func isUnsupportedRange(err error) bool {
	return err != nil && !strings.HasPrefix(err.Error(), "no version matches")
}
