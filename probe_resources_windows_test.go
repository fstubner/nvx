//go:build windows

package main

import (
	"fmt"
	"unsafe"
)

// memoryStatusEx mirrors MEMORYSTATUSEX. Commit is the field that matters here:
// the failures this file explains were commit-limit failures on a machine with
// gigabytes of free physical memory, so reporting only physical RAM would have
// made the run look healthy at the moment it was refusing to allocate.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// hostMemoryNote returns the machine's commit headroom, phrased for a test
// failure message. Empty if it cannot be read -- a diagnostic that fails is not
// worth failing a test over.
func hostMemoryNote() string {
	proc := modKernel32.NewProc("GlobalMemoryStatusEx")
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	if ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&st))); ret == 0 {
		return ""
	}
	const mb = 1024 * 1024
	return fmt.Sprintf(" Host at this moment: commit %d/%dMB used, %dMB free; physical %dMB free.",
		(st.TotalPageFile-st.AvailPageFile)/mb, st.TotalPageFile/mb, st.AvailPageFile/mb, st.AvailPhys/mb)
}
