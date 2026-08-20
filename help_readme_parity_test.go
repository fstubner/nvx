package main

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// The command list exists twice -- in `nvx help` and in README.md's CLI Usage
// block -- and drifts silently.
//
// It had already drifted when this test was written: the README was missing
// doctor, grants, import, setup and shim, some of them for several releases.
// Nothing fails when documentation is wrong, nobody re-reads a list they wrote,
// and the reader who notices is the one who concluded the command does not
// exist.
//
// Pinning the two lists against each other rather than against the dispatch
// switch: the switch also holds internal entries (__landlock-exec) that are
// deliberately undocumented, so "documented in both places" is the property
// worth having, and it is the one that broke.
func TestHelpAndReadmeListTheSameCommands(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}

	inHelp := commandsInBlock(commandsSection(helpText()))
	inReadme := commandsInBlock(commandsSection(string(readme)))

	if len(inHelp) == 0 || len(inReadme) == 0 {
		t.Fatalf("could not find a Commands: block (help=%d, readme=%d) -- the extractor "+
			"needs updating, not the docs", len(inHelp), len(inReadme))
	}

	if missing := difference(inHelp, inReadme); len(missing) > 0 {
		t.Errorf("commands in `nvx help` but not in README.md: %v", missing)
	}
	if extra := difference(inReadme, inHelp); len(extra) > 0 {
		t.Errorf("commands in README.md but not in `nvx help`: %v", extra)
	}
}

// commandsSection returns the lines after a "Commands:" heading, up to the next
// blank-line-separated heading.
func commandsSection(text string) []string {
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
func commandsInBlock(lines []string) []string {
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

func difference(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}
