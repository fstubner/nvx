//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// setupLoopbackNetworkNamespace moves the process into a new network namespace
// with only loopback available. Non-loopback egress (TCP and UDP) fails at the
// routing layer, complementing the parent egress proxy on 127.0.0.1.
func setupLoopbackNetworkNamespace() error {
	if err := syscall.Unshare(syscall.CLONE_NEWNET); err != nil {
		return fmt.Errorf("network namespace unshare: %w", err)
	}

	// Loopback exists in a new netns but is down by default.
	ip := exec.Command("ip", "link", "set", "lo", "up")
	ip.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	if out, err := ip.CombinedOutput(); err != nil {
		return fmt.Errorf("bring up loopback (install iproute2): %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func networkModeRequiresNamespace(mode string) bool {
	switch strings.ToLower(mode) {
	case "proxy", "offline", "loopback":
		return true
	default:
		return false
	}
}
