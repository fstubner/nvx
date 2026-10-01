package nvx

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// privateRegistryProject sets up a project whose .npmrc sends @corp to a fake
// private registry, a home whose .npmrc holds userNpmrc, and stand-ins for every
// public service the checks can reach. It returns the private registry's hit
// counter and a counter of everything that reached a public stand-in.
func privateRegistryProject(t *testing.T, wantToken, userNpmrc string) (nvxHome string, privateHits, publicHits *int32) {
	t.Helper()
	privateHits, publicHits = new(int32), new(int32)

	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(privateHits, 1)
		if wantToken != "" && r.Header.Get("Authorization") != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.EscapedPath(), "@corp%2Ftool") && !strings.Contains(r.URL.EscapedPath(), "@corp%2ftool") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
		_, _ = w.Write([]byte(`{"dist-tags":{"latest":"1.0.0"},"time":{"1.0.0":"` + old + `"},` +
			`"versions":{"1.0.0":{"dist":{"tarball":"` + "http://" + r.Host + `/@corp/tool/-/tool-1.0.0.tgz"}}}}`))
	}))
	t.Cleanup(private.Close)

	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(publicHits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(public.Close)

	origPublic, origOSV, origDownloads := publicNpmRegistry, osvQueryBatchURL, weeklyDownloads
	publicNpmRegistry = public.URL + "/"
	osvQueryBatchURL = public.URL + "/v1/querybatch"
	weeklyDownloads = func(string) (int, error) {
		atomic.AddInt32(publicHits, 1)
		return 0, nil
	}
	t.Cleanup(func() {
		publicNpmRegistry, osvQueryBatchURL, weeklyDownloads = origPublic, origOSV, origDownloads
		packuments.Range(func(k, _ any) bool { packuments.Delete(k); return true })
	})

	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("npm_config_userconfig", "")
	t.Setenv("npm_config_registry", "")
	if userNpmrc != "" {
		userNpmrc = strings.ReplaceAll(userNpmrc, "PRIVATE", strings.TrimPrefix(private.URL, "http:"))
		if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte(userNpmrc), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	proj := tempDir(t)
	if err := os.WriteFile(filepath.Join(proj, "package.json"), []byte(`{"name":"app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".npmrc"), []byte("@corp:registry="+private.URL+"/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevWd, _ := os.Getwd()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWd) })

	nvxHome = tempDir(t)
	// A popular list with a near neighbour, so the typosquat check would ask for
	// download counts if it ran for this package.
	if err := os.WriteFile(filepath.Join(nvxHome, "popular_packages.json"), []byte(`["@corp/tools"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NVX_YES", "")
	t.Setenv("NVX_NONINTERACTIVE", "1")
	return nvxHome, privateHits, publicHits
}

// A scoped package from the project's private registry is checked there, and
// nothing about it reaches the public registry, the download counts or OSV.
// Measured 2026-10-01 against main, the lookup went to registry.npmjs.org, came
// back 404, and with nobody to answer the install was refused.
func TestScopedPackageIsCheckedOnItsPrivateRegistryOnly(t *testing.T) {
	nvxHome, privateHits, publicHits := privateRegistryProject(t, "", "")

	var code int
	out := captureStderrHere(t, func() {
		code, _ = runVerifyInstall([]string{"@corp/tool@1.0.0"}, nvxHome)
	})
	if code != 0 {
		t.Fatalf("the private package was refused (exit %d):\n%s", code, out)
	}
	if atomic.LoadInt32(privateHits) == 0 {
		t.Fatal("the private registry was never asked about @corp/tool")
	}
	if n := atomic.LoadInt32(publicHits); n != 0 {
		t.Fatalf("%d requests about the private package reached a public service", n)
	}
	if !strings.Contains(out, "not the public npm registry") {
		t.Fatalf("the run did not say which checks were skipped:\n%s", out)
	}
	audit, _ := os.ReadFile(filepath.Join(nvxHome, "audit.log"))
	if !strings.Contains(string(audit), `"check_skipped"`) || !strings.Contains(string(audit), checkPublicRegistryOnly) {
		t.Fatalf("the skipped checks were not recorded:\n%s", audit)
	}
}

// A registry that needs a token for metadata gets the user's _authToken from
// ~/.npmrc on nvx's own request. The token stays out of the sandbox's
// environment, the output and the audit log.
func TestPrivateRegistryTokenIsUsedForMetadataAndKeptFromTheSandbox(t *testing.T) {
	const token = "s3cret-npm-token-for-test"
	nvxHome, privateHits, publicHits := privateRegistryProject(t, token, "PRIVATE/:_authToken="+token+"\n")

	var code int
	out := captureStderrHere(t, func() {
		code, _ = runVerifyInstall([]string{"@corp/tool@1.0.0"}, nvxHome)
	})
	if code != 0 {
		t.Fatalf("the private package was refused with a token configured (exit %d):\n%s", code, out)
	}
	if atomic.LoadInt32(privateHits) == 0 || atomic.LoadInt32(publicHits) != 0 {
		t.Fatalf("private hits %d, public hits %d", atomic.LoadInt32(privateHits), atomic.LoadInt32(publicHits))
	}
	if strings.Contains(out, token) {
		t.Fatal("the token was printed")
	}
	audit, _ := os.ReadFile(filepath.Join(nvxHome, "audit.log"))
	if strings.Contains(string(audit), token) {
		t.Fatal("the token was written to the audit log")
	}
	for _, e := range scrubEnvironment(tempDir(t)) {
		if strings.Contains(e, token) {
			t.Fatalf("the token reached the sandbox environment: %s", e)
		}
	}
}

// Without a token, a registry that answers 401 is "could not check", which
// goes to the prompt and is refused when nobody is there.
func TestPrivateRegistryWithoutTokenIsCouldNotCheck(t *testing.T) {
	nvxHome, _, publicHits := privateRegistryProject(t, "needed", "")

	var code int
	out := captureStderrHere(t, func() {
		code, _ = runVerifyInstall([]string{"@corp/tool@1.0.0"}, nvxHome)
	})
	if code == 0 {
		t.Fatalf("a registry answering 401 let the install through unchecked:\n%s", out)
	}
	if !strings.Contains(out, "_authToken") {
		t.Fatalf("the refusal did not say a token was missing:\n%s", out)
	}
	if atomic.LoadInt32(publicHits) != 0 {
		t.Fatal("a public service was asked about the private package")
	}
}

// A contained npm reads only the project's .npmrc, so a scope mapping in
// ~/.npmrc does not apply to it and must not steer the checks to a package npm
// will not fetch. Tokens are still read from ~/.npmrc for nvx's own requests.
func TestContainedRegistryConfigIgnoresTheUserNpmrcRegistries(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("npm_config_registry", "https://env.example/")
	user := "@corp:registry=https://user.example/\n//proj.example/:_authToken=tok\n"
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := tempDir(t)
	if err := os.WriteFile(filepath.Join(proj, ".npmrc"), []byte("@other:registry=https://proj.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	contained := loadNpmRegistryConfig(proj, true)
	if got := contained.registryFor("@corp/x"); got != publicNpmRegistry {
		t.Errorf("contained: @corp/x resolved to %s, want the public registry", got)
	}
	if got := contained.registryFor("lodash"); got != publicNpmRegistry {
		t.Errorf("contained: npm_config_registry applied (%s), but a contained npm does not see it", got)
	}
	if got := contained.registryFor("@other/y"); got != "https://proj.example/" {
		t.Errorf("contained: @other/y resolved to %s", got)
	}
	if contained.tokenFor("https://proj.example/") != "tok" {
		t.Error("contained: the user's token for the project's registry was not found")
	}

	host := loadNpmRegistryConfig(proj, false)
	if got := host.registryFor("@corp/x"); got != "https://user.example/" {
		t.Errorf("uncontained: @corp/x resolved to %s", got)
	}
	if got := host.registryFor("lodash"); got != "https://env.example/" {
		t.Errorf("uncontained: npm_config_registry was not applied (%s)", got)
	}
}
