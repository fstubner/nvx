package main

import (
	"strings"
	"testing"
)

// The line nvx prints when it starts a Docker sandbox names the variables it
// passes in, not their values.
//
// dockerRunArgs hands every allowed variable to docker as `-e KEY=VALUE`, and
// the launcher logged the whole argument list. LogInfo also goes to debug.log
// and from there into the `nvx report` bundle people are asked to attach to a
// bug report. The values are the ones the scrub let through on purpose --
// PATH always, and whatever isolation.environment.allow names, which is the
// mechanism for passing a token to a tool that needs one. So a token a user
// deliberately allowed into the sandbox was printed to the terminal and kept
// on disk.
func TestTheDockerLaunchLineNamesVariablesButNotValues(t *testing.T) {
	// A variable the policy passes through on purpose -- the shape a token
	// takes. Not PATH: the docker path drops PATH from -e because the
	// container has its own, which is why a first version of this test, using
	// PATH, found no value to redact.
	const sentinel = "hunter2-sentinel-value"
	t.Setenv("NVX_TEST_PASSTHRU", sentinel)

	cfg := SandboxConfig{Command: "node", Args: []string{"-e", "1"}, PassEnv: []string{"NVX_TEST_PASSTHRU"}}
	args := dockerRunArgs("node:20", "/work", cfg, nil, NetworkLaunchContext{Mode: "offline"})
	if !strings.Contains(strings.Join(args, " "), "NVX_TEST_PASSTHRU="+sentinel) {
		t.Fatalf("test premise failed: the allowed variable's value did not reach docker's -e arguments: %v", args)
	}

	line := dockerLaunchLine(args)
	if strings.Contains(line, sentinel) {
		t.Fatalf("the launch line carries an environment value:\n%s", line)
	}
	for _, want := range []string{"node:20", "NVX_TEST_PASSTHRU", "--network none", "-e"} {
		if !strings.Contains(line, want) {
			t.Errorf("the launch line lost %q, which is what makes it useful for debugging:\n%s", want, line)
		}
	}
}

// The redaction understands every spelling docker accepts, so a future change
// to how dockerRunArgs passes the environment cannot reopen this quietly.
func TestDockerLaunchLineRedactsEverySpellingOfAnEnvironmentFlag(t *testing.T) {
	for _, tc := range []struct{ name, in, mustNot, must string }{
		{"-e KEY=VALUE", "run -e SECRET=abc image", "abc", "SECRET"},
		{"--env KEY=VALUE", "run --env SECRET=abc image", "abc", "SECRET"},
		{"-e=KEY=VALUE", "run -e=SECRET=abc image", "abc", "SECRET"},
		{"--env=KEY=VALUE", "run --env=SECRET=abc image", "abc", "SECRET"},
		{"a value containing =", "run -e SECRET=a=b=c image", "a=b", "SECRET"},
		{"a bare key passes the parent's value; nothing to show", "run -e SECRET image", "\x00", "SECRET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := dockerLaunchLine(strings.Split(tc.in, " "))
			if strings.Contains(line, tc.mustNot) {
				t.Errorf("value leaked: %q", line)
			}
			if !strings.Contains(line, tc.must) {
				t.Errorf("key lost: %q", line)
			}
			if !strings.Contains(line, "image") {
				t.Errorf("a non-environment token was altered: %q", line)
			}
		})
	}
}
