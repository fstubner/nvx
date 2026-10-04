package nvx

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// A typosquat is a name someone typed wrongly. A dependency that arrives with a
// package was named by that package's author, so the typosquat check, and the
// download lookup behind it, run only on what the user chose.
//
// Measured 2026-10-04: `npm ci` over a 415-package tree got HTTP 429 from
// api.npmjs.org, the lookups fell back to name similarity, and the transitive
// package `regex` was refused as a typosquat.

// downloadsWorld makes every download lookup fail with a 429 and counts them.
type downloadsWorld struct {
	mu    sync.Mutex
	asked []string
}

func (d *downloadsWorld) install(t *testing.T) {
	t.Helper()
	orig := weeklyDownloads
	t.Cleanup(func() { weeklyDownloads = orig })
	weeklyDownloads = func(name string) (int, error) {
		d.mu.Lock()
		d.asked = append(d.asked, name)
		d.mu.Unlock()
		return 0, fmt.Errorf("HTTP 429 Too Many Requests")
	}
}

func (d *downloadsWorld) names() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.asked, ",")
}

func tarballOf(name, version string) string {
	return fmt.Sprintf("https://registry.npmjs.org/%s/-/%s-%s.tgz", name, name, version)
}

func (w *verifyWorld) addPlain(name, version string) {
	w.add(name+"@"+version, fakeRegistryPkg{dist: npmDist{tarball: tarballOf(name, version)}})
}

func lockEntry(name, version string) map[string]any {
	return map[string]any{"version": version, "resolved": tarballOf(name, version)}
}

// `lodahs` is one edit from the popular `lodash` and `reakt` one from `react`.
// In a tree where the user's package.json names reakt and the resolved tree
// also holds lodahs, only reakt is the user's choice.
func TestTyposquatChecksOnlyWhatTheUserChose(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string // written to the project
		cmd      []string
		lockRoot map[string]any
	}{
		{"bare install resolved by npm", map[string]string{"package.json": `{"name":"app","dependencies":{"reakt":"^1.0.0"}}`},
			[]string{"install"}, map[string]any{"dependencies": map[string]any{"reakt": "^1.0.0"}}},
		{"named install", map[string]string{"package.json": `{"name":"app"}`},
			[]string{"install", "reakt"}, map[string]any{"dependencies": map[string]any{"reakt": "^1.0.0"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newVerifyWorld(t, `{}`)
			var d downloadsWorld
			d.install(t)
			for f, body := range tc.files {
				w.write(f, body)
			}
			w.addPlain("reakt", "1.0.0")
			w.addPlain("lodahs", "1.0.0")
			w.lock = lockJSON(t, tc.lockRoot, map[string]map[string]any{
				"node_modules/reakt":  lockEntry("reakt", "1.0.0"),
				"node_modules/lodahs": lockEntry("lodahs", "1.0.0"),
			})

			code, out := w.run("npm", tc.cmd...)
			if code == 0 {
				t.Fatalf("the user's own reakt was not checked as a typosquat\n%s", out)
			}
			rec := findRecord(readAuditRecords(t, w.home), "check_refused", checkTyposquat)
			if rec == nil || rec["package"] != "reakt" {
				t.Fatalf("want the typosquat refusal on reakt, got %v", readAuditRecords(t, w.home))
			}
			if got := d.names(); strings.Contains(got, "lodahs") {
				t.Errorf("the transitive lodahs was looked up on api.npmjs.org: %s", got)
			}
		})
	}
}

// With the user's own package safe, a transitive near-match passes and makes no
// lookup at all, on each route a tree reaches the checks by.
func TestTransitivePackagesMakeNoDownloadLookups(t *testing.T) {
	cases := []struct {
		name  string
		cmd   []string
		root  map[string]any
		files map[string]string
	}{
		{"named install", []string{"install", "left-pad"}, map[string]any{"dependencies": map[string]any{"left-pad": "^1.3.0"}},
			map[string]string{"package.json": `{"name":"app"}`}},
		{"bare install resolved by npm", []string{"install"}, map[string]any{"dependencies": map[string]any{"left-pad": "^1.3.0"}},
			map[string]string{"package.json": `{"name":"app","dependencies":{"left-pad":"^1.3.0"}}`}},
		// The lockfile matches package.json, so npm ci reads it as it is.
		{"npm ci from the lockfile", []string{"ci"}, map[string]any{"dependencies": map[string]any{"left-pad": "^1.3.0"}},
			map[string]string{"package.json": `{"name":"app","dependencies":{"left-pad":"^1.3.0"}}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newVerifyWorld(t, `{}`)
			var d downloadsWorld
			d.install(t)
			for f, body := range tc.files {
				w.write(f, body)
			}
			w.addPlain("left-pad", "1.3.0")
			w.addPlain("lodahs", "1.0.0")
			lock := lockJSON(t, tc.root, map[string]map[string]any{
				"node_modules/left-pad": lockEntry("left-pad", "1.3.0"),
				"node_modules/lodahs":   lockEntry("lodahs", "1.0.0"),
			})
			w.lock = lock
			w.write("package-lock.json", lock)

			code, out := w.run("npm", tc.cmd...)
			if code != 0 {
				t.Fatalf("a transitive near-match was refused (exit %d)\n%s", code, out)
			}
			if got := d.names(); got != "" {
				t.Errorf("transitive packages made download lookups: %s", got)
			}
			if got := w.askedNames(); !strings.Contains(strings.Join(got, ","), "lodahs@") {
				t.Errorf("the other checks no longer cover lodahs, the registry was asked for %v", got)
			}
		})
	}
}

// A run asks about a popular package once, however many names sit near it.
func TestDownloadLookupsAreRememberedWithinARun(t *testing.T) {
	var asked []string
	lookup := memoizeDownloads(func(name string) (int, error) {
		asked = append(asked, name)
		return 0, fmt.Errorf("HTTP 429 Too Many Requests")
	})
	list := []string{"react"}
	for _, name := range []string{"reakt", "reast", "reakt"} {
		assessTyposquatWith(name, list, 2, lookup)
	}
	if got := strings.Join(asked, ","); got != "reakt,react,reast" {
		t.Errorf("lookups were %s, want one each for reakt, react and reast", got)
	}
}
