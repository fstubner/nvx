package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// sessionOwnerFile records which nvx process owns an ephemeral guest home, so
// `nvx cleanup` can tell a crashed session's leftovers from one that is in use.
//
// The name is dotted so it sorts away from the profile skeleton, and it lives
// inside the guest home rather than in a side table: a directory and its
// ownership record cannot then disagree, and removing the directory removes the
// record with it.
const sessionOwnerFile = ".nvx-session"

// sessionOwner is written once, at guest-home creation, and only ever read.
type sessionOwner struct {
	PID        int    `json:"pid"`
	StartedUTC string `json:"started_utc"`
}

// writeSessionOwner records this process as the owner of guestHome.
//
// Best-effort by design: a guest home that fails to get a marker is still a
// usable sandbox, and the only cost is that `nvx cleanup` falls back to the age
// rule for it. Refusing to launch over an unwritable marker would trade a
// working sandbox for a housekeeping nicety.
func writeSessionOwner(guestHome string, now time.Time) {
	data, err := json.Marshal(sessionOwner{
		PID:        os.Getpid(),
		StartedUTC: now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(guestHome, sessionOwnerFile), data, 0600)
}

// readSessionOwner returns the recorded owner of guestHome, or ok=false when
// there is no readable marker.
func readSessionOwner(guestHome string) (sessionOwner, bool) {
	data, err := os.ReadFile(filepath.Join(guestHome, sessionOwnerFile))
	if err != nil {
		return sessionOwner{}, false
	}
	var owner sessionOwner
	if json.Unmarshal(data, &owner) != nil || owner.PID <= 0 {
		return sessionOwner{}, false
	}
	return owner, true
}

// unownedGuestHomeGrace is how long a guest home with no readable owner marker
// is left alone.
//
// It covers the window between MkdirAll and the marker being written, and guest
// homes created by versions of nvx that predate the marker. Without it, a
// concurrent `nvx cleanup` could delete a sandbox that is milliseconds from
// starting -- the same failure this whole file exists to prevent, just narrower.
const unownedGuestHomeGrace = time.Hour

// guestHomeIsInUse reports whether an ephemeral guest home belongs to a live
// session and must not be deleted.
//
// Two rules, in order:
//
//   - A marker naming a process that is still running means in use. That is the
//     case `nvx cleanup` used to get wrong: it deleted the working directory of
//     every concurrent sandbox, including installs in progress.
//   - No readable marker means fall back to age, because absence is ambiguous --
//     it is equally a pre-marker guest home and one being created right now.
//
// PID reuse can make a dead session look live. That direction is deliberate: the
// cost is a leftover directory that the next cleanup removes once the reused PID
// exits, against deleting a running install's home. Stale-but-present is the
// cheaper mistake.
func guestHomeIsInUse(guestHome string, now time.Time) bool {
	if owner, ok := readSessionOwner(guestHome); ok {
		if owner.PID == os.Getpid() {
			return true // this very process, e.g. cleanup called mid-session
		}
		return processIsRunning(owner.PID)
	}

	info, err := os.Stat(guestHome)
	if err != nil {
		return false // unreadable; let the caller try to remove it
	}
	return now.Sub(info.ModTime()) < unownedGuestHomeGrace
}
