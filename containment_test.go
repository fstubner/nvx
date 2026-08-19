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

// TestStrictIsHonouredInThePayloadPosition covers F75. `nvx node --strict app.js`
// silently ran uncontained: --strict was stripped from the arguments and then
// discarded, so the user got neither the containment they asked for nor an error
// saying they had not got it. `nvx help` shows the flag without saying it must
// lead.
//
// The anti-bypass rule that ignores smuggled flags is right for --no-sandbox and
// --standard, which REDUCE containment. --strict only ever adds it, so there is
// nothing to gain by sneaking it in.
func TestStrictIsHonouredInThePayloadPosition(t *testing.T) {
	opts := parseShimOptions([]string{"--strict", "app.js"})
	if !opts.payloadStrict {
		t.Fatal("--strict was not parsed out of the payload arguments")
	}
	if !shouldContain(classYourCode, levelStandard, opts) {
		t.Error("`nvx node --strict app.js` ran uncontained; --strict in the payload position " +
			"must raise containment, since it can only ever add it")
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
