package main

import "strings"

// isolationLevel selects how invocation classes map to "contained?". Standard
// (default) contains only code you did not write (installs, ad-hoc tools);
// strict extends containment to your own code too.
type isolationLevel int

const (
	levelStandard isolationLevel = iota
	levelStrict
)

// parseIsolationLevel parses a policy/flag value into an isolationLevel. An
// empty string is treated as the default (standard, ok=true). An unrecognized
// non-empty value returns ok=false so callers can warn rather than silently
// falling back.
func parseIsolationLevel(s string) (isolationLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "standard":
		return levelStandard, true
	case "strict":
		return levelStrict, true
	default:
		return levelStandard, false
	}
}

func (l isolationLevel) String() string {
	if l == levelStrict {
		return "strict"
	}
	return "standard"
}

// shouldContain is the pure containment decision: given what kind of code is
// being invoked, the effective isolation level, and any per-command flag
// override, should this invocation run inside the sandbox?
func shouldContain(class invocationClass, level isolationLevel, opts shimOptions) bool {
	// A per-invocation flag overrides the effective isolation level for this
	// call only — it is not a blanket bypass. `--standard` downgrades strict to
	// standard, but standard still contains everything that is not your own
	// code (installs, ad-hoc tools); it never uncontains an install.
	effectiveLevel := level
	if opts.strictFlag {
		effectiveLevel = levelStrict
	} else if opts.standardFlag {
		effectiveLevel = levelStandard
	}

	if effectiveLevel == levelStrict {
		return true
	}
	// standard: only code you did not write is contained.
	return class != classYourCode
}
