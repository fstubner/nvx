//go:build linux

package nvx

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A guest home deep enough that its socket paths pass the 108-byte AF_UNIX
// field used to fail with a bare "bind: invalid argument", which reads like a
// permissions problem. Windows already said what it was and what to do. The
// refusal names the longest NVX_HOME that works, from the real guest home
// layout (sandbox_home/<id>) and socket names.
func TestALongSocketPathSaysToShortenNvxHome(t *testing.T) {
	nvxHome := filepath.Join(tempDir(t), strings.Repeat("d", 120))
	guestHome := filepath.Join(getSandboxHomeDir(nvxHome), "0123456789abcdef")
	// What NVX_HOME does not control, spelled out so the limit is not recomputed
	// by the formula under test.
	const egressRest = "/sandbox_home/0123456789abcdef/.nvx-egress.sock"
	const connectRest = "/sandbox_home/0123456789abcdef/.nvx-connect-9222.sock"

	proxy, err := startEgressProxy(context.Background(), DefaultPolicy(), Providers["node"], tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	err = prepareEgressSocket(proxy, guestHome, nvxHome, &NetworkLaunchContext{Mode: "proxy"})
	want := fmt.Sprintf("at most %d characters (it is %d)", unixSocketPathMax-1-len(egressRest), len(nvxHome))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("egress socket: got %v, want an error containing %q", err, want)
	}

	netCtx := NetworkLaunchContext{Mode: "proxy", ConnectPorts: []connectMapping{{Host: 9222}}}
	_, stop, err := openConnectSockets(guestHome, nvxHome, &netCtx)
	stop()
	want = fmt.Sprintf("at most %d characters (it is %d)", unixSocketPathMax-1-len(connectRest), len(nvxHome))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("tunnel socket: got %v, want an error containing %q", err, want)
	}
}
