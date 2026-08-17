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
