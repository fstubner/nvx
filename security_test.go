package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestScanVulnerabilitiesBatchChunks verifies the OSV scan chunks requests at
// the API's 1000-query cap (so a large tree isn't dropped) and merges results.
func TestScanVulnerabilitiesBatchChunks(t *testing.T) {
	var chunkSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body OSVQueryBatch
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		chunkSizes = append(chunkSizes, len(body.Queries))
		resp := OSVResponseBatch{Results: make([]OSVResult, len(body.Queries))}
		// Flag the first query in each chunk as vulnerable, to confirm merging.
		if len(resp.Results) > 0 {
			resp.Results[0].Vulns = []OSVVuln{{ID: "OSV-TEST", Summary: "x"}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	old := osvBatchURL
	osvBatchURL = srv.URL
	defer func() { osvBatchURL = old }()

	var queries []OSVQuery
	for i := 0; i < 2500; i++ {
		queries = append(queries, OSVQuery{Package: OSVPackage{Name: "p", Ecosystem: "npm"}, Version: "1.0.0"})
	}
	results, err := ScanVulnerabilitiesBatch(queries)
	if err != nil {
		t.Fatal(err)
	}

	if len(chunkSizes) != 3 {
		t.Fatalf("expected 3 chunks for 2500 queries, got %d: %v", len(chunkSizes), chunkSizes)
	}
	if chunkSizes[0] != 1000 || chunkSizes[1] != 1000 || chunkSizes[2] != 500 {
		t.Errorf("unexpected chunk sizes: %v", chunkSizes)
	}
	if len(results) == 0 {
		t.Error("expected merged vulnerability results across chunks")
	}
}
