//go:build windows

package main

import (
	"strings"
	"testing"
	"time"
)

// An install that sits there forever must not do so silently: a user who sees
// nothing concludes nvx is broken, rather than that this one command needs
// --no-sandbox.
//
// This used to assert the hint contained "install script captures", pinning a
// specific cause -- subprocess output capture -- that has since been fixed. The
// hint went on naming it for weeks, and the test would have gone on holding it
// there. So the assertions are now on what stays true whatever the cause: the
// symptom is reported, and the escape hatch is named. The cause the hint offers
// is prose, and prose that names a fixed bug as the live one is what an
// acceptance pass caught here.
func TestSlowContainedInstallGetsADiagnosis(t *testing.T) {
	orig := hangHintDelay
	hangHintDelay = 20 * time.Millisecond
	defer func() { hangHintDelay = orig }()

	out := captureStderr(t, func() {
		stop := startHangHint("npm", []string{"install", "esbuild"})
		time.Sleep(150 * time.Millisecond)
		stop()
	})

	if !strings.Contains(out, "has been running") {
		t.Errorf("a stuck install produced no diagnosis; stderr was:\n%s", out)
	}
	if !strings.Contains(out, "--no-sandbox") {
		t.Errorf("the diagnosis must name the workaround; stderr was:\n%s", out)
	}
}

// The hint must not fire for a command that finishes, or every normal install
// would carry a scary warning about a problem it does not have.
func TestFastInstallGetsNoDiagnosis(t *testing.T) {
	orig := hangHintDelay
	hangHintDelay = 2 * time.Second
	defer func() { hangHintDelay = orig }()

	out := captureStderr(t, func() {
		stop := startHangHint("npm", []string{"install"})
		stop() // finished immediately
		time.Sleep(50 * time.Millisecond)
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("a completed install was warned about anyway:\n%s", out)
	}
}

// Only invocations that run third-party install scripts qualify. A long dev
// server or test run is not a symptom of the named-pipe restriction, and warning
// about it would train users to ignore the message.
func TestOnlyInstallsQualifyForTheHint(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		want    bool
	}{
		{"npm", []string{"install"}, true},
		{"npm", []string{"install", "esbuild"}, true},
		{"npm", []string{"i", "lodash"}, true},
		{"npm", []string{"ci"}, true},
		{"yarn", []string{"add", "vite"}, true},
		{"pnpm", []string{"install"}, true},
		{"bun", []string{"add", "zod"}, true},
		{"npm", []string{"run", "dev"}, false},
		{"npm", []string{"test"}, false},
		{"npm", []string{"run", "build"}, false},
		{"node", []string{"server.js"}, false},
		{"npx", []string{"eslint"}, false},
	}
	for _, tc := range cases {
		got := commandCanTripNamedPipeLimit(tc.command, tc.args)
		if got != tc.want {
			t.Errorf("commandCanTripNamedPipeLimit(%q, %v) = %v, want %v",
				tc.command, tc.args, got, tc.want)
		}
	}
}
