//go:build linux || windows

package nvx

import "fmt"

// unixSocketPathMax is the size of sockaddr_un.sun_path. Windows uses the same
// 108-byte field as Linux, and afunix.sys rejects anything longer with
// WSAEINVAL -- which surfaces from Go as "bind: invalid argument", a message
// indistinguishable from a permissions failure. The relay probe hit exactly this
// and it cost a wrong diagnosis, so the length is checked up front and reported
// as what it is. Linux reports the same "bind: invalid argument".
const unixSocketPathMax = 108

// egressSocketPathFits reports whether path can be bound as an AF_UNIX socket.
// One byte is reserved for the terminating NUL.
func egressSocketPathFits(path string) bool {
	return path != "" && len(path) < unixSocketPathMax
}

// nvxHomeAdvice names the longest NVX_HOME that keeps sock inside the limit.
// sock is a path built under nvxHome, so what NVX_HOME does not control is the
// rest of it, and the limit minus that rest is the room NVX_HOME has. With no
// nvxHome to measure against it can only say shorter.
func nvxHomeAdvice(sock, nvxHome string) string {
	if nvxHome == "" {
		return "Set NVX_HOME to a shorter directory"
	}
	maxHome := unixSocketPathMax - 1 - (len(sock) - len(nvxHome))
	return fmt.Sprintf("Set NVX_HOME to a directory of at most %d characters (it is %d)", maxHome, len(nvxHome))
}

// unixSocketPathTooLong returns the error for a socket path that will not bind,
// naming the fix, or nil when it fits. path is under nvxHome.
func unixSocketPathTooLong(what, path, nvxHome string) error {
	if egressSocketPathFits(path) {
		return nil
	}
	return fmt.Errorf("the %s path is %d bytes, over the %d-byte AF_UNIX limit: %s\n%s",
		what, len(path), unixSocketPathMax-1, path, nvxHomeAdvice(path, nvxHome))
}
