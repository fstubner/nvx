//go:build windows

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var procGetSystemDirectoryW = modKernel32.NewProc("GetSystemDirectoryW")

// systemToolPath returns the absolute path of a Windows system tool, taken
// from the system directory the kernel reports, never from PATH.
//
// rel is the path under that directory: "icacls.exe", or
// `WindowsPowerShell\v1.0\powershell.exe`. A tool that is not there is an
// error, not a reason to search PATH.
//
// PATH is not nvx's. Its user half is written by ordinary user-level code and
// by installers, the shell integration prepends directories to it on every cd,
// and `nvx setup` runs this same binary elevated. exec.Command with a bare
// name resolves through PATH, so setup's `CheckNetIsolation` call was an
// Administrator running whatever a user-writable directory earlier in PATH
// chose to call CheckNetIsolation.exe. Measured unelevated with a planted
// file: it ran and its output was believed.
//
// The directory comes from GetSystemDirectoryW rather than %SystemRoot%
// because an environment variable is one more thing the caller's environment
// supplies; the API answers from the system itself.
func systemToolPath(rel string) (string, error) {
	buf := make([]uint16, syscall.MAX_PATH)
	n, _, callErr := procGetSystemDirectoryW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || n >= uintptr(len(buf)) {
		return "", fmt.Errorf("GetSystemDirectoryW: %v", callErr)
	}
	p := filepath.Join(syscall.UTF16ToString(buf[:n]), rel)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("system tool %s: %w", rel, err)
	}
	return p, nil
}
