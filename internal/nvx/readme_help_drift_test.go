package nvx

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The README's command list matches what `nvx help` prints.
//
// Both are hand-maintained, and the README's copy fell behind: a reader
// following it asked for a command with flags nvx no longer took, or missed one
// that existed. Nothing catches that -- documentation renders perfectly while
// being wrong, and the only reader who would notice is the one it misleads.
// This compares the two lists mechanically, so the next command added in one
// place fails here rather than in someone's terminal.
func TestTheREADMECommandListMatchesNvxHelp(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	fromHelp := commandsInBlock(helpText())
	fromREADME := commandsInBlock(string(readme))

	if len(fromHelp) == 0 || len(fromREADME) == 0 {
		t.Fatalf("could not find a command list to compare (help=%d, README=%d); the block's shape changed", len(fromHelp), len(fromREADME))
	}
	if strings.Join(fromHelp, " ") != strings.Join(fromREADME, " ") {
		t.Fatalf("the README's command list and `nvx help` disagree.\nonly in help:   %v\nonly in README: %v",
			missingFrom(fromHelp, fromREADME), missingFrom(fromREADME, fromHelp))
	}
}

// commandsInBlock returns the command names listed under a "Commands:" heading,
// up to the blank line that ends the block.
var commandLine = regexp.MustCompile(`^  ([a-z][a-z-]*(?:, [a-z-]+)?)(?: |$)`)

func commandsInBlock(text string) []string {
	var names []string
	inBlock := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "Commands:") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if m := commandLine.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	sort.Strings(names)
	return names
}

func missingFrom(have, other []string) []string {
	seen := map[string]bool{}
	for _, s := range other {
		seen[s] = true
	}
	var out []string
	for _, s := range have {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out
}
