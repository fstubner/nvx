package main

import (
	"os"
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

	inHelp := commandsInHelpBlock(helpCommandsSection(helpText()))
	inReadme := commandsInHelpBlock(helpCommandsSection(string(readme)))

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

// Flags documented as leading must actually parse as leading.
//
// The command lists are pinned against each other, but nothing guarded the flag
// block -- so a README rewrite moved `--filesystem-provider` into the
// before-the-command section, where it is not parsed at all and produces
// "Unknown command". Following the documentation verbatim failed. Text parity
// would not have caught it either; what matters is whether the parser agrees
// with the prose, so this asks the parser.
func TestDocumentedLeadingFlagsAreParsedAsLeading(t *testing.T) {
	leading := []string{"--no-sandbox", "--strict", "--standard", "-y", "--yes", "-q", "--quiet", "--agent-mode"}
	for _, flag := range leading {
		args, _, _, _, _ := parseStartupFlags([]string{"nvx", flag, "npm", "install"})
		if len(args) < 2 || args[1] != "npm" {
			t.Errorf("%s is documented as a leading flag but parseStartupFlags did not consume it; "+
				"the command would be read as %q", flag, args)
		}
	}

	// --expose carries a value, in both spellings, and both must consume it. The
	// separated form is the one that can go wrong: a parser that consumes the
	// flag but not its argument leaves "5173" as the command.
	defer func() { exposePortsFlag = nil }()
	for _, form := range [][]string{
		{"nvx", "--expose=5173", "npm", "install"},
		{"nvx", "--expose", "5173", "npm", "install"},
	} {
		exposePortsFlag = nil
		args, _, _, _, _ := parseStartupFlags(form)
		if len(args) < 2 || args[1] != "npm" {
			t.Errorf("%v: --expose did not consume its value; the command would be read as %q", form, args)
		}
		if len(exposePortsFlag) != 1 || exposePortsFlag[0] != "5173" {
			t.Errorf("%v: recorded %v", form, exposePortsFlag)
		}
	}

	// And one that is deliberately NOT leading. If this ever starts parsing as
	// leading, the README section it lives in has to move with it.
	args, _, _, _, _ := parseStartupFlags([]string{"nvx", "--filesystem-provider=native", "npm"})
	if len(args) >= 2 && args[1] == "npm" {
		t.Error("--filesystem-provider now parses as a leading flag; it is documented as belonging " +
			"to the wrapped command, and the docs need updating to match")
	}
}

// None of nvx's three containment flags is read from the wrapped command's own
// arguments. They must all lead.
//
// --no-sandbox and --standard were always ignored there, because they REDUCE
// containment and a dependency's arguments must not weaken the sandbox around
// it. --strict was honoured there until 2026-08-24 on the reasoning that adding
// containment is harmless — true of an attacker, false of a user, since --strict
// is TypeScript's and ESLint's flag and `nvx tsc --strict` was silently sandboxed
// as a result. See TestStrictIsNotReadFromTheProgramsArguments.
func TestNoContainmentFlagIsReadFromTheCommandsOwnArguments(t *testing.T) {
	if shouldContain(classYourCode, levelStandard, shimOptions{payloadStrict: true}) {
		t.Error("--strict among the command's own arguments changed containment; it belongs to the " +
			"program there and must lead to apply to nvx")
	}
	if !shouldContain(classYourCode, levelStrict, shimOptions{payloadStandard: true}) {
		t.Error("--standard among the command's own arguments weakened a strict policy; a dependency's " +
			"arguments must not be able to reduce containment")
	}
	if !shouldContain(classInstall, levelStandard, shimOptions{payloadNoSandbox: true}) {
		t.Error("--no-sandbox among the command's own arguments uncontained an install; that is the " +
			"bypass this rule exists to refuse")
	}
}
