//go:build linux && !amd64 && !arm64

package main

func prctlSyscall() uintptr { return 157 }
