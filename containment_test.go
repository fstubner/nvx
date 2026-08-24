package main

import "testing"

func TestParseIsolationLevel(t *testing.T) {
	tests := []struct {
		in     string
		want   isolationLevel
		wantOK bool
	}{
		{"standard", levelStandard, true},
		{"Standard", levelStandard, true},
		{"", levelStandard, true},
		{"strict", levelStrict, true},
		{"STRICT", levelStrict, true},
		{"bogus", levelStandard, false},
	}
	for _, tc := range tests {
		got, ok := parseIsolationLevel(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseIsolationLevel(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestShouldContain(t *testing.T) {
	tests := []struct {
		name  string
		class invocationClass
		level isolationLevel
		opts  shimOptions
		want  bool
	}{
		{"standard your-code not contained", classYourCode, levelStandard, shimOptions{}, false},
		{"standard install contained", classInstall, levelStandard, shimOptions{}, true},
		{"standard ad-hoc-tool contained", classAdHocTool, levelStandard, shimOptions{}, true},
		{"strict your-code contained", classYourCode, levelStrict, shimOptions{}, true},
		{"strict install contained", classInstall, levelStrict, shimOptions{}, true},
		{"strict ad-hoc-tool contained", classAdHocTool, levelStrict, shimOptions{}, true},
		{"per-command --strict overrides standard level", classYourCode, levelStandard, shimOptions{strictFlag: true}, true},
		// --standard downgrades the effective level from strict to standard for
		// this call, but standard still contains installs — it must never act
		// as a blanket bypass for code you did not write.
		{"per-command --standard downgrades level but still contains installs", classInstall, levelStrict, shimOptions{standardFlag: true}, true},
		{"per-command --standard leaves your own code uncontained", classYourCode, levelStrict, shimOptions{standardFlag: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldContain(tc.class, tc.level, tc.opts)
			if got != tc.want {
				t.Errorf("shouldContain(%v, %v, %+v) = %v, want %v", tc.class, tc.level, tc.opts, got, tc.want)
			}
		})
	}
}

// TestStrictIsNotReadFromTheProgramsArguments reverses an earlier decision, and
// the reason is worth keeping.
//
// --strict used to be honoured wherever it appeared, on the reasoning that it
// only ever ADDS containment so there was nothing to gain by smuggling it. That
// reasoning was about an attacker and forgot the ordinary user: --strict is
// TypeScript's most-used flag, and ESLint's. `nvx tsc --strict` means "typecheck
// strictly" and nvx read it as "sandbox this", quietly moving the command into a
// container where writes outside the project are redirected to a throwaway home
// -- and on Windows such a write REPORTS SUCCESS (docs/enforcement-matrix.md), so
// a build appears to work and produces nothing.
//
// It is the same defect as nvx removing --strict from a program's arguments,
// which was fixed alongside this: both treat a word that belongs to other tools
// as nvx's own wherever it is found. Now it must lead, like --no-sandbox and
// --standard, and the flag is still NOTICED so the user can be told why nothing
// happened.
func TestStrictIsNotReadFromTheProgramsArguments(t *testing.T) {
	opts := parseShimOptions([]string{"--strict", "app.js"})
	if !opts.payloadStrict {
		t.Fatal("--strict must still be noticed, so the user can be told it did not apply")
	}
	if shouldContain(classYourCode, levelStandard, opts) {
		t.Error("`nvx node app.js --strict` was contained; --strict is the program's flag there " +
			"and must not silently change how nvx runs it")
	}

	// Leading --strict is the supported spelling and still works.
	leading := shimOptions{strictFlag: true}
	if !shouldContain(classYourCode, levelStandard, leading) {
		t.Error("`nvx --strict node app.js` did not contain; the leading flag is the one that must work")
	}
}

// TestStandardIsStillIgnoredInThePayloadPosition is the other half. --standard
// lowers containment relative to a strict policy, so honouring it from inside a
// package's own arguments would let a dependency's script argument weaken the
// sandbox around itself.
func TestStandardIsStillIgnoredInThePayloadPosition(t *testing.T) {
	opts := parseShimOptions([]string{"--standard", "app.js"})
	if !opts.payloadStandard {
		t.Fatal("--standard was not parsed out of the payload arguments")
	}
	if !shouldContain(classYourCode, levelStrict, opts) {
		t.Error("--standard in the payload position downgraded a strict policy; a smuggled flag " +
			"must never reduce containment")
	}
}
