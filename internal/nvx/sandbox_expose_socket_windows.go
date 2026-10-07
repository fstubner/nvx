//go:build windows

package nvx

import "fmt"

// windowsExposeSocketPath is where the parent listens for tunnels for one port.
// It mirrors windowsEgressSocketPath and sits under the session's socket prefix,
// which already carries the grants the container needs. See windowsSocketPrefix.
func windowsExposeSocketPath(prefix string, port int) string {
	return prefix + fmt.Sprintf("expose-%d.sock", port)
}

// exposeSocketPath is the name sandbox_expose.go gives it. The prefix is the
// session's socket prefix.
func exposeSocketPath(prefix string, port int) string {
	return windowsExposeSocketPath(prefix, port)
}
