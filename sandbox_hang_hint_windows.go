//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"time"
)

// An install that trips a sandbox restriction can hang forever and say nothing.
//
// A contained process cannot create a named pipe, and Windows implements piped
// child stdio with named pipes. That is why `npm install esbuild` once never
// returned: its postinstall captures a subprocess itself, and the call blocked
// inside libuv before the grandchild existed. Measured 2026-08-19: no completion
// after 13 minutes against 8 seconds uncontained.
//
// Both halves of capture work now -- synchronous through temp files, streaming
// through pipes created outside the container -- so esbuild installs in seconds
// and is no longer an example of anything. The hint kept naming it as the live
// case for weeks afterwards, which an acceptance pass caught by installing
// esbuild and watching it succeed.
//
// What is still true is narrower: a child's stdin. `child.stdin` is null inside
// the sandbox, so an install script that feeds input to a subprocess cannot, and
// how that surfaces depends on the script -- a crash, or a wait for a prompt that
// will never be answered. So the hint no longer asserts a cause. It reports the
// symptom, offers the escape hatch, and leaves the diagnosis open, which is the
// honest shape for something firing on a timer.
//
// A hint, not a kill: a large install legitimately takes minutes, and terminating
// someone's install on a timer would be a worse failure than the one being
// diagnosed. It is worded as a possibility for the same reason.

// hangHintDelay is how long a contained install runs before the hint appears. Well
// clear of a slow-but-healthy install; a var so tests do not have to wait it out.
var hangHintDelay = 2 * time.Minute

// startHangHint prints a diagnostic once, if the command is still running after
// hangHintDelay. The returned function stops it and must be called.
func startHangHint(command string, args []string) (stop func()) {
	if !commandCanTripNamedPipeLimit(command, args) {
		return func() {}
	}
	// Read once, here, rather than inside the goroutine: the goroutine outlives
	// this call, so reading a package var from it races with anything that sets
	// the var afterwards.
	delay := hangHintDelay
	done := make(chan struct{})
	// exited makes stop() synchronous: it returns only once the goroutine can no
	// longer write anything. Without that the hint could still be printing after
	// the command it describes has finished and nvx has moved on.
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-done:
		case <-time.After(delay):
			LogWarn("This install has been running for %s with no result.", delay)
			LogWarn("That can be a large install, or a package whose install script does something the sandbox does not allow -- writing to a subprocess's stdin is the known one on Windows.")
			LogWarn("If it never finishes, install that package with: nvx --no-sandbox <your install command>")
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

// commandCanTripNamedPipeLimit reports whether this invocation runs third-party
// install scripts, which is the only situation the hint is about. A long-running
// dev server or test run is not a symptom of anything.
//
// `npx`/`bunx` tools hit the same restriction and are deliberately NOT covered.
// An acceptance pass noted the gap, and the honest answer is that a timer cannot
// distinguish the two cases there: an install still running after two minutes is
// anomalous, while an npx-launched dev server running for hours is doing exactly
// what was asked. Hinting on those would be noise, and a hint people learn to
// ignore is worse than none. The limitation is documented for tool runners in
// README.md and SECURITY.md instead of guessed at here.
// It deliberately does not reuse isPackageManagerCommand: that answers "does this
// walk to the drive root", feeds the elevation advisory, and omits bun. The
// question here is "does this run third-party install scripts", which is a
// different set.
func commandCanTripNamedPipeLimit(command string, args []string) bool {
	base := strings.ToLower(filepath.Base(command))
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".cmd"), ".exe")
	switch base {
	case "npm", "yarn", "pnpm", "bun":
	default:
		return false
	}
	for _, a := range args {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "install", "i", "add", "ci", "update", "up":
			return true
		}
	}
	return false
}
