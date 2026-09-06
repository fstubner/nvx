//go:build windows

package main

import (
	"syscall"
	"testing"
)

// The handle list pinned onto a contained launch must be distinct and valid.
//
// This is not a tidiness check. PROC_THREAD_ATTRIBUTE_HANDLE_LIST is validated
// by the kernel at CreateProcess time, and a list containing a duplicate or an
// unusable handle fails the whole call with ERROR_INVALID_PARAMETER -- it does
// not skip the bad entry. So getting this wrong does not weaken containment for
// one run, it breaks EVERY contained run on the machine.
//
// Both rejected shapes are ordinary, not exotic. A console gives stdin, stdout
// and stderr the same handle. `nvx ... > log 2>&1` gives stdout and stderr the
// same handle. A process with no console and no redirection gets null or
// INVALID_HANDLE_VALUE back from GetStdHandle.
func TestTheInheritedHandleListIsDistinctAndUsable(t *testing.T) {
	const (
		a = syscall.Handle(0x10)
		b = syscall.Handle(0x20)
		c = syscall.Handle(0x30)
	)

	for _, tc := range []struct {
		name string
		in   stdioHandles
		want []syscall.Handle
	}{
		{
			"three separate streams pass through in order",
			stdioHandles{in: a, out: b, err: c},
			[]syscall.Handle{a, b, c},
		},
		{
			"a console, where all three are the same handle",
			stdioHandles{in: a, out: a, err: a},
			[]syscall.Handle{a},
		},
		{
			"stdout and stderr redirected to one file",
			stdioHandles{in: a, out: b, err: b},
			[]syscall.Handle{a, b},
		},
		{
			"no console and no redirection: nothing to pin",
			stdioHandles{in: 0, out: syscall.InvalidHandle, err: 0},
			nil,
		},
		{
			"one stream missing, the others still pinned",
			stdioHandles{in: 0, out: b, err: c},
			[]syscall.Handle{b, c},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inheritableStdioHandleList(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
			seen := map[syscall.Handle]bool{}
			for _, h := range got {
				if seen[h] {
					t.Errorf("handle %#x appears twice; CreateProcess would reject the launch entirely", h)
				}
				seen[h] = true
				if h == 0 || h == syscall.InvalidHandle {
					t.Errorf("handle %#x is not usable; CreateProcess would reject the launch entirely", h)
				}
			}
		})
	}
}

// The attribute is omitted, not passed empty, when there is nothing to pin.
//
// A launch with no console and no redirection has no handles to inherit. Asking
// for a handle-list attribute anyway would mean handing CreateProcess a
// zero-length list, so the launcher counts attributes from this result and the
// count must come out at one, not two.
func TestNothingToPinMeansNoHandleListAttribute(t *testing.T) {
	got := inheritableStdioHandleList(stdioHandles{in: 0, out: syscall.InvalidHandle, err: 0})
	if len(got) != 0 {
		t.Fatalf("expected an empty list so the caller omits the attribute, got %v", got)
	}
}
