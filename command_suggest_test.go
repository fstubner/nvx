package main

import (
	"strings"
	"testing"
)

// A mistyped command should name the one it meant.
//
// nvx computes edit distance for a misspelt policy key and for a typosquatted
// package name, and did not for its own commands: `nvx instal 20` answered
// "Unknown command: instal" and printed the whole help, leaving a newcomer to
// spot the one line differing by a letter among twenty-odd.
func TestAMistypedCommandNamesTheOneItMeant(t *testing.T) {
	for _, tc := range []struct{ typo, want string }{
		{"instal", "install"},
		{"ninstall", "install"},
		{"usse", "use"},
		{"lst", "list"},
		{"doctro", "doctor"},
		{"grnats", "grants"},
		{"uninstal", "uninstall"},
	} {
		if got := nearestCommand(tc.typo); got != tc.want {
			t.Errorf("nearestCommand(%q) = %q, want %q", tc.typo, got, tc.want)
		}
	}
}

// Something that is not a typo gets no guess. A wrong suggestion is worse than
// none: it sends the reader to a command that does something else.
func TestNonsenseGetsNoSuggestion(t *testing.T) {
	for _, in := range []string{"totallybogus", "", "   ", "xyzzy", "deploy"} {
		if got := nearestCommand(in); got != "" {
			t.Errorf("nearestCommand(%q) = %q, want no suggestion", in, got)
		}
	}
}

// The candidates come from the help text, which is the one place the command
// names are written down. If the extractor breaks, suggestions silently stop
// happening and nothing else notices -- so assert it finds a real list.
func TestTheSuggesterReadsTheRealCommandList(t *testing.T) {
	names := commandNamesFromHelp()
	if len(names) < 10 {
		t.Fatalf("only %d commands found in the help text (%v); the extractor is broken and "+
			"every suggestion would silently stop", len(names), names)
	}
	for _, want := range []string{"install", "use", "doctor", "list"} {
		found := false
		for _, n := range names {
			if strings.Fields(n)[0] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q is missing from the extracted command list %v", want, names)
		}
	}
}
