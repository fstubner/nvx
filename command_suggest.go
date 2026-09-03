package main

import (
	"sort"
	"strings"
)

// nearestCommand returns the command a mistyped one most likely meant, or "".
//
// nvx already computes edit distance twice -- for a misspelt policy key and for
// a typosquatted package name -- and did not for its own commands. `nvx instal
// 20` answered "Unknown command: instal" and printed the whole help, leaving a
// newcomer to find the one line that differs by a letter among twenty-odd.
//
// The candidates come from the help text rather than a second hand-maintained
// list. This project has already been bitten by a list of names kept alongside
// the thing it describes: a CI -run filter drifted until 31 of 41 probes matched
// nothing and ran nowhere. helpText() is the list, and help_readme_parity_test
// already holds it to the README.
func nearestCommand(unknown string) string {
	unknown = strings.ToLower(strings.TrimSpace(unknown))
	if unknown == "" {
		return ""
	}
	best, bestDist := "", 0
	for _, candidate := range commandNamesFromHelp() {
		// Only the first word: "grants list" is reached by typing "grants".
		name := strings.Fields(candidate)[0]
		d := LevenshteinDistance(unknown, name)
		// Never suggest something further away than a typo plausibly is, and never
		// one shorter than the distance itself -- every short word is close to
		// every other. Same bounds as the policy-key suggestion.
		if d == 0 || d > 2 || d >= len(unknown) {
			continue
		}
		if best == "" || d < bestDist {
			best, bestDist = name, d
		}
	}
	return best
}

// commandNamesFromHelp reads the command names out of the help text, which is
// the single place they are written down.
func commandNamesFromHelp() []string {
	return commandsInHelpBlock(helpCommandsSection(helpText()))
}

// commandsSection returns the lines after a "Commands:" heading, up to the next
// blank-line-separated heading.
func helpCommandsSection(text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	collecting := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Commands:" {
			collecting = true
			continue
		}
		if !collecting {
			continue
		}
		// A new heading ("Options:", "Isolation flags ...") ends the block, as
		// does the end of the fenced code block in the README.
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			break
		}
		out = append(out, line)
	}
	return out
}

// commandsInBlock pulls the command names out of a two-column block. A wrapped
// description line is indented further than a command line, and is skipped.
func commandsInHelpBlock(lines []string) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent != 2 {
			continue // a continuation of the previous command's description
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		// "list, ls" and "version, -v" name one command by several spellings.
		name = strings.TrimSuffix(name, ",")
		// A subcommand is part of the name: "grants list" and "grants reset" are
		// separate entries and must both be documented.
		if len(fields) > 1 && isPlainWord(fields[1]) {
			name += " " + fields[1]
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// isPlainWord reports whether a token is a bare word rather than a placeholder
// ("<pkgs>"), a flag ("[--fix]") or prose.
func isPlainWord(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") {
		return false // "version, -v" is one command spelled two ways, not "version -v"
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && r != '-' {
			return false
		}
	}
	return true
}
