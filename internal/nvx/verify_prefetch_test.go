package nvx

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// `npm ci` verifies every lockfile entry, and each was fetched only when the
// walk reached it: one registry request at a time, 36 to 74 s for a 651-entry
// lockfile. The fetches now run ahead of the walk, a bounded number at once.
func TestVerifyFetchesRunConcurrentlyWithinTheirBound(t *testing.T) {
	var inFlight, peak atomic.Int32
	var mu sync.Mutex
	calls := map[string]int{}
	orig := resolveNpmPackageDetailsForVerify
	resolveNpmPackageDetailsForVerify = func(name, query string) (string, time.Time, bool, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		mu.Lock()
		calls[name+"@"+query]++
		mu.Unlock()
		return query, time.Time{}, false, nil
	}
	t.Cleanup(func() { resolveNpmPackageDetailsForVerify = orig })

	var args []string
	for i := 0; i < 3*verifyFetchConcurrency; i++ {
		args = append(args, fmt.Sprintf("pkg%d@1.0.%d", i, i))
	}
	args = append(args, "pkg0@1.0.0", "git+https://example.invalid/x.git")

	got := prefetchPackageDetails(args)

	if p := peak.Load(); p < 2 || p > verifyFetchConcurrency {
		t.Errorf("peak concurrent fetches = %d, want between 2 and %d", p, verifyFetchConcurrency)
	}
	if len(got) != 3*verifyFetchConcurrency {
		t.Errorf("got details for %d packages, want %d", len(got), 3*verifyFetchConcurrency)
	}
	if calls["pkg0@1.0.0"] != 1 {
		t.Errorf("a repeated package was fetched %d times, want 1", calls["pkg0@1.0.0"])
	}
	if d := got[packageQueryKey{"pkg5", "1.0.5"}]; d.version != "1.0.5" || d.err != nil {
		t.Errorf("details for pkg5 = %+v", d)
	}
}
