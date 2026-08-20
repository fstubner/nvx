//go:build windows

package main

import (
	"fmt"
	"syscall"
	"testing"
)

// A staged supervisor can be corrupted in place without changing its size -- an
// antivirus quarantine stub, a cloud-sync placeholder, a bad sector. The staged
// name encodes the source's size and timestamp and the reuse check compares size,
// so none of that notices, and every later contained launch failed identically
// with nothing in the product able to clear it. An acceptance pass found it by
// zeroing 512 bytes in place; `cleanup` and `doctor` both missed it.
//
// The recovery is one automatic re-stage on an image error, which makes it
// important that the trigger is narrow: a launch refused for permissions must not
// spin. These pin which error codes qualify.
func TestOnlyImageErrorsTriggerARestage(t *testing.T) {
	unusable := map[string]syscall.Errno{
		"corrupt file":   1392,
		"corrupt disk":   1393,
		"bad exe format": 193,
		"invalid image":  577,
		"volume altered": 1006,
	}
	for name, errno := range unusable {
		// Wrapped exactly as launchAppContainerProcess reports it, so this covers
		// the %w that carries the code through.
		err := fmt.Errorf("CreateProcess(AppContainer) exe=%q cwd=%q: %w", "x", "y", errno)
		if !stagedImageIsUnusable(err) {
			t.Errorf("%s (errno %d) should trigger a re-stage", name, uintptr(errno))
		}
	}

	// These are the launch being refused, not the image being broken. Retrying
	// would repeat the failure and hide it behind a spurious warning.
	usable := map[string]syscall.Errno{
		"access denied":     5,
		"file not found":    2,
		"path not found":    3,
		"sharing violation": 32,
	}
	for name, errno := range usable {
		err := fmt.Errorf("CreateProcess(AppContainer): %w", errno)
		if stagedImageIsUnusable(err) {
			t.Errorf("%s (errno %d) must not trigger a re-stage", name, uintptr(errno))
		}
	}

	// An error carrying no errno at all must not either.
	if stagedImageIsUnusable(fmt.Errorf("something went wrong")) {
		t.Error("an error with no syscall errno triggered a re-stage")
	}
	if stagedImageIsUnusable(nil) {
		t.Error("a nil error triggered a re-stage")
	}
}

// The recovery only works if the code survives the wrapping the launch path does.
// It was %v before this change, which stringified the errno and lost it.
func TestLaunchErrorCarriesTheErrnoThrough(t *testing.T) {
	wrapped := fmt.Errorf("CreateProcess(AppContainer) exe=%q cwd=%q: %w", "a", "b", syscall.Errno(1392))
	if !stagedImageIsUnusable(wrapped) {
		t.Fatal("the errno did not survive wrapping; the recovery cannot fire")
	}
	// And the stringified form, which is what %v would have produced, must not.
	if stagedImageIsUnusable(fmt.Errorf("%s", wrapped.Error())) {
		t.Error("a stringified error matched; detection must be by code, not message text")
	}
}
