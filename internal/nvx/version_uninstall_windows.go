//go:build windows

package nvx

import (
	"syscall"
	"unsafe"
)

var procQueryFullProcessImageNameW = modKernel32.NewProc("QueryFullProcessImageNameW")

// processesRunningFrom lists the processes whose executable lives under dir.
//
// Asked of the processes rather than of the files, so the refusal can say
// which process to stop. A process this user may not query is not listed. If
// one of those is running from dir, the delete stops on its file, and the
// rename in removeInstalledVersion keeps that outcome consistent.
func processesRunningFrom(dir string) []runningProcess {
	const th32csSnapProcess = 0x00000002
	const processQueryLimitedInformation = 0x1000
	snap, _, _ := procCreateToolhelp32Snapshot.Call(uintptr(th32csSnapProcess), 0)
	if snap == uintptr(syscall.InvalidHandle) || snap == 0 {
		return nil
	}
	defer syscall.CloseHandle(syscall.Handle(snap))

	var found []runningProcess
	var entry processEntry32W
	entry.Size = uint32(unsafe.Sizeof(entry))
	ret, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ; ret != 0; ret, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry))) {
		h, _, _ := procOpenProcessForJob.Call(processQueryLimitedInformation, 0, uintptr(entry.ProcessID))
		if h == 0 {
			continue
		}
		buf := make([]uint16, syscall.MAX_LONG_PATH)
		size := uint32(len(buf))
		ok, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		syscall.CloseHandle(syscall.Handle(h))
		if ok == 0 {
			continue
		}
		if image := syscall.UTF16ToString(buf[:size]); isPathStrictlyUnder(image, dir) {
			found = append(found, runningProcess{Name: image, PID: entry.ProcessID})
		}
	}
	return found
}
