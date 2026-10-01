package nvx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The pre-install checks run on what the package manager will install. These
// tests stand in for npm and the registry and check which packages reached
// the checks, and with what.

type fakeRegistryPkg struct {
	age     time.Duration
	scripts bool
	dist    npmDist
}

// verifyWorld is one test's registry, OSV answer and npm.
type verifyWorld struct {
	t       *testing.T
	home    string
	project string

	mu       sync.Mutex
	asked    []string // name@query as the registry was asked
	resolves [][]string
	workDirs []string

	registry map[string]fakeRegistryPkg // by name@version
	lock     string                     // what the fake npm writes
	npmExit  int
}

func newVerifyWorld(t *testing.T, policy string) *verifyWorld {
	t.Helper()
	w := &verifyWorld{t: t, home: tempDir(t), project: tempDir(t), registry: map[string]fakeRegistryPkg{}}
	if err := os.WriteFile(filepath.Join(w.home, "policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.home, "popular_packages.json"), []byte(`["react","lodash"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	chdirForCollapsedScopeTest(t, w.project)
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")

	origResolve, origScan, origDist, origLaunch := resolveNpmPackageDetailsForVerify, scanVulnerabilitiesBatchForVerify, fetchNpmDistForVerify, launchNpmResolution
	t.Cleanup(func() {
		resolveNpmPackageDetailsForVerify, scanVulnerabilitiesBatchForVerify, fetchNpmDistForVerify, launchNpmResolution = origResolve, origScan, origDist, origLaunch
	})
	resolveNpmPackageDetailsForVerify = func(name, query string) (string, time.Time, bool, error) {
		w.mu.Lock()
		w.asked = append(w.asked, name+"@"+query)
		w.mu.Unlock()
		version := query
		if version == "" || strings.ContainsAny(version, "^~<>*x ") {
			version = w.newest(name)
		}
		p, ok := w.registry[name+"@"+version]
		if !ok {
			return "", time.Time{}, false, fmt.Errorf("404 Not Found")
		}
		return version, time.Now().Add(-p.age), p.scripts, nil
	}
	scanVulnerabilitiesBatchForVerify = func([]OSVQuery) (map[string][]OSVVuln, error) { return nil, nil }
	fetchNpmDistForVerify = func(name, version string) (npmDist, error) {
		return w.registry[name+"@"+version].dist, nil
	}
	launchNpmResolution = func(cfg SandboxConfig, contain bool) int {
		w.mu.Lock()
		w.resolves = append(w.resolves, append([]string{}, cfg.Args...))
		w.workDirs = append(w.workDirs, cfg.WorkDir)
		w.mu.Unlock()
		prefix := ""
		for _, a := range cfg.Args {
			if strings.HasPrefix(a, "--prefix=") {
				prefix = strings.TrimPrefix(a, "--prefix=")
			}
		}
		if prefix == "" {
			t.Errorf("the resolution step was not pointed at a scratch directory: %v", cfg.Args)
			return 1
		}
		if w.npmExit != 0 {
			return w.npmExit
		}
		// What npm does with --package-lock-only: both files are rewritten.
		if err := os.WriteFile(filepath.Join(prefix, "package.json"), []byte(`{"rewritten":true}`), 0o600); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(filepath.Join(prefix, "package-lock.json"), []byte(w.lock), 0o600); err != nil {
			t.Error(err)
		}
		return 0
	}
	return w
}

func (w *verifyWorld) newest(name string) string {
	best := ""
	for k := range w.registry {
		if n, v, _ := strings.Cut(k, "@"); n == name && v > best {
			best = v
		}
	}
	return best
}

func (w *verifyWorld) add(spec string, p fakeRegistryPkg) {
	if p.age == 0 {
		p.age = 1000 * time.Hour
	}
	w.registry[spec] = p
}

func (w *verifyWorld) write(name, body string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.project, name), []byte(body), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func (w *verifyWorld) run(cmd string, args ...string) (int, string) {
	w.t.Helper()
	var code int
	out := captureStderrHere(w.t, func() {
		code, _, _ = verifyBeforeRun(verifyRequest{pmCmd: cmd, pmArgs: args, nvxHome: w.home, contain: true,
			launch: SandboxConfig{NvxHome: w.home}})
	})
	return code, out
}

func (w *verifyWorld) askedNames() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var names []string
	for _, a := range w.asked {
		names = append(names, a)
	}
	sort.Strings(names)
	return names
}

// lockJSON renders a lockfileVersion 3 file from path -> entry.
func lockJSON(t *testing.T, root map[string]any, entries map[string]map[string]any) string {
	t.Helper()
	pkgs := map[string]any{"": root}
	for k, v := range entries {
		pkgs[k] = v
	}
	b, err := json.Marshal(map[string]any{"name": "app", "lockfileVersion": 3, "packages": pkgs})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	leftPadURL = "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"
	leftPadSRI = "sha512-XI5MPzVNApjAyhQzphX8BkmKsKUxD4LdyK24iZeQGinBN9yTQT3bFlCBy/aVx2HrNcqQGsdot8ghrjyrvMCoEA=="
	isNumURL   = "https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz"
	isNumSRI   = "sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng=="
)

func tsxLock(t *testing.T) string {
	return lockJSON(t, map[string]any{"dependencies": map[string]any{"tsx": "^4.0.0"}}, map[string]map[string]any{
		"node_modules/tsx":     {"version": "4.0.0", "resolved": "https://registry.npmjs.org/tsx/-/tsx-4.0.0.tgz", "dependencies": map[string]any{"esbuild": "~0.20.0"}},
		"node_modules/esbuild": {"version": "0.20.0", "resolved": "https://registry.npmjs.org/esbuild/-/esbuild-0.20.0.tgz"},
	})
}

func (w *verifyWorld) addTsx(esbuild fakeRegistryPkg) {
	w.add("tsx@4.0.0", fakeRegistryPkg{dist: npmDist{tarball: "https://registry.npmjs.org/tsx/-/tsx-4.0.0.tgz"}})
	esbuild.dist = npmDist{tarball: "https://registry.npmjs.org/esbuild/-/esbuild-0.20.0.tgz"}
	w.add("esbuild@0.20.0", esbuild)
}

// `npm install tsx` installs esbuild, whose postinstall runs. The checks saw
// only tsx, so esbuild's install scripts ran without the prompt that
// `npm install esbuild` gets, and its release age was never looked at.
func TestTransitivePackagesMeetTheSameChecks(t *testing.T) {
	cases := []struct {
		name    string
		esbuild fakeRegistryPkg
		check   string
	}{
		{"install scripts", fakeRegistryPkg{scripts: true}, checkInstallScripts},
		{"release age", fakeRegistryPkg{age: 2 * time.Hour}, checkReleaseAge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
			w.write("package.json", `{"name":"app"}`)
			w.addTsx(tc.esbuild)
			w.lock = tsxLock(t)

			code, out := w.run("npm", "install", "tsx")
			if code == 0 {
				t.Fatalf("installed with esbuild unchecked; the registry was asked for %v\n%s", w.askedNames(), out)
			}
			rec := findRecord(readAuditRecords(t, w.home), "check_refused", tc.check)
			if rec == nil || rec["package"] != "esbuild" {
				t.Fatalf("no %s refusal recorded for esbuild: %v", tc.check, readAuditRecords(t, w.home))
			}
		})
	}
}

// The resolution step runs npm's own resolver on a copy, so the project's
// package.json and lockfile are what they were, and the copy is gone.
func TestResolutionLeavesTheProjectAlone(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"name":"app"}`)
	w.write(".npmrc", "registry=https://registry.npmjs.org/\n")
	w.addTsx(fakeRegistryPkg{})
	w.lock = tsxLock(t)

	code, out := w.run("npm", "install", "tsx", "--no-save")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(w.resolves) != 1 {
		t.Fatalf("npm's resolver ran %d times, want 1", len(w.resolves))
	}
	args := strings.Join(w.resolves[0], " ")
	for _, want := range []string{"install tsx --no-save", "--package-lock-only", "--ignore-scripts", "--save=true", "--dry-run=false"} {
		if !strings.Contains(args, want) {
			t.Errorf("resolution args %q lack %q", args, want)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(w.project, "package.json")); string(got) != `{"name":"app"}` {
		t.Errorf("package.json changed to %s", got)
	}
	if _, err := os.Stat(filepath.Join(w.project, "package-lock.json")); err == nil {
		t.Errorf("a package-lock.json appeared in the project")
	}
	// Both sides with links resolved: on macOS the temp dir is under /var, a
	// link to /private/var, and the launch reports the resolved form.
	gotDir, _ := filepath.EvalSymlinks(w.workDirs[0])
	wantDir, _ := filepath.EvalSymlinks(w.project)
	if !dirsEqual(gotDir, wantDir) {
		t.Errorf("resolution ran in %s, want the project %s", w.workDirs[0], w.project)
	}
	if left, _ := filepath.Glob(filepath.Join(w.project, "node_modules", ".cache", "nvx-resolve-*")); len(left) > 0 {
		t.Errorf("scratch copies left behind: %v", left)
	}
	if got := w.askedNames(); !reflect.DeepEqual(got, []string{"esbuild@0.20.0", "tsx@4.0.0"}) {
		t.Errorf("checked %v, want esbuild and tsx at their resolved versions", got)
	}
}

// A lockfile entry is fetched from its `resolved` URL and checked against its
// `integrity`, and the checks read only its name and version. An entry named
// left-pad@1.3.0 that pointed at another tarball was checked as left-pad and
// installed something else.
func TestALockfileEntryMustMatchTheRegistry(t *testing.T) {
	cases := []struct {
		name      string
		resolved  string
		integrity string
		ok        bool
	}{
		{"genuine", leftPadURL, leftPadSRI, true},
		{"mirror with the registry's hash", "https://mirror.example.invalid/left-pad/-/left-pad-1.3.0.tgz", leftPadSRI, true},
		{"another package's tarball and hash", isNumURL, isNumSRI, false},
		{"right URL, wrong hash", leftPadURL, isNumSRI, false},
		{"another tarball, no hash", isNumURL, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
			w.add("left-pad@1.3.0", fakeRegistryPkg{dist: npmDist{tarball: leftPadURL, integrity: leftPadSRI}})
			entry := map[string]any{"version": "1.3.0", "resolved": tc.resolved}
			if tc.integrity != "" {
				entry["integrity"] = tc.integrity
			}
			w.write("package.json", `{"dependencies":{"left-pad":"1.3.0"}}`)
			w.write("package-lock.json", lockJSON(t, map[string]any{"dependencies": map[string]any{"left-pad": "1.3.0"}},
				map[string]map[string]any{"node_modules/left-pad": entry}))

			code, out := w.run("npm", "ci")
			if tc.ok && code != 0 {
				t.Fatalf("a genuine entry was refused:\n%s", out)
			}
			if !tc.ok {
				if code == 0 {
					t.Fatalf("an entry that does not match the registry was accepted:\n%s", out)
				}
				if rec := findRecord(readAuditRecords(t, w.home), "check_refused", checkLockfileSource); rec == nil || rec["package"] != "left-pad" {
					t.Fatalf("no %s refusal recorded: %v", checkLockfileSource, readAuditRecords(t, w.home))
				}
			}
		})
	}
}

// blocked_packages names packages. A spec that is not a plain registry name
// still names one, or points at one on the registry host, and that name was
// never compared with the list.
func TestTheBlocklistReachesNonRegistrySpecs(t *testing.T) {
	specs := []string{
		"left-pad@https://example.invalid/x.tgz",
		"left-pad@github:someone/left-pad",
		"left-pad@file:../left-pad",
		"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz",
		"@scope/blocked@git+https://example.invalid/x.git",
	}
	for _, spec := range specs {
		t.Run(spec, func(t *testing.T) {
			w := newVerifyWorld(t, `{"typosquatting":{"enabled":false},"blocked_packages":["left-pad","@scope/blocked"]}`)
			var code int
			out := captureStderrHere(t, func() { code, _ = runVerifyInstall([]string{spec}, w.home) })
			if code == 0 {
				t.Fatalf("%s installs a blocked package and was let through:\n%s", spec, out)
			}
			if len(w.asked) > 0 {
				t.Errorf("the registry was asked for %v", w.asked)
			}
		})
	}

	t.Run("lockfile entry from git", func(t *testing.T) {
		w := newVerifyWorld(t, `{"typosquatting":{"enabled":false},"blocked_packages":["left-pad"]}`)
		w.write("package.json", `{"dependencies":{"left-pad":"github:someone/left-pad"}}`)
		w.write("package-lock.json", lockJSON(t, map[string]any{"dependencies": map[string]any{"left-pad": "github:someone/left-pad"}},
			map[string]map[string]any{"node_modules/left-pad": {"version": "1.3.0", "resolved": "git+ssh://git@github.com/someone/left-pad.git#abc"}}))
		if code, out := w.run("npm", "ci"); code == 0 {
			t.Fatalf("a blocked package from git was let through:\n%s", out)
		}
	})
}

// Without a lockfile the checks read package.json, and they checked the newest
// version of each name rather than the declared one, and looked local and
// workspace specs up in the registry by name.
func TestPackageJSONIsReadAsDeclared(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.add("esbuild@0.20.0", fakeRegistryPkg{})
	w.add("esbuild@0.28.2", fakeRegistryPkg{})
	w.write("package.json", `{"dependencies":{
		"esbuild":"0.20.0",
		"evil-pkg":"file:./evil-pkg",
		"shared":"workspace:*",
		"linked":"link:../linked",
		"fromgit":"github:someone/fromgit"}}`)

	code, out := w.run("pnpm", "install")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := w.askedNames(); !reflect.DeepEqual(got, []string{"esbuild@0.20.0"}) {
		t.Fatalf("the registry was asked for %v, want only esbuild at its declared version", got)
	}
}

// Commands that fetch or run packages and were contained, but checked nothing.
func TestRefreshAndRunnerVerbsAreChecked(t *testing.T) {
	cases := []struct {
		cmd  string
		args []string
		want []string
	}{
		{"npm", []string{"exec", "cowsay"}, []string{"cowsay"}},
		{"npm", []string{"x", "-p", "cowsay@1.5.0", "cowsay", "hi"}, []string{"cowsay@1.5.0"}},
		{"npm", []string{"exec", "--", "cowsay", "hi"}, []string{"cowsay"}},
		{"npm", []string{"create", "vite"}, []string{"create-vite"}},
		{"npm", []string{"init", "vite@5", "app"}, []string{"create-vite@5"}},
		{"npm", []string{"init", "@scope"}, []string{"@scope/create"}},
		{"npm", []string{"create", "@scope/thing@2"}, []string{"@scope/create-thing@2"}},
		{"pnpm", []string{"dlx", "cowsay"}, []string{"cowsay"}},
		{"pnpm", []string{"create", "vite"}, []string{"create-vite"}},
		{"yarn", []string{"dlx", "cowsay"}, []string{"cowsay"}},
		{"yarn", []string{"create", "vite"}, []string{"create-vite"}},
		{"bun", []string{"x", "cowsay"}, []string{"cowsay"}},
		{"bun", []string{"create", "vite"}, []string{"create-vite"}},
		{"pnpm", []string{"update", "left-pad"}, []string{"left-pad"}},
	}
	for _, tc := range cases {
		if got := detectShimPackagesForVerification(tc.cmd, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v: checks %v, want %v", tc.cmd, tc.args, got, tc.want)
		}
	}

	// With no package named, these read the project.
	dir := tempDir(t)
	chdirForCollapsedScopeTest(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"left-pad":"1.3.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{{"pnpm", "update"}, {"pnpm", "rebuild"}, {"yarn", "upgrade"}, {"bun", "update"}, {"pnpm", "dedupe"}} {
		if got := detectShimPackagesForVerification(c[0], c[1:]); !reflect.DeepEqual(got, []string{"left-pad@1.3.0"}) {
			t.Errorf("%v: checks %v, want left-pad@1.3.0", c, got)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(lockV3), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := detectShimPackagesForVerification("npm", []string{"rebuild"}); !reflect.DeepEqual(got, []string{"left-pad@1.3.0", "right-pad@2.0.0"}) {
		t.Errorf("npm rebuild: checks %v, want the installed set", got)
	}
}

// npm update and dedupe change the tree, so npm's resolver says what they
// will install.
func TestNpmUpdateAndDedupeAreResolved(t *testing.T) {
	for _, verb := range []string{"update", "dedupe"} {
		t.Run(verb, func(t *testing.T) {
			w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
			w.write("package.json", `{"dependencies":{"tsx":"^4.0.0"}}`)
			w.addTsx(fakeRegistryPkg{scripts: true})
			w.lock = tsxLock(t)
			if code, out := w.run("npm", verb); code == 0 {
				t.Fatalf("npm %s ran esbuild's install scripts unasked:\n%s", verb, out)
			}
			if len(w.resolves) != 1 || w.resolves[0][0] != verb {
				t.Fatalf("resolution runs: %v", w.resolves)
			}
		})
	}
}

// When npm's resolver fails, the run is asked about like any check whose
// lookup failed: refused with nobody to ask, and recorded.
func TestAFailedResolutionIsAskedAbout(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"name":"app"}`)
	w.addTsx(fakeRegistryPkg{})
	w.npmExit = 1
	code, out := w.run("npm", "install", "tsx")
	if code == 0 {
		t.Fatalf("a failed resolution was not asked about:\n%s", out)
	}
	if rec := findRecord(readAuditRecords(t, w.home), "check_refused", checkResolution); rec == nil {
		t.Fatalf("no %s refusal recorded: %v", checkResolution, readAuditRecords(t, w.home))
	}

	t.Setenv("NVX_YES", "1")
	if code, out := w.run("npm", "install", "tsx"); code != 0 {
		t.Fatalf("an approved failed resolution was refused:\n%s", out)
	}
	if got := w.askedNames(); !reflect.DeepEqual(got, []string{"tsx@"}) {
		t.Fatalf("after an approved failure the checks saw %v, want the named package", got)
	}
}

// A lockfile lists every platform's optional binaries, and npm installs only
// this platform's.
func TestAnotherPlatformsOptionalPackageIsNotChecked(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	here, there := nodePlatformName(runtime.GOOS), "aix"
	if here == "aix" {
		there = "sunos"
	}
	w.add("@esbuild/here@0.20.0", fakeRegistryPkg{})
	w.add("@esbuild/there@0.20.0", fakeRegistryPkg{})
	w.write("package.json", `{"name":"app"}`)
	w.write("package-lock.json", lockJSON(t, map[string]any{}, map[string]map[string]any{
		"node_modules/@esbuild/here":  {"version": "0.20.0", "optional": true, "os": []string{here}},
		"node_modules/@esbuild/there": {"version": "0.20.0", "optional": true, "os": []string{there}},
	}))
	if code, out := w.run("npm", "ci"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := w.askedNames(); !reflect.DeepEqual(got, []string{"@esbuild/here@0.20.0"}) {
		t.Fatalf("checked %v, want only this platform's package", got)
	}
}

// A bare `npm install` whose lockfile matches package.json installs the
// lockfile, which is read as it is. One that does not match is resolved.
func TestBareInstallResolvesOnlyWhenTheLockfileIsStale(t *testing.T) {
	lock := lockJSON(t, map[string]any{"dependencies": map[string]any{"left-pad": "1.3.0"}},
		map[string]map[string]any{"node_modules/left-pad": {"version": "1.3.0", "resolved": leftPadURL, "integrity": leftPadSRI}})
	for _, tc := range []struct {
		pkg     string
		resolve bool
	}{
		{`{"dependencies":{"left-pad":"1.3.0"}}`, false},
		{`{"dependencies":{"left-pad":"1.3.0","tsx":"^4.0.0"}}`, true},
	} {
		w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
		w.add("left-pad@1.3.0", fakeRegistryPkg{dist: npmDist{tarball: leftPadURL, integrity: leftPadSRI}})
		w.addTsx(fakeRegistryPkg{})
		w.write("package.json", tc.pkg)
		w.write("package-lock.json", lock)
		w.lock = tsxLock(t)
		if code, out := w.run("npm", "install"); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if got := len(w.resolves) > 0; got != tc.resolve {
			t.Errorf("package.json %s: resolved = %v, want %v", tc.pkg, got, tc.resolve)
		}
	}
}

// A workspace project cannot be copied as two files, so it is not resolved,
// and the run says what that leaves unchecked.
func TestAWorkspaceProjectSaysTransitivePackagesAreNotResolved(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.add("left-pad@1.3.0", fakeRegistryPkg{})
	w.write("package.json", `{"workspaces":["packages/*"]}`)
	code, out := w.run("npm", "install", "left-pad@1.3.0")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(w.resolves) != 0 {
		t.Fatalf("resolved a workspace project: %v", w.resolves)
	}
	if !strings.Contains(out, "dependencies") || !strings.Contains(out, "workspaces") {
		t.Fatalf("nothing said the dependencies of left-pad go unchecked:\n%s", out)
	}
}

// `npm ci` in a docs site printed "Failed to parse package-lock.json for
// verification" and then checked package.json instead, so @astrojs/starlight
// was checked at the newest version its range allows, 0.42.5, and refused as
// too new, while the lockfile pins 0.42.1. The lockfile below has that file's
// shape: string dependency maps in `packages`, peerDependenciesMeta, engines,
// bin, funding, and os and cpu lists.
func TestCleanInstallChecksTheLockedVersions(t *testing.T) {
	siteLock := `{
  "name": "site",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "site",
      "dependencies": {"@astrojs/starlight": "^0.42.1"},
      "devDependencies": {"sharp": "^0.35.4"},
      "engines": {"node": ">=22"}
    },
    "node_modules/@astrojs/starlight": {
      "version": "0.42.1",
      "resolved": "https://registry.npmjs.org/@astrojs/starlight/-/starlight-0.42.1.tgz",
      "integrity": "sha512-ABS/A0IfbJeMbuhCKVkrAcO3EWxhpeLveFuL28+7+BQje7ZHa9OXZbUoiLXdYUhH7XFaiAQjAg44RiF2uuZPJw==",
      "license": "MIT",
      "dependencies": {"klona": "^2.0.6"},
      "peerDependencies": {"astro": "^7.2.10"},
      "peerDependenciesMeta": {"astro": {"optional": true}},
      "bin": {"starlight": "bin.js"},
      "funding": [{"type": "github", "url": "https://example.invalid"}],
      "engines": {"node": ">=22"}
    },
    "node_modules/@img/sharp-elsewhere": {
      "version": "0.35.4",
      "optional": true,
      "os": ["!` + nodePlatformName(runtime.GOOS) + `"],
      "cpu": "x64"
    }
  }
}`
	young := fakeRegistryPkg{age: 2 * time.Hour}

	t.Run("locked version", func(t *testing.T) {
		w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
		w.add("@astrojs/starlight@0.42.1", fakeRegistryPkg{dist: npmDist{
			tarball:   "https://registry.npmjs.org/@astrojs/starlight/-/starlight-0.42.1.tgz",
			integrity: "sha512-ABS/A0IfbJeMbuhCKVkrAcO3EWxhpeLveFuL28+7+BQje7ZHa9OXZbUoiLXdYUhH7XFaiAQjAg44RiF2uuZPJw=="}})
		w.add("@astrojs/starlight@0.42.5", young)
		w.write("package.json", `{"dependencies":{"@astrojs/starlight":"^0.42.1"},"devDependencies":{"sharp":"^0.35.4"}}`)
		w.write("package-lock.json", siteLock)
		code, out := w.run("npm", "ci")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if got := w.askedNames(); !reflect.DeepEqual(got, []string{"@astrojs/starlight@0.42.1"}) {
			t.Fatalf("checked %v, want the locked @astrojs/starlight@0.42.1 only", got)
		}
	})

	t.Run("unreadable lockfile", func(t *testing.T) {
		w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
		w.add("@astrojs/starlight@0.42.5", young)
		w.write("package.json", `{"dependencies":{"@astrojs/starlight":"^0.42.1"}}`)
		w.write("package-lock.json", `{"lockfileVersion": 3, "packages": {"node_modules/x": {"version": 1}}}`)
		code, out := w.run("npm", "ci")
		if code == 0 {
			t.Fatalf("an unreadable lockfile was not asked about:\n%s", out)
		}
		if got := w.askedNames(); len(got) > 0 {
			t.Fatalf("checked %v from package.json in place of the lockfile", got)
		}
		if findRecord(readAuditRecords(t, w.home), "check_refused", checkLockfileUnreadable) == nil {
			t.Fatalf("no %s refusal recorded: %v", checkLockfileUnreadable, readAuditRecords(t, w.home))
		}
	})
}

// npm's tree holds the whole project, and npm leaves what is installed at the
// same version as it is. `npm install left-pad` in a large project checks
// left-pad and what it brings in, and not every package already there.
func TestResolutionSkipsWhatIsAlreadyInstalled(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.write("package.json", `{"dependencies":{"tsx":"^4.0.0"}}`)
	w.addTsx(fakeRegistryPkg{})
	w.lock = tsxLock(t)
	installed := filepath.Join(w.project, "node_modules", "tsx")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "package.json"), []byte(`{"name":"tsx","version":"4.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := w.run("npm", "install", "esbuild"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := w.askedNames(); !reflect.DeepEqual(got, []string{"esbuild@0.20.0"}) {
		t.Fatalf("checked %v, want only esbuild, which is not installed yet", got)
	}
}
