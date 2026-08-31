//go:build linux

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// bringUpLoopback enables loopback inside the caller's network namespace, which
// is created for the sandbox process at clone time (CLONE_NEWNET in
// platformLaunchNative) and starts with loopback down.
//
// This deliberately does NOT unshare. unshare(CLONE_NEWNET) moves only the
// calling thread, and Go schedules goroutines across threads freely, so a
// self-unsharing process keeps some threads in the original namespace -- measured
// at 52 of 64 goroutines, one of which reached the public internet. Requesting the
// namespace as a clone flag covers the whole process from birth instead.
func bringUpLoopback() error {
	// Loopback exists in a new netns but is down by default.
	ip := exec.Command("ip", "link", "set", "lo", "up")
	ip.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	if out, err := ip.CombinedOutput(); err != nil {
		return fmt.Errorf("bring up loopback (install iproute2): %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// networkModeRequiresNamespace reports whether mode needs a loopback-only
// network namespace.
//
// TrimSpace as well as ToLower, and the default arm is the open one, so an
// unrecognised string here means no containment. normalizePolicy now guarantees a
// canonical value, but this is the reader that turns the guarantee into an OS
// boundary: it read `strings.ToLower(mode)` alone, and a policy asking for
// "offline " with a trailing space fell through to default and got no namespace
// at all. Trimming here costs nothing and removes the dependency on every caller
// having normalised first.
func networkModeRequiresNamespace(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "proxy", "offline", "loopback":
		return true
	default:
		return false
	}
}
