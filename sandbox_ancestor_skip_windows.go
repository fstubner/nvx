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
// AppData\Local and AppData -- the icacls write never completes and is killed at
// the per-path timeout. The cheap has-grant read then answers "not granted"
// forever, because the grant it is looking for never landed, so the next launch
// tried again, and the next. Measured cost: 3057ms cold and 3054ms warm, against
// tens of milliseconds for every other phase of a launch.
//
// What made the retry pointless rather than merely slow: the grants are not
// needed. With the ancestor walk skipped entirely, a contained process still
// launches, stats and writes its working directory, and two of those three
// ancestors are statable anyway from ACEs Windows ships. So the failing grants
// were buying nothing and costing three seconds a command.
//
// Failures are therefore remembered and not retried for a while. A time limit
// rather than forever, because the cause is environmental -- a filter driver, an
// antivirus policy -- and those change; an environment that starts working should
// recover on its own rather than needing someone to know about a cache file.
const ancestorSkipTTL = 7 * 24 * time.Hour

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
	now := time.Now()
	for p, ts := range raw {
		at, err := time.Parse(time.RFC3339, ts)
		if err != nil || now.Sub(at) > ancestorSkipTTL {
			continue // expired or unreadable: let it be retried
		}
		skips[p] = at
	}
	return skips
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

// grantAncestorsSkippingKnownFailures runs grant over paths, leaving out any whose
// grant failed recently, and records new failures. It returns how many it
// attempted, so the caller can still report what it skipped.
func grantAncestorsSkippingKnownFailures(nvxHome string, paths []string, grant func(string) error) (attempted int) {
	skips := loadAncestorSkips(nvxHome)

	var eligible []string
	for _, p := range paths {
		if _, skipped := skips[normalizeAncestorKey(p)]; !skipped {
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
