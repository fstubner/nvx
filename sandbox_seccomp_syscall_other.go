//go:build linux && !amd64 && !arm64

package main

func seccompSyscall() uintptr { return 0 }
