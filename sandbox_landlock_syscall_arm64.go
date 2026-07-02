//go:build linux && arm64

package main

func landlockSyscallCreateRuleset() uintptr { return 445 }
func landlockSyscallAddRule() uintptr        { return 446 }
func landlockSyscallRestrictSelf() uintptr   { return 447 }
