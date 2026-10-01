//go:build windows

package nvx

import (
	"os"
	"syscall"
)

var procSetStdHandle = modKernel32.NewProc("SetStdHandle")

// STD_OUTPUT_HANDLE, as SetStdHandle takes it: (DWORD)-11.
const stdOutputHandle = uint32(0xFFFFFFF5)

// quietStdout points this process's stdout, and so a child's that is started
// with it, at the null device until restore is called.
//
// The process's standard handle changes as well as os.Stdout: the AppContainer
// launcher hands the child GetStdHandle(STD_OUTPUT_HANDLE), not os.Stdout.
func quietStdout() (restore func()) {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return func() {}
	}
	prevHandle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		_ = null.Close()
		return func() {}
	}
	if ret, _, _ := procSetStdHandle.Call(uintptr(stdOutputHandle), null.Fd()); ret == 0 {
		_ = null.Close()
		return func() {}
	}
	prev := os.Stdout
	os.Stdout = null
	return func() {
		_, _, _ = procSetStdHandle.Call(uintptr(stdOutputHandle), uintptr(prevHandle))
		os.Stdout = prev
		_ = null.Close()
	}
}
