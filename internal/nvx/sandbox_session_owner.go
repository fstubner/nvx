package nvx

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

// sessionLeasePrefix names the per-run owner records in a persistent tool home.
//
// A tool home outlives its runs and several runs of the same tool can share it
// at once, so the single write-once marker above cannot describe it. Each run
// writes its own lease, named by its random sandbox id, and removes it when it
// ends. Without one, a trusted tool running for longer than the package
// retention window lost its AppContainer profile underneath it.
const sessionLeasePrefix = ".nvx-lease-"

// writeSessionLease records this process as a current user of a persistent
// home, and returns the function that removes the record again. Best-effort for
// the same reason as writeSessionOwner.
func writeSessionLease(home, sandboxID string, now time.Time) (release func()) {
	path := filepath.Join(home, sessionLeasePrefix+sandboxID)
	data, err := json.Marshal(sessionOwner{
		PID:        os.Getpid(),
		StartedUTC: now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return func() {}
	}
	_ = os.WriteFile(path, data, 0600)
	return func() { _ = os.Remove(path) }
}

// homeHasLiveLease reports whether any lease in home names a running process.
// A lease left by a run that was killed names a dead process and holds nothing.
func homeHasLiveLease(home string) bool {
	matches, _ := filepath.Glob(filepath.Join(home, sessionLeasePrefix+"*"))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var owner sessionOwner
		if json.Unmarshal(data, &owner) != nil || owner.PID <= 0 {
			continue
		}
		if owner.PID == os.Getpid() || processIsRunning(owner.PID) {
			return true
		}
	}
	return false
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
