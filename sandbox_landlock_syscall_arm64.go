//go:build linux && arm64

package main

// arm64 uses the asm-generic syscall table, identical to amd64 for Landlock:
// create_ruleset=444, add_rule=445, restrict_self=446.
func landlockSyscallCreateRuleset() uintptr { return 444 }
func landlockSyscallAddRule() uintptr       { return 445 }
func landlockSyscallRestrictSelf() uintptr  { return 446 }
