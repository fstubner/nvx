package nvx

import (
	"strings"
	"testing"
)

// Yarn 2 and later, Yarn Berry, ignores HTTP_PROXY and HTTPS_PROXY. Its own
// settings are httpProxy and httpsProxy, which it also reads from YARN_HTTP_PROXY
// and YARN_HTTPS_PROXY. A contained `yarn install` therefore never asked nvx's
// proxy for anything, and failed on the first fetch with a DNS error for
// registry.yarnpkg.com, a host the allowlist names.

// envValues returns every value env holds under name, matching the name the way
// Windows does.
func envValues(env []string, name string) []string {
	var out []string
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if strings.EqualFold(k, name) {
			out = append(out, v)
		}
	}
	return out
}

func TestProxyEnvironmentPointsYarnAtTheProxy(t *testing.T) {
	p := testProxyForEnv()
	env := applyProxyEnv([]string{"PATH=/bin", "YARN_HTTP_PROXY=http://host-proxy.example:8080"}, p, false)

	want := p.HTTProxyURL()
	for _, name := range []string{"YARN_HTTP_PROXY", "YARN_HTTPS_PROXY"} {
		got := envValues(env, name)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want exactly [%s] (an inherited value is replaced, not kept beside it)", name, got, want)
		}
	}
	// With no proxy, which is network.mode open, the environment comes back as it was.
	if got := envValues(applyProxyEnv([]string{"YARN_HTTPS_PROXY=http://host-proxy.example:8080"}, nil, false), "YARN_HTTPS_PROXY"); len(got) != 1 {
		t.Errorf("with no proxy the environment is returned as it was: %q", got)
	}
}

// The supervisor inside the sandbox rewrites the proxy address to the relay's, and
// Yarn has to follow, or it would dial the parent's own listener, which is not
// reachable from in there.
func TestRelayEnvironmentPointsYarnAtTheRelay(t *testing.T) {
	inherited := []string{
		"HTTPS_PROXY=http://nvx:tok@127.0.0.1:41000",
		"YARN_HTTP_PROXY=http://nvx:tok@127.0.0.1:41000",
		"YARN_HTTPS_PROXY=http://nvx:tok@127.0.0.1:41000",
	}
	env := applyRelayProxyEnv(inherited, "127.0.0.1:42000", "")
	for _, name := range []string{"YARN_HTTP_PROXY", "YARN_HTTPS_PROXY"} {
		got := envValues(env, name)
		if len(got) != 1 || got[0] != "http://nvx:tok@127.0.0.1:42000" {
			t.Errorf("%s = %q, want the relay's address with the session credential", name, got)
		}
	}
	// With no relay there is nothing for it to point at.
	for _, name := range []string{"YARN_HTTP_PROXY", "YARN_HTTPS_PROXY"} {
		if got := envValues(applyRelayProxyEnv(inherited, "", ""), name); len(got) != 0 {
			t.Errorf("%s survived with no relay: %q", name, got)
		}
	}
}
