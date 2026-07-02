//go:build linux && amd64

package main

func landlockSyscallCreateRuleset() uintptr { return 444 }
func landlockSyscallAddRule() uintptr        { return 445 }
func landlockSyscallRestrictSelf() uintptr   { return 446 }
