//go:build linux && !amd64 && !arm64

package main

func seccompSyscall() uintptr { return 0 }

// auditArch is unknown on unsupported arches; 0 signals "no arch guard" and
// installSeccompFilter refuses to run there anyway (seccompSyscall()==0).
func auditArch() uint32 { return 0 }
