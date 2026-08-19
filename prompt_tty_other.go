//go:build !windows && !linux && !darwin

package main

import "os"

// stdinIsInteractive falls back to a character-device check on platforms without
// a per-OS terminal probe here. Slightly permissive -- /dev/null passes -- but
// this is the same tier that gets no OS isolation at all (see
// sandbox_native_other.go), so it is not a platform nvx claims to protect.
func stdinIsInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
