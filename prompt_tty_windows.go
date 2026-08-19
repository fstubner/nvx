//go:build windows

package main

import "syscall"

// stdinIsInteractive reports whether standard input is a console a person could
// be typing at.
//
// GetConsoleMode is the test, not a character-device check: NUL is itself a
// character device on Windows, so `< /dev/null` -- the exact shape a CI step or
// an agent harness uses -- looked interactive under the obvious check, and the
// prompt went on to read from a console nobody was watching. GetConsoleMode
// succeeds only for a real console handle and fails for a pipe, a file, or NUL.
func stdinIsInteractive() bool {
	handle, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil || handle == 0 || handle == syscall.InvalidHandle {
		return false
	}
	var mode uint32
	return syscall.GetConsoleMode(handle, &mode) == nil
}
