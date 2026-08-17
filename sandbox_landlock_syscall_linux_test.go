//go:build linux

package main

import "testing"

// TestLandlockSyscallNumbersMatchKernelTable pins F24. The arm64 variant returned
// 445/446/447 -- one too high on every entry -- so landlock_restrict_self invoked
// 447, which is memfd_secret. The sandbox failed closed, but the error surfaced as
// "landlock not supported (kernel 5.13+ required)", misdirecting the reader to
// their kernel version rather than to a wrong constant.
//
// Landlock was added to every architecture's syscall table at once with the same
// numbers, so these are NOT per-architecture values. This test compiles and runs
// on every GOARCH, which is what makes it able to catch a per-arch divergence:
// run it under `GOARCH=arm64` and it fails against the old per-arch files.
func TestLandlockSyscallNumbersMatchKernelTable(t *testing.T) {
	// Source of truth: include/uapi/asm-generic/unistd.h (Linux 5.13+), which is
	// also what x86_64's own table uses for these three.
	//   444 landlock_create_ruleset
	//   445 landlock_add_rule
	//   446 landlock_restrict_self
	//   447 memfd_secret   <-- off-by-one lands here
	for _, tc := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"landlock_create_ruleset", landlockSyscallCreateRuleset(), 444},
		{"landlock_add_rule", landlockSyscallAddRule(), 445},
		{"landlock_restrict_self", landlockSyscallRestrictSelf(), 446},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d (447 is memfd_secret, not a landlock call)", tc.name, tc.got, tc.want)
		}
	}
}
