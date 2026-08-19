//go:build darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// stdinIsInteractive reports whether standard input is a terminal. See the Linux
// twin for why this is an ioctl rather than a character-device check; macOS
// spells the request TIOCGETA.
func stdinIsInteractive() bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		os.Stdin.Fd(),
		syscall.TIOCGETA,
		uintptr(unsafe.Pointer(&termios)),
		0, 0, 0,
	)
	return errno == 0
}
