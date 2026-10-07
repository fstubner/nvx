//go:build linux

package nvx

import (
	"fmt"
	"os"
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
	//
	// Taken from the system directories, never from PATH: see systemToolPath.
	// cmd.Env below governs only what `ip` itself sees.
	tool, err := systemToolPath("ip")
	if err != nil {
		return fmt.Errorf("bring up loopback (install iproute2): %v", err)
	}
	ip := exec.Command(tool, "link", "set", "lo", "up")
	ip.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	if out, err := ip.CombinedOutput(); err != nil {
		return fmt.Errorf("bring up loopback (install iproute2): %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// unprivilegedPortStartPath is the lowest port a process without
// CAP_NET_BIND_SERVICE may bind in the caller's network namespace. It covers
// IPv6 as well, and a new namespace starts at 1024.
const unprivilegedPortStartPath = "/proc/sys/net/ipv4/ip_unprivileged_port_start"

// allowLowPortsInNamespace lets the target listen on any port in its own network
// namespace. As root in its user namespace it could, and running as the user
// it could not listen below 1024. The namespace holds only its own loopback,
// so a low port there reaches nothing a high one does not. Docker sets the same
// value for its containers, 0 as read from one on 2026-10-07.
func allowLowPortsInNamespace() error {
	return os.WriteFile(unprivilegedPortStartPath, []byte("0"), 0)
}

// networkModeRequiresNamespace reports whether mode needs a loopback-only
// network namespace.
//
// TrimSpace as well as ToLower. normalizePolicy guarantees a canonical value,
// but this is the reader that turns the guarantee into an OS boundary: it read
// `strings.ToLower(mode)` alone, and a policy asking for "offline " with a
// trailing space fell through to default and got no namespace at all.
//
// Only "open" goes without. The default arm was the open one until 2026-09-26,
// so an empty or unrecognised mode meant no containment, while Windows treats
// the same input as proxy (windowsEgressNeedsRelay).
func networkModeRequiresNamespace(mode string) bool {
	return strings.ToLower(strings.TrimSpace(mode)) != "open"
}
