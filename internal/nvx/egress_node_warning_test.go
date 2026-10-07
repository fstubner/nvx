package nvx

import (
	"path/filepath"
	"strings"
	"testing"
)

// The flag that silences Node 22's experimental-feature warning is refused by a
// Node that does not know it, and that Node then does not start. These tests pin
// who gets it. Which versions accept it, and which print the warning, are
// measurements recorded in egress_node_warning.go.

func nodeOptions(env []string) []string { return envValues(env, "NODE_OPTIONS") }

func TestTheEnvProxyWarningFlagGoesOnlyToANodeThatReadsTheVariable(t *testing.T) {
	home := tempDir(t)
	cmd := func(runtime, version, name string) string {
		return filepath.Join(home, "versions", runtime, version, "bin", name)
	}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"22.23.2 prints it", cmd("node", "v22.23.2", "npm"), true},
		{"22.21.0 is the first 22 that reads the variable", cmd("node", "v22.21.0", "node"), true},
		{"22.20.9 does not read the variable", cmd("node", "v22.20.9", "node"), false},
		{"24.0.0 reads it", cmd("node", "v24.0.0", "node"), true},
		{"24.21.0", cmd("node", "v24.21.0", "npx"), true},
		{"a later major", cmd("node", "v26.1.0", "node"), true},
		{"23 never read it", cmd("node", "v23.11.1", "node"), false},
		{"21.7.3 accepts the flag and ignores the variable", cmd("node", "v21.7.3", "node"), false},
		{"20.11.0 accepts the flag and ignores the variable", cmd("node", "v20.11.0", "node"), false},
		{"19.9.0 does not start with the flag", cmd("node", "v19.9.0", "node"), false},
		{"18.20.4 does not start with the flag", cmd("node", "v18.20.4", "node"), false},
		{"18.5.0 does not start with the flag", cmd("node", "v18.5.0", "node"), false},
		{"bun", cmd("bun", "v1.4.2", "bun"), false},
		{"a Node nvx did not resolve", filepath.Join(tempDir(t), "bin", "node"), false},
		{"a version directory that is not a version", cmd("node", "lts", "node"), false},
		{"a version with fewer than three parts", cmd("node", "v22", "node"), false},
		{"the versions directory itself", filepath.Join(home, "versions", "node"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := withEnvProxyWarningSilenced([]string{"PATH=/bin"}, testProxyForEnv(), tc.path, home)
			got := nodeOptions(env)
			if tc.want {
				if len(got) != 1 || got[0] != envProxyWarningFlag {
					t.Fatalf("NODE_OPTIONS = %q, want exactly [%s]", got, envProxyWarningFlag)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("NODE_OPTIONS = %q, want none: a Node that does not know the flag refuses to start with it", got)
			}
		})
	}
}

// With no proxy there is no variable and nothing to warn about.
func TestNoProxyMeansNoWarningFlag(t *testing.T) {
	home := tempDir(t)
	path := filepath.Join(home, "versions", "node", "v22.23.2", "bin", "node")
	if got := nodeOptions(withEnvProxyWarningSilenced([]string{"PATH=/bin"}, nil, path, home)); len(got) != 0 {
		t.Fatalf("NODE_OPTIONS = %q with no proxy", got)
	}
}

// Whatever a project passed in NODE_OPTIONS stays, and the flag is not added twice.
func TestTheWarningFlagJoinsWhatNodeOptionsAlreadyHolds(t *testing.T) {
	home := tempDir(t)
	path := filepath.Join(home, "versions", "node", "v22.23.2", "bin", "node")

	env := withEnvProxyWarningSilenced([]string{"Node_Options=--max-old-space-size=512"}, testProxyForEnv(), path, home)
	if got := nodeOptions(env); len(got) != 1 || got[0] != "--max-old-space-size=512 "+envProxyWarningFlag {
		t.Fatalf("NODE_OPTIONS = %q", got)
	}
	again := withEnvProxyWarningSilenced(env, testProxyForEnv(), path, home)
	if got := nodeOptions(again); len(got) != 1 || strings.Count(got[0], envProxyWarningFlag) != 1 {
		t.Fatalf("the flag was added twice: %q", got)
	}
}
