package nvx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The Node.js index, archive and checksums all come from the configured
// mirror. Measured 2026-10-01 against main, nodejs.org was hard-coded, so a
// network that reaches it only through an internal mirror could not install
// Node.js. NVM_NODEJS_ORG_MIRROR and FNM_NODE_DIST_MIRROR are read too, so a
// machine already set up for nvm or fnm needs nothing new.
func TestNodeMirrorServesIndexArchiveAndChecksums(t *testing.T) {
	for _, name := range nodeMirrorVars {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				switch {
				case strings.HasSuffix(r.URL.Path, "/index.json"):
					_, _ = w.Write([]byte(`[{"version":"v99.0.0","lts":false,"files":[]}]`))
				case strings.HasSuffix(r.URL.Path, "/SHASUMS256.txt"):
					// A hash that matches nothing, so the install stops at
					// verification once every file has been fetched.
					_, _ = w.Write([]byte(strings.Repeat("0", 64) + "  node-v99.0.0-" + getOS() + "-" + GetArch() + "." + getExtension() + "\n"))
				default:
					_, _ = w.Write([]byte("not really an archive"))
				}
			}))
			t.Cleanup(srv.Close)

			for _, other := range nodeMirrorVars {
				t.Setenv(other, "")
			}
			t.Setenv(name, srv.URL+"/mirror/dist/")
			home := tempDir(t)
			t.Setenv("NVX_HOME", home)

			err := NodeProvider{}.Install("99.0.0", home)
			if err == nil {
				t.Fatal("the install of a fake archive succeeded")
			}
			mu.Lock()
			got := strings.Join(paths, " ")
			mu.Unlock()
			for _, want := range []string{
				"/mirror/dist/index.json",
				"/mirror/dist/v99.0.0/node-v99.0.0-",
				"/mirror/dist/v99.0.0/SHASUMS256.txt",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the mirror was not asked for %s (it saw: %s; install error: %v)", want, got, err)
				}
			}
		})
	}
}
