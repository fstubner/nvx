package nvx

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The fixtures under testdata/lockfiles were written by the package managers
// themselves, for one project: is-odd@3.0.1 (which brings in is-number@6.0.0),
// lp as an alias of left-pad@1.3.0, chokidar@3.6.0 (fsevents for macOS among
// what it brings in), react and react-dom, @sindresorhus/is, a file: folder,
// ms as a dev dependency, and a workspace member, packages/app, depending on
// is-even@1.0.0. Generated 2026-10-06 with `install --lockfile-only` or its
// equivalent:
//
//	pnpm-v5      pnpm 7.33   lockfileVersion 5.4
//	pnpm-v6      pnpm 8.15   lockfileVersion 6.0
//	pnpm-v9      pnpm 10.34  lockfileVersion 9.0 (pnpm 9, 11.28 and 12.9 wrote the same bytes)
//	pnpm-v9-env  pnpm 12.9   two documents: pnpm itself, from devEngines, then the project
//	yarn-v1      yarn 1.22.22
//	yarn-berry   yarn 4 (__metadata version 10)
//	bun          bun 1.4.2   lockfileVersion 2

var fixtureRegistryPackages = []string{
	"@sindresorhus/is@4.6.0", "anymatch@3.1.3", "binary-extensions@2.3.0", "braces@3.0.3", "chokidar@3.6.0",
	"fill-range@7.1.1", "glob-parent@5.1.2", "is-binary-path@2.1.0", "is-buffer@1.1.6", "is-even@1.0.0",
	"is-extglob@2.1.1", "is-glob@4.0.3", "is-number@3.0.0", "is-number@6.0.0", "is-number@7.0.0",
	"is-odd@0.1.2", "is-odd@3.0.1", "js-tokens@4.0.0", "kind-of@3.2.2", "left-pad@1.3.0",
	"loose-envify@1.4.0", "ms@2.1.3", "normalize-path@3.0.0", "picomatch@2.3.2", "react-dom@18.3.1",
	"react@18.3.1", "readdirp@3.6.0", "scheduler@0.23.2", "to-regex-range@5.0.1",
}

// Yarn 4 adds node-gyp, which it gives fsevents because fsevents has an
// install script, and what node-gyp brings in. None of them carries an os
// condition, so they are checked everywhere.
var berryNodeGyp = []string{
	"@isaacs/fs-minipass@4.0.1", "abbrev@5.0.0", "chownr@3.0.0", "env-paths@2.2.1", "exponential-backoff@3.1.3",
	"fdir@6.5.0", "graceful-fs@4.2.11", "isexe@4.0.0", "minipass@7.1.3", "minizlib@3.1.0", "node-gyp@13.1.0",
	"nopt@10.0.1", "picomatch@4.0.7", "proc-log@7.0.0", "semver@7.8.5", "tar@7.5.22", "tinyglobby@0.2.17",
	"undici@8.11.2", "which@7.0.0", "yallist@5.0.0",
}

type lockFixture struct {
	dir, cmd, file string
}

var lockFixtures = []lockFixture{
	{"pnpm-v5", "pnpm", "pnpm-lock.yaml"},
	{"pnpm-v6", "pnpm", "pnpm-lock.yaml"},
	{"pnpm-v9", "pnpm", "pnpm-lock.yaml"},
	{"yarn-v1", "yarn", "yarn.lock"},
	{"yarn-berry", "yarn", "yarn.lock"},
	{"bun", "bun", "bun.lock"},
}

// fixtureRoot is resolved before any test changes directory.
var fixtureRoot, _ = filepath.Abs(filepath.Join("testdata", "lockfiles"))

func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(fixtureRoot, name)
}

// copyFixture copies a fixture project into dst.
func copyFixture(t *testing.T, name, dst string) {
	t.Helper()
	src := fixtureDir(t, name)
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readFixtureLock(t *testing.T, f lockFixture) (pmLockfile, string) {
	t.Helper()
	dir := fixtureDir(t, f.dir)
	lock, ok, err := readPMLockfile(f.cmd, dir)
	if err != nil || !ok {
		t.Fatalf("%s: ok=%v err=%v", f.dir, ok, err)
	}
	return lock, dir
}

func specsOf(targets []verifyTarget) []string {
	out := targetSpecs(targets)
	sort.Strings(out)
	return out
}

// Every format lists the same project, and the checks get the same packages
// from each, for the platform the install runs on.
func TestEachLockfileFormatListsWhatItInstalls(t *testing.T) {
	linux, mac := nodePlatform{"linux", "x64"}, nodePlatform{"darwin", "arm64"}
	for _, f := range lockFixtures {
		t.Run(f.dir, func(t *testing.T) {
			lock, dir := readFixtureLock(t, f)
			withFsevents := append(append([]string{}, fixtureRegistryPackages...), "fsevents@2.3.3")
			sort.Strings(withFsevents)
			wantLinux, wantMac := fixtureRegistryPackages, withFsevents
			switch f.dir {
			case "yarn-v1":
				// Yarn 1 records no os, so macOS's fsevents is checked everywhere.
				wantLinux = withFsevents
			case "yarn-berry":
				wantLinux = append(append([]string{}, fixtureRegistryPackages...), berryNodeGyp...)
				wantMac = append(append([]string{}, withFsevents...), berryNodeGyp...)
			}
			for _, tc := range []struct {
				platform nodePlatform
				want     []string
			}{{linux, wantLinux}, {mac, wantMac}} {
				want := append([]string{}, tc.want...)
				sort.Strings(want)
				if got := specsOf(pmLockTargets(lock, tc.platform, dir)); !reflect.DeepEqual(got, want) {
					t.Errorf("%s/%s: checked\n  %v\nwant\n  %v", tc.platform.os, tc.platform.cpu, got, want)
				}
			}

			for _, tg := range pmLockTargets(lock, linux, dir) {
				name := targetPackageName(tg)
				// The project's own choices keep the typosquat check, and what
				// they bring in skips it (#132). left-pad is chosen through
				// the lp alias, and is-odd and is-even by the workspace member.
				chosen := containsString([]string{"@sindresorhus/is", "chokidar", "is-odd", "left-pad", "react", "react-dom", "ms", "is-even"}, name)
				if tg.transitive == chosen {
					t.Errorf("%s: transitive=%v", tg.spec, tg.transitive)
				}
				if !tg.fromLockfile() {
					t.Errorf("%s is not marked as a lockfile entry", tg.spec)
				}
				switch f.dir {
				case "yarn-berry":
					// Yarn 2+'s checksum is not the registry's hash.
					if tg.carriesSource() {
						t.Errorf("%s carries a source %q %q", tg.spec, tg.resolved, tg.integrity)
					}
				case "yarn-v1":
					if !strings.HasPrefix(tg.resolved, "https://registry.yarnpkg.com/"+name+"/-/") || !strings.HasPrefix(tg.integrity, "sha512-") {
						t.Errorf("%s: resolved %q integrity %q", tg.spec, tg.resolved, tg.integrity)
					}
				default:
					if tg.resolved != "" || !strings.HasPrefix(tg.integrity, "sha512-") {
						t.Errorf("%s: resolved %q integrity %q", tg.spec, tg.resolved, tg.integrity)
					}
				}
			}
		})
	}
}

// pnpm 10 and later can write pnpm itself, from devEngines, as a first
// document. It downloads and runs that pnpm, so it is checked too.
func TestPnpmEnvironmentDocumentIsRead(t *testing.T) {
	lock, dir := readFixtureLock(t, lockFixture{"pnpm-v9-env", "pnpm", "pnpm-lock.yaml"})
	got := specsOf(pmLockTargets(lock, nodePlatform{"win32", "x64"}, dir))
	want := []string{"@pnpm/exe.win32-x64@12.9.1", "is-number@6.0.0", "is-odd@3.0.1", "pnpm@12.9.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checked %v, want %v", got, want)
	}
}

// fixtureWorld is a verifyWorld holding a fixture project, with a registry
// that agrees with its lockfile.
func fixtureWorld(t *testing.T, f lockFixture, policy string) *verifyWorld {
	t.Helper()
	w := newVerifyWorld(t, policy)
	copyFixture(t, f.dir, w.project)
	lock, ok, err := readPMLockfile(f.cmd, w.project)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	for _, e := range lock.entries {
		if e.sourceKind != "" {
			continue
		}
		base := e.name
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		w.add(e.name+"@"+e.version, fakeRegistryPkg{dist: npmDist{
			tarball:   "https://registry.npmjs.org/" + e.name + "/-/" + base + "-" + e.version + ".tgz",
			integrity: e.integrity}})
	}
	return w
}

// is-number is in none of the project's package.json files. It installs only
// because is-odd and to-regex-range depend on it, which only the lockfile
// says. The checks read package.json until 2026-10-06, and a blocklisted
// is-number installed.
func TestABlockedTransitiveDependencyInTheLockfileIsRefused(t *testing.T) {
	installs := map[string][][]string{
		"pnpm": {{"install"}, {"install", "--frozen-lockfile"}, {"i"}},
		"yarn": {{}, {"install"}, {"install", "--immutable"}},
		"bun":  {{"install"}, {"install", "--frozen-lockfile"}},
	}
	for _, f := range lockFixtures {
		for _, args := range installs[f.cmd] {
			t.Run(f.dir+" "+strings.Join(args, " "), func(t *testing.T) {
				w := fixtureWorld(t, f, `{"typosquatting":{"enabled":false},"blocked_packages":["is-number"]}`)
				code, out := w.run(f.cmd, args...)
				if code == 0 {
					t.Fatalf("%s %v installed a blocked dependency of a dependency:\n%s", f.cmd, args, out)
				}
				rec := findRecord(readAuditRecords(t, w.home), "check_refused", checkBlockedPackage)
				if rec == nil || rec["package"] != "is-number" {
					t.Fatalf("refusal record %v, want one naming is-number:\n%s", rec, out)
				}

				// Unblocked, it goes ahead, and is-number was checked at the
				// versions the lockfile installs.
				w = fixtureWorld(t, f, `{"typosquatting":{"enabled":false}}`)
				if code, out := w.run(f.cmd, args...); code != 0 {
					t.Fatalf("exit %d:\n%s", code, out)
				}
				asked := w.askedNames()
				for _, v := range []string{"is-number@3.0.0", "is-number@6.0.0", "is-number@7.0.0"} {
					if !containsString(asked, v) {
						t.Errorf("%s was not checked: %v", v, asked)
					}
				}
			})
		}
	}
}

// A lockfile entry is installed from what it records, so it must be the
// registry's record of the package it names.
func TestAForgedPMLockfileEntryIsRefused(t *testing.T) {
	const leftPadSHA = "XI5MPzVNApjAyhQzphX8BkmKsKUxD4LdyK24iZeQGinBN9yTQT3bFlCBy/aVx2HrNcqQGsdot8ghrjyrvMCoEA=="
	const isNumber7SHA = "41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng=="
	cases := []struct {
		f        lockFixture
		old, new string
	}{
		// left-pad's entry carries is-number's hash, and pnpm's store serves
		// what that hash names.
		{lockFixtures[2], leftPadSHA, isNumber7SHA},
		{lockFixtures[5], leftPadSHA, isNumber7SHA},
		// Yarn 1 fetches the resolved URL.
		{lockFixtures[3], `resolved "https://registry.yarnpkg.com/left-pad/-/left-pad-1.3.0.tgz#5b8a3a7765dfe001261dde915589e782f8c94d1e"
  integrity sha512-` + leftPadSHA, `resolved "https://evil.example/left-pad-1.3.0.tgz"
  integrity sha512-` + isNumber7SHA},
		// Yarn 4 fetches an __archiveUrl in place of the registry's tarball.
		{lockFixtures[4], `resolution: "left-pad@npm:1.3.0"`, `resolution: "left-pad@npm:1.3.0::__archiveUrl=https%3A%2F%2Fevil.example%2Fleft-pad-1.3.0.tgz"`},
	}
	for _, tc := range cases {
		t.Run(tc.f.dir, func(t *testing.T) {
			w := fixtureWorld(t, tc.f, `{"typosquatting":{"enabled":false}}`)
			path := filepath.Join(w.project, tc.f.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			old, forged := tc.old, tc.new
			if strings.Contains(string(data), "\r\n") {
				// Yarn writes CRLF on Windows, and the fixtures keep it.
				old, forged = strings.ReplaceAll(old, "\n", "\r\n"), strings.ReplaceAll(forged, "\n", "\r\n")
			}
			if !strings.Contains(string(data), old) {
				t.Fatalf("the fixture does not hold %q", old)
			}
			w.write(tc.f.file, strings.Replace(string(data), old, forged, 1))
			code, out := w.run(tc.f.cmd, "install")
			if code == 0 {
				t.Fatalf("a forged entry for left-pad installed:\n%s", out)
			}
			if rec := findRecord(readAuditRecords(t, w.home), "check_refused", checkLockfileSource); rec == nil || rec["package"] != "left-pad" {
				t.Fatalf("refusal record %v, want a lockfile_source refusal for left-pad:\n%s", rec, out)
			}
		})
	}
}

// A lockfile that is there and cannot be read is asked about, as an
// unreadable package-lock.json is: refused with nobody to ask. Approved, the
// checks run on what package.json declares, which is what they read before,
// and the run says what that leaves out.
func TestAnUnreadablePMLockfileIsAskedAbout(t *testing.T) {
	cases := []struct {
		f    lockFixture
		body string
	}{
		{lockFixtures[2], "lockfileVersion: '9.0'\npackages:\n  is-number@6.0.0: &a\n    resolution: {integrity: sha512-x}\n"},
		{lockFixtures[2], "lockfileVersion: '10.0'\npackages: {}\n"},
		{lockFixtures[2], "lockfileVersion: '9.0'\npackages:\n  is-number@6.0.0:\n    resolution: {integrity: sha512-x}\n  is-number@6.0.0:\n    resolution: {integrity: sha512-y}\n"},
		{lockFixtures[2], "lockfileVersion: '9.0'\npackages:\n  is-number@6.0.0:\n    resolution: {commit: abc}\n"},
		{lockFixtures[3], "# yarn lockfile v1\n\nis-number@^6.0.0:\n  resolved \"https://registry.yarnpkg.com/is-number/-/is-number-6.0.0.tgz\"\n"},
		{lockFixtures[4], "__metadata:\n  version: 8\n\n\"is-number@npm:^6.0.0\":\n  version: 6.0.0\n  resolution: \"is-number@weird:6.0.0\"\n"},
		{lockFixtures[5], `{"lockfileVersion": 1, "packages": {"is-number": ["is-number@weird:6.0.0"]}}`},
	}
	for i, tc := range cases {
		t.Run(tc.f.dir+string(rune('a'+i)), func(t *testing.T) {
			w := fixtureWorld(t, tc.f, `{"typosquatting":{"enabled":false}}`)
			w.write(tc.f.file, tc.body)
			code, out := w.run(tc.f.cmd, "install")
			if code == 0 {
				t.Fatalf("an unreadable %s was not asked about:\n%s", tc.f.file, out)
			}
			if len(w.askedNames()) > 0 {
				t.Fatalf("checked %v before the unreadable lockfile was approved", w.askedNames())
			}
			if findRecord(readAuditRecords(t, w.home), "check_refused", checkLockfileUnreadable) == nil {
				t.Fatalf("no %s refusal recorded:\n%s", checkLockfileUnreadable, out)
			}

			t.Setenv("NVX_YES", "1")
			code, out = w.run(tc.f.cmd, "install")
			if code != 0 {
				t.Fatalf("approved, exit %d:\n%s", code, out)
			}
			if !containsString(w.askedNames(), "is-odd@3.0.1") || !strings.Contains(out, "not checked") {
				t.Fatalf("approved, checked %v, want package.json's dependencies and a warning:\n%s", w.askedNames(), out)
			}
		})
	}
}

// A lockfile written for an older package.json leaves out what was added
// since, which the package manager resolves afresh. That is checked as
// package.json declares it, and the run says so.
func TestADependencyTheLockfileDoesNotRecordIsCheckedFromPackageJSON(t *testing.T) {
	for _, f := range lockFixtures {
		t.Run(f.dir, func(t *testing.T) {
			w := fixtureWorld(t, f, `{"typosquatting":{"enabled":false}}`)
			data, err := os.ReadFile(filepath.Join(w.project, "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			w.write("package.json", strings.Replace(string(data), `"ms": "2.1.3"`, `"ms": "2.1.3", "evil-new": "^1.0.0"`, 1))
			w.add("evil-new@1.0.0", fakeRegistryPkg{})
			code, out := w.run(f.cmd, "install")
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if !containsString(w.askedNames(), "evil-new@^1.0.0") {
				t.Fatalf("evil-new was not checked: %v", w.askedNames())
			}
			if !strings.Contains(out, "evil-new") || strings.Count(out, "written for another version of package.json") != 1 {
				t.Fatalf("the run did not say the lockfile leaves evil-new out:\n%s", out)
			}
		})
	}
}

// dropBlocks removes each block of text that starts with a line beginning
// with head, and the lines indented under it.
func dropBlocks(text, head string) string {
	lines := strings.Split(text, "\n")
	indent := len(head) - len(strings.TrimLeft(head, " "))
	var out []string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], head) {
			out = append(out, lines[i])
			continue
		}
		for i+1 < len(lines) {
			next := strings.TrimRight(lines[i+1], "\r")
			if next == "" || len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
			i++
		}
	}
	return strings.Join(out, "\n")
}

// A dependency whose entry is missing from the lockfile is resolved afresh.
// Measured 2026-10-06: with is-number@6.0.0's entry deleted and is-odd's still
// naming it, pnpm 10, frozen or not, and Yarn 1 both installed is-number. It
// is checked as the entry that needs it declares it, and the run says so.
func TestADependencyWithNoEntryIsCheckedAsDeclared(t *testing.T) {
	cases := []struct {
		f    lockFixture
		head string
		want string
	}{
		{lockFixtures[0], "  /is-number/6.0.0:", "is-number@6.0.0"},
		{lockFixtures[1], "  /is-number@6.0.0:", "is-number@6.0.0"},
		{lockFixtures[2], "  is-number@6.0.0:", "is-number@6.0.0"},
		{lockFixtures[3], "is-number@^6.0.0:", "is-number@^6.0.0"},
		{lockFixtures[4], `"is-number@npm:^6.0.0":`, "is-number@^6.0.0"},
		{lockFixtures[5], `    "is-number": [`, "is-number@^6.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.f.dir, func(t *testing.T) {
			w := fixtureWorld(t, tc.f, `{"typosquatting":{"enabled":false}}`)
			data, err := os.ReadFile(filepath.Join(w.project, tc.f.file))
			if err != nil {
				t.Fatal(err)
			}
			cut := dropBlocks(string(data), tc.head)
			if cut == string(data) {
				t.Fatalf("the fixture has no %q", tc.head)
			}
			w.write(tc.f.file, cut)
			code, out := w.run(tc.f.cmd, "install")
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if !containsString(w.askedNames(), tc.want) {
				t.Fatalf("%s was not checked: %v", tc.want, w.askedNames())
			}
			if !strings.Contains(out, "has no entry for is-number") {
				t.Fatalf("the run did not say the lockfile has no entry for is-number:\n%s", out)
			}
		})
	}
}

// An update installs versions newer than the lockfile's, so the lockfile is
// not what it is checked against: package.json's ranges are, as before.
func TestAnUpdateIsNotCheckedAgainstTheLockfile(t *testing.T) {
	dir := tempDir(t)
	copyFixture(t, "pnpm-v9", dir)
	chdirForCollapsedScopeTest(t, dir)
	for _, c := range [][]string{{"pnpm", "update"}, {"yarn", "upgrade"}, {"bun", "update"}} {
		got := detectShimPackagesForVerification(c[0], c[1:])
		if containsString(got, "is-number@6.0.0") || !containsString(got, "is-odd@3.0.1") {
			t.Errorf("%v: checks %v, want package.json's dependencies", c, got)
		}
	}
	if got := detectShimPackagesForVerification("pnpm", []string{"rebuild"}); !containsString(got, "is-number@6.0.0") {
		t.Errorf("pnpm rebuild: checks %v, want what is installed, from the lockfile", got)
	}
}

func TestBunBinaryLockfileIsNamedAsUnread(t *testing.T) {
	w := newVerifyWorld(t, `{"typosquatting":{"enabled":false}}`)
	w.add("is-odd@3.0.1", fakeRegistryPkg{})
	w.write("package.json", `{"dependencies":{"is-odd":"3.0.1"}}`)
	w.write("bun.lockb", "\x00\x01binary")
	code, out := w.run("bun", "install")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "bun.lockb") || !reflect.DeepEqual(w.askedNames(), []string{"is-odd@3.0.1"}) {
		t.Fatalf("checked %v; output:\n%s", w.askedNames(), out)
	}
}

func TestYAMLSubset(t *testing.T) {
	docs, err := parseYAMLDocuments([]byte("# c\r\n---\na: 1\n---\nlockfileVersion: '9.0'\n" +
		"k:\n  'q@1': {integrity: sha512-x, tarball: 'https://h/x.tgz', n: {a: [b, \"c\\u0041\"]}}\n" +
		"  plain@file:x: {}\n  seq:\n  - one\n  - k: v\n    k2: v2\n" +
		"  msg: >-\n    folded\n    text\n  esc: \"a\\\"b\"\n  sq: 'it''s'\n  e: []\n  n: ~\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("%d documents", len(docs))
	}
	k := docs[1].get("k")
	q := k.get("q@1")
	if q.get("integrity").str() != "sha512-x" || q.get("tarball").str() != "https://h/x.tgz" ||
		!reflect.DeepEqual(q.get("n").get("a").list(), []string{"b", "cA"}) {
		t.Fatalf("flow mapping read as %+v", q)
	}
	if k.get("plain@file:x") == nil || k.get("plain@file:x").kind != yamlMap {
		t.Fatalf("keys %v", k.keys)
	}
	seq := k.get("seq")
	if len(seq.items) != 2 || seq.items[0].str() != "one" || seq.items[1].get("k2").str() != "v2" {
		t.Fatalf("sequence read as %+v", seq)
	}
	for key, want := range map[string]string{"msg": "folded text", "esc": `a"b`, "sq": "it's"} {
		if got := k.get(key).str(); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if k.get("n").kind != yamlNull || k.get("e").kind != yamlSeq {
		t.Errorf("n and e read as %v and %v", k.get("n").kind, k.get("e").kind)
	}

	for _, bad := range []string{
		"a: &x 1\nb: *x\n",               // anchors and aliases
		"a: !!str 1\n",                   // tags
		"a: 1\na: 2\n",                   // a duplicate key
		"a:\n\tb: 1\n",                   // tab indentation
		"a: {b: 1\n",                     // an unclosed flow mapping
		"a: 'b\n",                        // an unclosed quote
		"a:\n  b: 1\n   c: 2\n",          // inconsistent indentation
		"? a\n: b\n",                     // a complex key
		"a: \"\\q\"\n",                   // an unknown escape
		"a: 1\n  b: 2\n",                 // a value with a nested block
		"a: \"x\" y\n",                   // text after a value
		"a: 'https://secret@host/x' z\n", // errors never quote the line
		"a: " + strings.Repeat("[", 100) + strings.Repeat("]", 100) + "\n",
	} {
		_, err := parseYAMLDocuments([]byte(bad))
		if err == nil {
			t.Errorf("%q parsed", bad)
			continue
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("the error quotes the lockfile: %v", err)
		}
	}
}
