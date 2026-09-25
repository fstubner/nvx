//go:build linux

package nvx

import (
	"context"
	"testing"
)

// Offline gets no way to the proxy, as on Windows. The socket used to be
// offered in every namespaced mode, offline included, so a contained process
// that got past seccomp's refusal of connect() had a route to allowlisted hosts.
func TestOfflineModeIsNotOfferedTheEgressSocket(t *testing.T) {
	policy := DefaultPolicy()
	proxy, err := startEgressProxy(context.Background(), policy, Providers["node"], tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	for mode, want := range map[string]bool{"offline": false, " Offline ": false, "proxy": true, "loopback": true} {
		netCtx := &NetworkLaunchContext{Mode: mode}
		if err := prepareEgressSocket(proxy, tempDir(t), netCtx); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if got := netCtx.EgressSocketPath != ""; got != want {
			t.Errorf("mode %q: egress socket offered = %v, want %v", mode, got, want)
		}
	}
}
