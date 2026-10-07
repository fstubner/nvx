package nvx

import (
	"strings"
	"testing"
)

// Node's own fetch() and http/https ignore HTTP_PROXY and HTTPS_PROXY unless
// NODE_USE_ENV_PROXY=1 is set, so a contained process using them went straight to
// the network. The OS refused that, and the proxy was never asked, even for a
// host the allowlist names.
//
// nvx sets the variable wherever it sets the proxy variables. The tests below
// look at the environment a contained process is handed, which is the layer that
// matters. A Node that is never told cannot be blamed for not asking.

// nodeProxyValues returns every NODE_USE_ENV_PROXY entry in env, matching the name
// the way Windows does.
func nodeProxyValues(env []string) []string {
	var out []string
	for _, e := range env {
		name, value, _ := strings.Cut(e, "=")
		if strings.EqualFold(name, "NODE_USE_ENV_PROXY") {
			out = append(out, value)
		}
	}
	return out
}

func testProxyForEnv() *EgressProxy {
	return &EgressProxy{httpAddr: "127.0.0.1:41000", socksAddr: "127.0.0.1:41001", token: "tok"}
}

func TestProxyEnvironmentTellsNodeToUseTheProxy(t *testing.T) {
	env := applyProxyEnv([]string{"PATH=/bin"}, testProxyForEnv())
	if got := nodeProxyValues(env); len(got) != 1 || got[0] != "1" {
		t.Fatalf("NODE_USE_ENV_PROXY entries = %q, want exactly [1]; env: %q", got, env)
	}
	// It travels with the variables it makes Node read.
	if !strings.Contains(strings.Join(env, "\n"), "HTTPS_PROXY=http://nvx:tok@127.0.0.1:41000") {
		t.Fatalf("the proxy variables are missing: %q", env)
	}
}

// No proxy, no variable. network.mode open sets nothing, and a Node told to use
// a proxy that is not there would only be a Node told to fail.
func TestNoProxyMeansNoNodeProxyVariable(t *testing.T) {
	env := applyProxyEnv([]string{"PATH=/bin"}, nil)
	if got := nodeProxyValues(env); len(got) != 0 {
		t.Fatalf("NODE_USE_ENV_PROXY was set without a proxy: %q", got)
	}
}

// What the contained process gets is the scrubbed environment with the proxy
// variables put on afterwards, so the scrub, which drops every name not on its
// list, cannot take the variable away. A value passed in through
// isolation.environment.allow is replaced, not duplicated, because two entries
// of one name leave which one wins to the platform.
func TestTheScrubDoesNotRemoveTheNodeProxyVariableNvxSets(t *testing.T) {
	t.Setenv("NODE_USE_ENV_PROXY", "0")

	t.Run("the host's own value does not pass the scrub", func(t *testing.T) {
		scrubbed := scrubEnvironmentAllowing("", nil)
		if got := nodeProxyValues(scrubbed.Env); len(got) != 0 {
			t.Fatalf("the scrub passed the host's NODE_USE_ENV_PROXY: %q", got)
		}
		env := applyProxyEnv(scrubbed.Env, testProxyForEnv())
		if got := nodeProxyValues(env); len(got) != 1 || got[0] != "1" {
			t.Fatalf("NODE_USE_ENV_PROXY entries after the scrub = %q, want exactly [1]", got)
		}
	})

	t.Run("a project that names it in isolation.environment.allow", func(t *testing.T) {
		scrubbed := scrubEnvironmentAllowing("", []string{"NODE_USE_ENV_PROXY"})
		if got := nodeProxyValues(scrubbed.Env); len(got) != 1 || got[0] != "0" {
			t.Fatalf("the allowed variable did not pass the scrub: %q", got)
		}
		env := applyProxyEnv(scrubbed.Env, testProxyForEnv())
		if got := nodeProxyValues(env); len(got) != 1 || got[0] != "1" {
			t.Fatalf("NODE_USE_ENV_PROXY entries = %q, want exactly [1]: nvx sets the proxy variables, and a project cannot turn Node's use of them off", got)
		}
	})
}

// The supervisor inside the sandbox rewrites the proxy variables to point at the
// relay, and it is the last thing to touch the environment before the target
// starts.
func TestRelayEnvironmentKeepsTheNodeProxyVariable(t *testing.T) {
	inherited := []string{
		"PATH=/bin",
		"HTTPS_PROXY=http://nvx:tok@127.0.0.1:41000",
		"NODE_USE_ENV_PROXY=0", // whatever arrived, only 1 leaves
	}
	env := applyRelayProxyEnv(inherited, "127.0.0.1:42000", "")
	if got := nodeProxyValues(env); len(got) != 1 || got[0] != "1" {
		t.Fatalf("NODE_USE_ENV_PROXY entries = %q, want exactly [1]; env: %q", got, env)
	}
	if !strings.Contains(strings.Join(env, "\n"), "HTTPS_PROXY=http://nvx:tok@127.0.0.1:42000") {
		t.Fatalf("the proxy variables do not point at the relay: %q", env)
	}

	// With no relay, the proxy variables are stripped, so the one that tells Node
	// to read them goes too.
	if got := nodeProxyValues(applyRelayProxyEnv(inherited, "", "")); len(got) != 0 {
		t.Fatalf("NODE_USE_ENV_PROXY survived with no proxy to use: %q", got)
	}
}

// The ports nvx opens inside the sandbox are endpoints in the sandbox. A client
// that follows the proxy variables has to be told to dial them itself, or its
// request goes to the proxy, which would dial the same number on this machine.
func TestInSandboxNoProxyNamesThePortsNvxOpened(t *testing.T) {
	args := supervisorExecArgs{
		ConnectPorts: []connectMapping{{Host: 9222, Inside: 19222}, {Host: 5432, Inside: 0}},
		ExposePorts:  []int{5173},
	}
	want := "127.0.0.1:19222,localhost:19222,127.0.0.1:5173,localhost:5173"
	if got := inSandboxNoProxy(args); got != want {
		t.Fatalf("inSandboxNoProxy = %q, want %q (a port of 0 names nothing)", got, want)
	}
	if got := inSandboxNoProxy(supervisorExecArgs{}); got != "" {
		t.Fatalf("inSandboxNoProxy with no ports = %q, want nothing, so NO_PROXY stays unset", got)
	}
}

func TestRelayEnvironmentSetsNoProxyOnlyWhenThereAreInSandboxPorts(t *testing.T) {
	noProxyValues := func(env []string) []string {
		var out []string
		for _, e := range env {
			if name, value, _ := strings.Cut(e, "="); strings.EqualFold(name, "NO_PROXY") {
				out = append(out, name+"="+value)
			}
		}
		return out
	}
	// What the parent put there, which the supervisor replaces.
	inherited := []string{"NO_PROXY=127.0.0.1,localhost,::1", "HTTPS_PROXY=http://nvx:tok@127.0.0.1:41000"}

	got := noProxyValues(applyRelayProxyEnv(inherited, "127.0.0.1:42000", "127.0.0.1:19222,localhost:19222"))
	if strings.Join(got, "|") != "NO_PROXY=127.0.0.1:19222,localhost:19222|no_proxy=127.0.0.1:19222,localhost:19222" {
		t.Fatalf("NO_PROXY entries = %q", got)
	}
	// Loopback as a whole stays out of it, because the proxy is how a service on
	// this machine is reached from in here.
	if got := noProxyValues(applyRelayProxyEnv(inherited, "127.0.0.1:42000", "")); len(got) != 0 {
		t.Fatalf("with no in-sandbox ports NO_PROXY was set: %q", got)
	}
	if got := noProxyValues(applyRelayProxyEnv(inherited, "", "127.0.0.1:19222")); len(got) != 0 {
		t.Fatalf("with no relay NO_PROXY was set: %q", got)
	}
}
