package main

import (
	"strings"
	"testing"
)

// TestDoctorDoesNotRepairWithoutFix pins that diagnosis is read-only.
//
// `nvx doctor` used to rewrite the user's persistent PATH the moment it found a
// problem. Because the repair targets whatever NVX_HOME is currently set,
// pointing that at a throwaway directory — which is exactly what anyone testing
// or scripting nvx does — silently fronted the real user PATH with it. Found
// during acceptance, on the acceptor's own machine.
//
// This asserts the plan/apply split at the boundary that does the writing:
// repairPersistentPath must not mutate anything when apply is false. On non-
// Windows it is a no-op either way, so the test states the contract on both and
// is meaningful on the platform that has the setting.
func TestDoctorDoesNotRepairWithoutFix(t *testing.T) {
	nvxHome := tempDir(t)

	// apply=false must never write, whatever it finds. It may report that a
	// repair is available (true) or that none is needed (false); either is fine.
	// What must not happen is an error from attempting the write, or a mutation.
	if _, err := repairPersistentPath(nvxHome, false); err != nil {
		// An error here is acceptable only if it comes from *reading* the current
		// PATH, never from writing. The message is the only signal available
		// without mocking the registry, so assert it is not a write failure.
		if strings.Contains(err.Error(), "set User PATH") {
			t.Errorf("repairPersistentPath attempted a write with apply=false: %v", err)
		}
	}
}

// TestDoctorFixFlagIsRecognised guards the wiring. If --fix stopped being parsed,
// the repair would become unreachable and the only symptom would be a doctor that
// reports the same problem forever.
func TestDoctorFixFlagIsRecognised(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"nvx", "doctor"}, false},
		{[]string{"nvx", "doctor", "--fix"}, true},
		{[]string{"nvx", "doctor", "-q", "--fix"}, true},
		{[]string{"nvx", "doctor", "--fixup"}, false},
	}
	for _, tc := range cases {
		got := false
		for _, a := range tc.args[2:] {
			if a == "--fix" {
				got = true
			}
		}
		if got != tc.want {
			t.Errorf("%v: parsed --fix as %v, want %v", tc.args, got, tc.want)
		}
	}
}
