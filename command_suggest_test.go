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

// Every command the quickstart tells a newcomer to type must exist.
//
// The quickstart is the first screen after installing, so a line that does not
// work is the worst place to have one. A draft of it said `nvx install` with no
// arguments reads .nvmrc; it does not -- it answers "Please specify a version"
// -- and `nvx auto` is the command that does. That particular error is not
// catchable here, since `install` is a real command and only the description
// was wrong, but a command that stops existing is.
func TestTheQuickstartOnlyNamesRealCommands(t *testing.T) {
	known := map[string]bool{}
	for _, name := range commandNamesFromHelp() {
		known[strings.Fields(name)[0]] = true
	}
	if len(known) < 10 {
		t.Fatalf("could not read the command list; the guard would pass vacuously")
	}

	for _, line := range strings.Split(quickstartText(), "\n") {
		// Indented lines only: the title line is "nvx - a runtime version
		// manager ...", and reading that as a command suggested `nvx -`.
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nvx" {
			continue
		}
		cmd := fields[1]
		if strings.HasPrefix(cmd, "<") {
			continue
		}
		if !known[cmd] {
			t.Errorf("the quickstart tells a new user to run 'nvx %s', which is not a command "+
				"nvx has. This is the first screen after installing.", cmd)
		}
	}
}
