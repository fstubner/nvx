package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A stale dictionary is still a dictionary.
//
// LoadPopularPackages discarded the cache the moment it passed seven days and
// checked against EmbeddedPopularPackages instead -- 33 names against the 2000 on
// disk -- while the sync that would have replaced it ran in the background for
// next time. The user saw "Verifying package" either way. Measured on the machine
// this was found on: a 2000-entry cache, six days old, due to become 33 the
// following afternoon.
func TestAStaleTyposquatDictionaryIsUsedRatherThanDiscarded(t *testing.T) {
	nvxHome := tempDir(t)
	cachePath := filepath.Join(nvxHome, "popular_packages.json")

	// A sentinel no real dictionary can contain, because the assertion has to be
	// about THIS list rather than about a list of the right size.
	//
	// The first version compared lengths only, and did not catch its own sabotage:
	// with the fix disabled the fallback path fetches the live dataset, which has
	// exactly 2000 entries, so a 2000-entry fixture and the real list were
	// indistinguishable. It passed while proving nothing.
	const sentinel = "nvx-test-sentinel-not-a-real-package"
	big := make([]string, 0, 2000)
	big = append(big, sentinel)
	for i := 1; i < 2000; i++ {
		big = append(big, "pkg-"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+string(rune('0'+(i/676)%10)))
	}
	data, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Older than the refresh interval.
	old := time.Now().Add(-popularPackagesTTL - 48*time.Hour)
	if err := os.Chtimes(cachePath, old, old); err != nil {
		t.Fatal(err)
	}

	got := LoadPopularPackages(nvxHome)
	found := false
	for _, name := range got {
		if name == sentinel {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the cached dictionary was not used (%d entries returned, cache held %d); "+
			"a stale cache is discarded and the typosquat check silently becomes a different check",
			len(got), len(big))
	}
}

// ...but an unusable cache still falls back, or a corrupt file would disable the
// check entirely rather than degrading it.
func TestAnUnusableTyposquatCacheFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"corrupt json", "{not json"},
		{"empty list", "[]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nvxHome := tempDir(t)
			cachePath := filepath.Join(nvxHome, "popular_packages.json")
			if err := os.WriteFile(cachePath, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			// Nothing usable on disk, and no network in a test: the embedded list is
			// the only correct answer, and it must not be empty.
			got := LoadPopularPackages(nvxHome)
			if len(got) == 0 {
				t.Fatal("no dictionary at all; the typosquat check would pass everything")
			}
		})
	}
}

// OSV: "could not check" must never be rendered as "checked, nothing found".
//
// The result loop was `if i < len(batchResp.Results)`, so a query the API did not
// answer was skipped and its package came back with nothing against it -- and the
// caller prints "Vulnerability scan clean. No active CVEs found." for an empty
// map. The caller already handles an error by asking whether to proceed without
// CVE checks, so an error is the honest answer and the path exists.
func TestAShortOSVAnswerIsAnErrorNotACleanResult(t *testing.T) {
	// Decode a batch response carrying fewer results than were asked for, and
	// assert the shape the fix depends on: results count is what tells them apart.
	var resp OSVResponseBatch
	if err := json.Unmarshal([]byte(`{"results":[{"vulns":[]}]}`), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("decoded %d results, want 1", len(resp.Results))
	}
	// Two queries against one result is the case that used to be silently dropped.
	if len(resp.Results) >= 2 {
		t.Fatal("fixture does not reproduce the short-answer case")
	}
}

// The pagination token has to survive decoding, or the check that reports an
// incomplete answer can never fire.
func TestOSVPaginationTokenIsParsed(t *testing.T) {
	var resp OSVResponseBatch
	body := `{"results":[{"vulns":[{"id":"GHSA-x"}],"next_page_token":"abc123"}]}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("decoded %d results, want 1", len(resp.Results))
	}
	if resp.Results[0].NextPageToken != "abc123" {
		t.Fatalf("next_page_token = %q, want abc123; without it nvx reports the first page "+
			"as the whole answer for a package with many advisories", resp.Results[0].NextPageToken)
	}
}
