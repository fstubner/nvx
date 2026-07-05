package main

import "testing"

func TestParseAndCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"18", "18.0.0", 0},
		{"1.2.3-rc.1", "1.2.3", -1},      // prerelease < release
		{"1.2.3-rc.1", "1.2.3-rc.2", -1}, // numeric prerelease ordering
		{"1.2.3-rc.2", "1.2.3-rc.10", -1},
		{"1.2.3-alpha", "1.2.3-beta", -1}, // lexical
		{"1.2.3-rc.1", "1.2.3-rc.1.1", -1},
	}
	for _, c := range cases {
		a, err := parseSemver(c.a)
		if err != nil {
			t.Fatalf("parse %q: %v", c.a, err)
		}
		b, err := parseSemver(c.b)
		if err != nil {
			t.Fatalf("parse %q: %v", c.b, err)
		}
		if got := compareSemver(a, b); got != c.want {
			t.Errorf("compare(%s,%s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSemverSatisfies(t *testing.T) {
	cases := []struct {
		version, rng string
		want         bool
	}{
		{"18.16.0", "^18.0.0", true},
		{"19.0.0", "^18.0.0", false},
		{"18.16.5", "~18.16.0", true},
		{"18.17.0", "~18.16.0", false},
		{"18.16.0", ">=18 <21", true},
		{"21.0.0", ">=18 <21", false},
		{"20.5.0", "18.x || 20.x", true},
		{"19.0.0", "18.x || 20.x", false},
		{"18.16.0", "18", true},
		{"18.16.0", "18.16.x", true},
		{"18.16.0", "*", true},
		{"0.2.5", "^0.2.3", true}, // caret on 0.x locks minor
		{"0.3.0", "^0.2.3", false},
	}
	for _, c := range cases {
		if got := semverSatisfies(c.version, c.rng); got != c.want {
			t.Errorf("satisfies(%q,%q) = %v, want %v", c.version, c.rng, got, c.want)
		}
	}
}

func TestMaxSatisfyingVersion(t *testing.T) {
	versions := []string{"v18.14.0", "v18.16.0", "v18.16.1", "v20.1.0", "v20.5.0"}
	if got := maxSatisfyingVersion(versions, "^18"); got != "v18.16.1" {
		t.Errorf("maxSatisfying ^18 = %q, want v18.16.1", got)
	}
	if got := maxSatisfyingVersion(versions, ">=18 <20"); got != "v18.16.1" {
		t.Errorf("maxSatisfying >=18 <20 = %q, want v18.16.1", got)
	}
	if got := maxSatisfyingVersion(versions, "20.x"); got != "v20.5.0" {
		t.Errorf("maxSatisfying 20.x = %q, want v20.5.0", got)
	}
	if got := maxSatisfyingVersion(versions, "^19"); got != "" {
		t.Errorf("maxSatisfying ^19 = %q, want empty", got)
	}
}

func TestCompareVersionsPrerelease(t *testing.T) {
	// The main comparator must now respect prerelease precedence.
	if CompareVersions("v20.0.0-rc.1", "v20.0.0") >= 0 {
		t.Error("rc.1 should be less than final release")
	}
	if CompareVersions("v20.0.0-rc.2", "v20.0.0-rc.1") <= 0 {
		t.Error("rc.2 should be greater than rc.1")
	}
}
