//go:build !windows && !linux

package nvx

import "fmt"

// systemToolPath resolves Windows and Linux system tools; see
// system_tool_windows.go and system_tool_linux.go. Nothing on another OS
// should reach it, and if something does the answer is an error rather than a
// name for PATH to resolve.
func systemToolPath(rel string) (string, error) {
	return "", fmt.Errorf("system tool %s: not resolved on this OS", rel)
}
