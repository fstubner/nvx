//go:build linux

package nvx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A guest home deep enough that its socket paths pass the 108-byte AF_UNIX
// field used to fail with a bare "bind: invalid argument", which reads like a
// permissions problem. Windows already said what it was and what to do.
func TestALongSocketPathSaysToShortenNvxHome(t *testing.T) {
	guestHome := filepath.Join(tempDir(t), strings.Repeat("d", 120))

	proxy, err := startEgressProxy(context.Background(), DefaultPolicy(), Providers["node"], tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	err = prepareEgressSocket(proxy, guestHome, &NetworkLaunchContext{Mode: "proxy"})
	if err == nil || !strings.Contains(err.Error(), "NVX_HOME") {
		t.Errorf("egress socket: got %v, want an error naming NVX_HOME", err)
	}

	netCtx := NetworkLaunchContext{Mode: "proxy", ConnectPorts: []connectMapping{{Host: 9222}}}
	_, stop, err := openConnectSockets(guestHome, &netCtx)
	stop()
	if err == nil || !strings.Contains(err.Error(), "NVX_HOME") {
		t.Errorf("tunnel socket: got %v, want an error naming NVX_HOME", err)
	}
}
