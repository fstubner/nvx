//go:build linux

package main

import "testing"

// /proc is granted only when the sandbox has one of its own.
//
// Nothing remounts /proc, so the one a contained process sees is the host's,
// listing every process on the machine: cmdline is world-readable and environ
// is readable for the user's own processes, which is where credentials live.
// Granting that to contained code would be worse than the problem it solves --
// Bun needing /proc to run at all. So the grant travels with the private mount,
// and this pins that pairing, which is otherwise two statements in different
// files that have to agree.
func TestProcIsGrantedOnlyWithAPrivateMount(t *testing.T) {
	has := func(rules []landlockRule, path string) bool {
		for _, r := range rules {
			if r.path == path {
				return true
			}
		}
		return false
	}

	if has(landlockReadOnlyRules(tempDir(t), false), "/proc") {
		t.Fatal("the host's /proc is granted to contained code when no private procfs was mounted")
	}
	if !has(landlockReadOnlyRules(tempDir(t), true), "/proc") {
		t.Fatal("the sandbox's own /proc is not granted, so a runtime that reads it cannot run")
	}
}
