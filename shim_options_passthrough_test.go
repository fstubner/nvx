package main

import (
	"strings"
	"testing"
)

// TestShimOptionsDoNotRewriteTheProgramsArguments is the regression guard for a
// defect that produced wrong answers with no error message.
//
// nvx read its own flags out of a wrapped command's arguments and REMOVED them.
// The names are not nvx's to take: --strict belongs to TypeScript and ESLint,
// --no-sandbox to Chromium and everything embedding it. Measured on 2026-08-24
// against the shipped binary:
//
//	nvx npx tsc --strict          -> tsc ran without --strict, reporting a clean
//	                                 non-strict typecheck as a strict one
//	nvx npx electron --no-sandbox -> the flag never reached electron
//	nvx node app.js -- --strict   -> stripped past the end-of-options separator
//	nvx node app.js --filesystem-provider x keep -> "x" consumed as its value
//
// It applied to uncontained runs too, where nvx has no security interest at all.
//
// The anti-bypass rule is unaffected and is asserted separately below: nvx still
// NOTICES a weakening flag in this position and still refuses to honour it. What
// changed is that noticing no longer means confiscating.
func TestShimOptionsDoNotRewriteTheProgramsArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a flag TypeScript owns", []string{"tsc", "--strict", "src"}},
		{"a flag Chromium owns", []string{"electron", "--no-sandbox", "main.js"}},
		{"--standard", []string{"app.js", "--standard"}},
		{"past the end-of-options separator", []string{"app.js", "--", "--strict", "keep"}},
		{"a bare provider flag and the argument after it", []string{"app.js", "--filesystem-provider", "notes.txt", "keep"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := parseShimOptions(tc.args)
			if len(opts.args) != len(tc.args) {
				t.Fatalf("nvx changed the program's arguments\n got: %q\nwant: %q", opts.args, tc.args)
			}
			for i := range tc.args {
				if opts.args[i] != tc.args[i] {
					t.Fatalf("argument %d changed: got %q, want %q\n got: %q\nwant: %q",
						i, opts.args[i], tc.args[i], opts.args, tc.args)
				}
			}
		})
	}
}

// TestShimOptionsStillNoticeSmuggledFlags: passing them through must not stop nvx
// seeing them. shouldContain reads these fields to refuse a weakening flag that
// arrived through a package manager's arguments, and that is the rule the
// stripping was really there to serve.
func TestShimOptionsStillNoticeSmuggledFlags(t *testing.T) {
	opts := parseShimOptions([]string{"install", "--no-sandbox", "--standard"})
	if !opts.payloadNoSandbox {
		t.Error("a smuggled --no-sandbox must still be noticed, or the anti-bypass rule cannot refuse it")
	}
	if !opts.payloadStandard {
		t.Error("a smuggled --standard must still be noticed")
	}

	// And NOT noticed after the separator: everything there belongs to the
	// program, so nvx must not act on it either.
	after := parseShimOptions([]string{"app.js", "--", "--no-sandbox"})
	if after.payloadNoSandbox {
		t.Error("a --no-sandbox after -- is the program's argument; nvx must not read it as its own")
	}
}

// TestShimOptionsReadOnlyTheAttachedProviderForm pins why the separated spelling
// is gone. Finding its value meant consuming the next argument, and that argument
// belongs to the program -- the most damaging form of the bug above, since it
// removed a filename rather than a flag.
func TestShimOptionsReadOnlyTheAttachedProviderForm(t *testing.T) {
	attached := parseShimOptions([]string{"install", "--filesystem-provider=docker"})
	if attached.filesystemProvider != "docker" {
		t.Errorf("the documented spelling must still work: got %q", attached.filesystemProvider)
	}

	bare := parseShimOptions([]string{"app.js", "--filesystem-provider", "docker"})
	if bare.filesystemProvider != "" {
		t.Errorf("a separated --filesystem-provider must not claim the next argument: got %q", bare.filesystemProvider)
	}
	if !bare.payloadBareProvider {
		t.Error("a separated --filesystem-provider must be recorded, so the user is told why it did nothing")
	}
}

// TestStrictIsHonouredFromTheProgramsArgumentsAndStillReachesIt covers the one
// flag deliberately honoured in this position. --strict only ever ADDS
// containment, so there is nothing to gain by smuggling it -- and the program
// must receive it too, because `tsc --strict` means something to tsc.
func TestStrictIsHonouredFromTheProgramsArgumentsAndStillReachesIt(t *testing.T) {
	opts := parseShimOptions([]string{"tsc", "--strict"})
	if !opts.payloadStrict {
		t.Error("--strict among the program's arguments must still be honoured by nvx")
	}
	if !strings.Contains(strings.Join(opts.args, " "), "--strict") {
		t.Errorf("...and must still reach the program: %q", opts.args)
	}
}
