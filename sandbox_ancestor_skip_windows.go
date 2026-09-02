//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Ancestor grants that cannot succeed used to be retried on every launch.
//
// The walk grants traverse rights on the directories above the working directory
// and the guest home. On some chains -- measured on AppData\Local\Temp,
// AppData\Local and AppData -- the write does not complete and is killed at the
// per-path timeout. The cheap has-grant read then answers "not granted", so the
// next launch tried again, and the next. Measured cost: 3057ms cold and 3054ms
// warm, against tens of milliseconds for every other phase of a launch.
//
// Why those three and not others, measured 2026-08-31: the cost is the size of
// the subtree, not the identity of the directory. SetNamedSecurityInfoW on a
// directory runs Windows' auto-inheritance propagation over everything beneath
// it, and it is linear in the number of entries -- 1ms empty, 92ms at 500
// entries, 773ms at 5000, 3.108s at 20000. This profile holds 748,317 entries
// under AppData\Local\Temp and over 2,000,000 under each of AppData\Local and
// AppData. A 1500ms budget is not attainable at that size and never will be.
//
// Two things follow that the earlier "a filter driver, an antivirus policy"
// reading got wrong. The write is not hung: given no deadline, AppData\Local\Temp
// returned success after 3m45s (and revoking it took 2m52s), while the other two
// were still running at 5m. So an abandoned grant does land, minutes after the
// caller gave up and recorded it as failed -- which is harmless only because
// appContainerHasGrant finds it on the next launch. And the environment cannot
// "start working" again: profile trees grow.
//
// Passing DACL_SECURITY_INFORMATION without UNPROTECTED_DACL_SECURITY_INFORMATION
// was tried as a way to skip the propagation. It does not: 3.084s against 3.13s
// on the same 20000-entry tree. There is no cheap flag here.
//
// What made the retry pointless rather than merely slow: the grants are not
// needed. With the ancestor walk skipped entirely, a contained process still
// launches, stats and writes its working directory, and two of those three
// ancestors are statable anyway from ACEs Windows ships. So the failing grants
// were buying nothing and costing three seconds a command.
//
// Failures are therefore remembered and not retried for a while. A time limit
// rather than forever, so a path that stops being expensive is picked up again
// without anyone having to know a cache file exists -- a working directory moves,
// a huge Temp gets cleared, the chain above a project is simply smaller.
//
// The retry is kept even though the usual cause, subtree size, does not reverse
// on its own. It costs one timeout a month for one path, and it is the only
// thing that would notice a chain becoming cheap; deleting it would trade that
// for nothing worth having. What it is NOT is a wait for a filter driver or an
// antivirus policy to change, which is what this comment used to claim and what
// the measurement above disproves.
//
// A month rather than a week: re-testing weekly bought nothing while costing a
// visibly slower command each time it came round. Paired with re-testing one
// path per run rather than all of them, the recurring cost of remembering a
// failure is now about a second a month instead of three seconds a week.
const ancestorSkipTTL = 30 * 24 * time.Hour

func ancestorSkipPath(nvxHome string) string {
	return filepath.Join(nvxHome, "ancestor-grant-skip.json")
}

// loadAncestorSkips returns the paths whose grant recently failed, keyed by a
// normalised path. A missing or corrupt file means "nothing is skipped", which
// costs a retry rather than silently disabling the grants.
func loadAncestorSkips(nvxHome string) map[string]time.Time {
	skips := map[string]time.Time{}
	if nvxHome == "" {
		return skips
	}
	data, err := os.ReadFile(ancestorSkipPath(nvxHome))
	if err != nil {
		return skips
	}
	var raw map[string]string
	if json.Unmarshal(data, &raw) != nil {
		return skips
	}
	for p, ts := range raw {
		at, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			continue // unreadable: let it be retried
		}
		// Expired entries are KEPT, with their timestamp. Dropping them here
		// made every expired path eligible in the same run, so a machine with
		// three failing ancestors paid the whole grant budget at once -- three
		// seconds, on whichever command happened to be the first after the TTL
		// lapsed. The caller decides how many to re-test.
		skips[p] = at
	}
	return skips
}

// ancestorSkipIsExpired reports whether a recorded failure is old enough to be
// worth re-testing.
func ancestorSkipIsExpired(at time.Time) bool {
	return time.Since(at) > ancestorSkipTTL
}

func saveAncestorSkips(nvxHome string, skips map[string]time.Time) {
	if nvxHome == "" {
		return
	}
	raw := map[string]string{}
	for p, at := range skips {
		raw[p] = at.UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	if os.MkdirAll(nvxHome, 0o700) != nil {
		return
	}
	// Best-effort: this is a cache. Failing to write it costs a retry next time,
	// which is the behaviour that existed before it.
	_ = os.WriteFile(ancestorSkipPath(nvxHome), data, 0o600)
}

func normalizeAncestorKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

// grantRequiredAncestors runs grant over paths that are NOT optional, ignoring
// the skip cache entirely, and returns the ones that failed.
//
// The cache above exists because most ancestor grants buy nothing: with the walk
// skipped, a contained process still launches, stats and writes its working
// directory. That reasoning was measured on the chain above a PROJECT, and it
// does not hold for the chain above the guest home.
//
// Measured 2026-09-01: `nvx npx cowsay hi` failed with
//
//	EPERM lstat C:\Users\Felix\.nvx\sandbox_home
//
// because npm walks up from the guest home, and that directory had been recorded
// as a failed grant on 2026-08-29 and was therefore not being attempted. One
// transient failure disabled contained npx for the thirty-day life of the entry,
// silently, with the only evidence a count of skipped checks and a cache file
// nobody knows to read. Removing that single entry made the same command succeed.
//
// So a required ancestor is attempted every launch, and a failure is returned to
// be reported rather than remembered. The cost of retrying is bounded by the
// has-grant check inside the grant itself: once the ACE is there, later launches
// pay one ACL read.
func grantRequiredAncestors(paths []string, grant func(string) error) (failed []string) {
	for _, p := range paths {
		if err := grant(p); err != nil {
			failed = append(failed, p)
		}
	}
	return failed
}

// grantAncestorsSkippingKnownFailures runs grant over paths, leaving out any whose
// grant failed recently, and records new failures. It returns how many it
// attempted, so the caller can still report what it skipped.
func grantAncestorsSkippingKnownFailures(nvxHome string, paths []string, grant func(string) error) (attempted int) {
	skips := loadAncestorSkips(nvxHome)

	// At most one expired path is re-tested per run. Recovery still happens --
	// an environment that starts working is noticed within a few commands -- but
	// the cost of checking is one timeout rather than the whole budget, and it
	// lands on one command instead of stacking on the same unlucky one.
	retriedExpired := false

	var eligible []string
	for _, p := range paths {
		at, skipped := skips[normalizeAncestorKey(p)]
		switch {
		case !skipped:
			eligible = append(eligible, p)
		case ancestorSkipIsExpired(at) && !retriedExpired:
			retriedExpired = true
			eligible = append(eligible, p)
		}
	}
	if len(eligible) == 0 {
		return 0
	}

	changed := false
	attempted = grantAncestorsWithinBudget(eligible, ancestorGrantBudget, func(p string) error {
		err := grant(p)
		key := normalizeAncestorKey(p)
		if err != nil {
			skips[key] = time.Now()
			changed = true
			return err
		}
		// A grant that now works clears any old record, so a fixed environment
		// stops being penalised immediately rather than at the end of the TTL.
		if _, had := skips[key]; had {
			delete(skips, key)
			changed = true
		}
		return nil
	})

	if changed {
		saveAncestorSkips(nvxHome, skips)
	}
	return attempted
}
