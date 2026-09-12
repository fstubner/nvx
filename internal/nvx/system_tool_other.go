//go:build !windows

package nvx

import "fmt"

// systemToolPath resolves Windows system tools; see system_tool_windows.go.
// Nothing on another OS should reach it, and if something does the answer is
// an error rather than a name for PATH to resolve.
func systemToolPath(rel string) (string, error) {
	return "", fmt.Errorf("system tool %s: not on Windows", rel)
}
