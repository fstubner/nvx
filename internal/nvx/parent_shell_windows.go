//go:build windows

package nvx

import (
	"syscall"
	"unsafe"
)

// parentShellExe is a variable so a test can name the parent: a test binary's
// own parent is `go.exe`, and the shell it is really run from cannot be chosen.
var parentShellExe = parentProcessExeName

// parentProcessExeName returns the executable file name of the process that
// started nvx ("cmd.exe"), or "" when it cannot be found.
//
// One snapshot, read once into a map, then looked up twice: this process, then
// its parent. The snapshot types are the ones sandbox_parent_watch_windows.go
// already uses to find the parent id.
func parentProcessExeName() string {
	const th32csSnapProcess = 0x00000002
	snap, _, _ := procCreateToolhelp32Snapshot.Call(uintptr(th32csSnapProcess), 0)
	if snap == uintptr(syscall.InvalidHandle) || snap == 0 {
		return ""
	}
	defer syscall.CloseHandle(syscall.Handle(snap))

	type proc struct {
		parent uint32
		exe    string
	}
	procs := map[uint32]proc{}
	var entry processEntry32W
	entry.Size = uint32(unsafe.Sizeof(entry))
	ret, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ret != 0 {
		procs[entry.ProcessID] = proc{entry.ParentProcessID, syscall.UTF16ToString(entry.ExeFile[:])}
		ret, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	self, ok := procs[uint32(syscall.Getpid())]
	if !ok {
		return ""
	}
	return procs[self.parent].exe
}
