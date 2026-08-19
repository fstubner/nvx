//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// stdinIsInteractive reports whether standard input is a terminal.
//
// The TCGETS ioctl is the test rather than a character-device check, because
// /dev/null is a character device too and redirecting from it must read as
// non-interactive.
func stdinIsInteractive() bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		os.Stdin.Fd(),
		syscall.TCGETS,
		uintptr(unsafe.Pointer(&termios)),
		0, 0, 0,
	)
	return errno == 0
}
