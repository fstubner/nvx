package nvx

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1" // #nosec G505 -- the registry's shasum field is SHA-1
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNpmRegistry is a registry real npm can install from. It serves packuments
// and tarballs for what a test publishes, and logs what was asked, so a test can
// change what the registry holds between two runs of npm and see what each asked.
type fakeNpmRegistry struct {
	t   *testing.T
	srv *httptest.Server

	mu   sync.Mutex
	pkgs map[string]*fakeNpmPackage
	log  []fakeNpmRequest
}

type fakeNpmPackage struct {
	versions map[string]fakeNpmVersion
	tags     map[string]string
}

type fakeNpmVersion struct {
	deps    map[string]string
	scripts map[string]string
	tgz     []byte
}

// fakeNpmRequest is one request, with the program that made it.
type fakeNpmRequest struct {
	path, agent string
}

// byNpm reports a request that npm itself made, as opposed to nvx's own checks.
func (r fakeNpmRequest) byNpm() bool { return strings.HasPrefix(r.agent, "npm/") }

// forPackument reports a request for a package's metadata rather than a tarball.
func (r fakeNpmRequest) forPackument() bool { return !strings.Contains(r.path, "/-/") }

func newFakeNpmRegistry(t *testing.T) *fakeNpmRegistry {
	t.Helper()
	r := &fakeNpmRegistry{t: t, pkgs: map[string]*fakeNpmPackage{}}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeNpmRegistry) url() string { return r.srv.URL + "/" }

// publish adds a version and makes it the latest.
func (r *fakeNpmRegistry) publish(name, version string, deps map[string]string) {
	r.t.Helper()
	r.publishWith(name, version, deps, nil)
}

func (r *fakeNpmRegistry) publishWith(name, version string, deps, scripts map[string]string) {
	r.t.Helper()
	manifest := map[string]any{"name": name, "version": version}
	if len(deps) > 0 {
		manifest["dependencies"] = deps
	}
	if len(scripts) > 0 {
		manifest["scripts"] = scripts
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		r.t.Fatal(err)
	}
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{
		{"package/package.json", string(body)},
		{"package/index.js", "module.exports = " + fmt.Sprintf("%q", version) + ";\n"},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), ModTime: time.Unix(0, 0)}); err != nil {
			r.t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			r.t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		r.t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		r.t.Fatal(err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pkgs[name]
	if p == nil {
		p = &fakeNpmPackage{versions: map[string]fakeNpmVersion{}, tags: map[string]string{}}
		r.pkgs[name] = p
	}
	p.versions[version] = fakeNpmVersion{deps: deps, scripts: scripts, tgz: tgz.Bytes()}
	p.tags["latest"] = version
}

// requests returns what has been asked so far.
func (r *fakeNpmRegistry) requests() []fakeNpmRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fakeNpmRequest(nil), r.log...)
}

func (r *fakeNpmRegistry) serve(w http.ResponseWriter, req *http.Request) {
	path := req.URL.EscapedPath()
	r.mu.Lock()
	r.log = append(r.log, fakeNpmRequest{path: path, agent: req.UserAgent()})
	r.mu.Unlock()

	name, tarball, isTarball := strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/-/")
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pkgs[name]
	if p == nil {
		http.NotFound(w, req)
		return
	}
	if isTarball {
		for version, v := range p.versions {
			if tarball == name+"-"+version+".tgz" {
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(v.tgz)
				return
			}
		}
		http.NotFound(w, req)
		return
	}

	versions := map[string]any{}
	times := map[string]string{"created": "2020-01-01T00:00:00.000Z", "modified": "2020-01-01T00:00:00.000Z"}
	for version, v := range p.versions {
		sum := sha512.Sum512(v.tgz)
		sha := sha1.Sum(v.tgz) // #nosec G401 -- the registry's shasum field is SHA-1
		manifest := map[string]any{
			"name": name, "version": version,
			"dist": map[string]string{
				"tarball":   r.srv.URL + "/" + name + "/-/" + name + "-" + version + ".tgz",
				"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:]),
				"shasum":    hex.EncodeToString(sha[:]),
			},
		}
		if len(v.deps) > 0 {
			manifest["dependencies"] = v.deps
		}
		if len(v.scripts) > 0 {
			manifest["scripts"] = v.scripts
		}
		versions[version] = manifest
		times[version] = "2020-01-02T03:04:05.000Z"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": name, "dist-tags": p.tags, "versions": versions, "time": times,
	})
}

// reset forgets every package and every request.
func (r *fakeNpmRegistry) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pkgs = map[string]*fakeNpmPackage{}
	r.log = nil
}
