package nvx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The release list is read to its last page. Only the first 100 releases were
// read, so an older line such as bun@0.8 could not be resolved.
func TestTheBunReleaseListIsReadPastTheFirstPage(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/releases?page=2>; rel="next", <%s/releases?page=2>; rel="last"`, srv.URL, srv.URL))
			fmt.Fprint(w, `[{"tag_name":"bun-v1.1.0"}]`)
		case "2":
			fmt.Fprint(w, `[{"tag_name":"bun-v0.8.1"}]`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer srv.Close()

	prev := bunReleasesURL
	bunReleasesURL = srv.URL + "/releases"
	t.Cleanup(func() { bunReleasesURL = prev })

	versions, err := fetchBunReleasesFromGitHub()
	if err != nil {
		t.Fatal(err)
	}
	if got := matchVersionPrefix("v0.8", versions); got == "" {
		t.Fatalf("v0.8 not found in %v; only the first page was read", versions)
	}
}
