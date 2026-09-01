//go:build windows

package main

// A containment probe must decide for itself where its projects begin.
//
// nvx scopes a sandbox to a project, and finds that project with
// sandboxScopeForWorkDir -> findProjectRoot, which walks UP from the working
// directory looking for a package.json. The identity a session is granted is
// derived from whatever that walk returns.
//
// The probes build their projects with tempDir, under %TEMP%. A bare temporary
// directory holds no package.json, so the walk keeps going -- past %TEMP%, past
// AppData -- and whatever it finds above becomes the scope. On 2026-09-01 an
// `npm install` was run in C:\Users\Felix, leaving a package.json in the home
// directory at 18:40. From that moment every temporary directory on the machine
// resolved to the SAME project root, so "project A" and "project B" derived the
// SAME capability, and a session scoped to B held exactly the identity that opens
// A. TestSandboxCannotReachOtherProjects and TestOneSandboxSessionCannotReadAnother
// both began reporting a containment hole in nvx that nvx had not opened. The
// same gate had passed twice earlier the same day, before that file existed.
//
// Giving each fixture its own package.json stops the walk at the fixture, which
// is what a real project looks like and what production therefore exercises. It
// makes the probes stricter rather than quieter: each project now genuinely gets
// its own identity, so a regression that shares one identity across projects
// fails here instead of being indistinguishable from the ambient filesystem.
//
// The underlying behaviour is left exactly as it is, and is worth knowing: a
// package.json above your projects collapses all of them into one sandbox scope.
// That is a property of nvx, not of these tests, and it is not this file's place
// to change it.

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureProjectDir returns a temporary directory that reads as a project root.
func fixtureProjectDir(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	markAsProjectRoot(t, dir)
	return dir
}

// markAsProjectRoot makes findProjectRoot stop at dir.
func markAsProjectRoot(t *testing.T, dir string) {
	t.Helper()
	manifest := filepath.Join(dir, "package.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"nvx-probe-fixture","private":true}`+"\n"), 0o600); err != nil {
		t.Fatalf("mark %s as a project root: %v", dir, err)
	}
	// Fail loudly rather than silently measuring the wrong thing: if the scope
	// still resolves somewhere else, every assertion downstream is about that
	// other directory's identity instead of this fixture's.
	if got := sandboxScopeForWorkDir(dir); !dirsEqual(got, dir) {
		t.Fatalf("fixture %s still resolves to project root %s, so this probe would compare "+
			"two projects that share one sandbox identity and its result would be meaningless", dir, got)
	}
}
