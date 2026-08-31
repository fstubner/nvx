//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// verdictT records which way requireAppContainerLaunch went.
//
// Fatalf and Skipf abandon the calling goroutine, which is why the real ones end
// in runtime.Goexit. A recorder that merely notes the call and returns lets
// execution run on into later branches, so the LAST verdict reached is the one
// recorded rather than the first -- which is not a subtle difference: the first
// version of this file reported the fallback failure for every case and made a
// working decision look broken. stopVerdict reproduces the abandonment.
type verdictT struct {
	failed, skipped bool
	msg             string
}

type stopVerdict struct{}

func (v *verdictT) Helper() {}
func (v *verdictT) Fatalf(format string, args ...any) {
	v.failed = true
	v.msg = fmt.Sprintf(format, args...)
	panic(stopVerdict{})
}
func (v *verdictT) Skipf(format string, args ...any) {
	v.skipped = true
	v.msg = fmt.Sprintf(format, args...)
	panic(stopVerdict{})
}

// decide runs requireAppContainerLaunch and returns where it landed.
func decide(err error) verdictT {
	var v verdictT
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ours := r.(stopVerdict); !ours {
					panic(r)
				}
			}
		}()
		requireAppContainerLaunch(&v, err)
	}()
	return v
}

// withCapability swaps the host answer for one case.
func withCapability(t *testing.T, capable bool, why string) {
	t.Helper()
	prev := hostAppContainerCapability
	hostAppContainerCapability = func() (bool, string) { return capable, why }
	t.Cleanup(func() { hostAppContainerCapability = prev })
}

// The refusal texts below are the ones a host that genuinely cannot make
// AppContainers produces, and they are also what a host that CAN make them
// produces when it is out of memory. Which it is decides whether a containment
// probe may skip.
//
// This is the check that a gate reporting "0 failures" actually ran its
// containment probes. On a capable host the refusal is a failure; the previous
// behaviour skipped both cases, and a full run once turned seventeen containment
// probes into skips with the gate still green.
func TestARefusedLaunchOnACapableHostIsAFailureNotASkip(t *testing.T) {
	refusals := []error{
		errors.New(`CreateProcess(AppContainer) exe="probe.exe": The system cannot find the file specified.`),
		errors.New(`CreateProcess(AppContainer) exe="probe.exe": Access is denied.`),
	}
	for _, err := range refusals {
		t.Run(err.Error(), func(t *testing.T) {
			withCapability(t, true, "")
			v := decide(err)
			if v.skipped {
				t.Fatalf("skipped on a host that demonstrably creates AppContainers; this is how a "+
					"containment probe silently does not run: %s", v.msg)
			}
			if !v.failed {
				t.Fatal("neither failed nor skipped")
			}
			if !strings.Contains(v.msg, "DOES create AppContainer children") {
				t.Fatalf("the failure does not say why it is not an environment excuse: %q", v.msg)
			}
		})
	}
}

// The complement, and the reason the skip still exists: on a host that really
// cannot create AppContainer children -- a GitHub-hosted Windows runner -- these
// probes must still skip rather than turn CI red.
func TestARefusedLaunchOnAnIncapableHostStillSkips(t *testing.T) {
	withCapability(t, false, "the control launch of cmd.exe was refused: Access is denied.")
	v := decide(errors.New(`CreateProcess(AppContainer): Access is denied.`))
	if !v.skipped {
		t.Fatalf("did not skip on a host that cannot create AppContainers; this would turn every hosted CI run red (failed=%v msg=%q)", v.failed, v.msg)
	}
	if !strings.Contains(v.msg, "the control launch agrees") {
		t.Fatalf("the skip does not report the control's evidence: %q", v.msg)
	}
}

// A launch refused for any other reason was always a failure and stays one.
func TestAnUnrecognisedRefusalIsAlwaysAFailure(t *testing.T) {
	withCapability(t, false, "irrelevant")
	v := decide(errors.New("CreateProcess(AppContainer): The parameter is incorrect."))
	if v.skipped || !v.failed {
		t.Fatalf("an unrecognised refusal must fail loudly (failed=%v skipped=%v)", v.failed, v.skipped)
	}
}

// And a launch that did not fail is not a verdict at all.
func TestASuccessfulLaunchIsNeitherFailedNorSkipped(t *testing.T) {
	withCapability(t, true, "")
	v := decide(nil)
	if v.failed || v.skipped {
		t.Fatalf("a successful launch produced a verdict: failed=%v skipped=%v msg=%q", v.failed, v.skipped, v.msg)
	}
}
