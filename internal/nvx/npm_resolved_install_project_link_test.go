package nvx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pass runs from the project with a folder inside it as its prefix. For an
// install that names nothing, npm 7 to 10 then install the project into that
// folder as a file: dependency, and the lockfile it writes lists the project's own
// files, a link to them and a dependency on them. None of it belongs in the
// lockfile the install starts from, where the folder is not there. Measured
// 2026-10-08 with 7.24.2, 8.19.4, 9.9.4, 10.9.3 and 10.9.9. 11.11.0 does not do it.

const (
	linkedManifest = `{"name":"app","version":"1.0.0","dependencies":{"app":"file:../../..","foo":"^1.0.0"}}`
	// What npm 10 wrote for it, lockfileVersion 3.
	linkedLockV3 = `{"name":"app","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{` +
		`"":{"name":"app","version":"1.0.0","dependencies":{"app":"file:../../..","foo":"^1.0.0"}},` +
		`"../../..":{"name":"app","version":"1.0.0","dependencies":{"foo":"^1.0.0"}},` +
		`"node_modules/app":{"resolved":"../../..","link":true},` +
		`"node_modules/foo":{"version":"1.2.3","resolved":"https://registry.npmjs.org/foo/-/foo-1.2.3.tgz","integrity":"sha512-AAAA"}}}`
	// And npm 7 and 8, lockfileVersion 2, which repeats the tree the old way.
	linkedLockV2 = `{"name":"app","version":"1.0.0","lockfileVersion":2,"requires":true,"packages":{` +
		`"":{"name":"app","version":"1.0.0","dependencies":{"app":"file:../../..","foo":"^1.0.0"}},` +
		`"../../..":{"name":"app","version":"1.0.0","dependencies":{"foo":"^1.0.0"}},` +
		`"node_modules/app":{"resolved":"../../..","link":true},` +
		`"node_modules/foo":{"version":"1.2.3","resolved":"https://registry.npmjs.org/foo/-/foo-1.2.3.tgz","integrity":"sha512-AAAA"}},` +
		`"dependencies":{"app":{"version":"file:../../..","requires":{"foo":"^1.0.0"}},` +
		`"foo":{"version":"1.2.3","resolved":"https://registry.npmjs.org/foo/-/foo-1.2.3.tgz","integrity":"sha512-AAAA"}}}`
)

// linkedFixture puts the scratch folder where the pass puts it, inside the project.
func linkedFixture(t *testing.T, project, written map[string]string) *resolvedFixture {
	t.Helper()
	f := newResolvedFixture(t, project, nil)
	f.scratch = filepath.Join(f.root, "node_modules", ".cache", "nvx-resolve-test")
	if err := os.MkdirAll(f.scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range written {
		if err := os.WriteFile(filepath.Join(f.scratch, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestTheProjectIsLeftOutOfTheLockfileTheInstallStartsFrom(t *testing.T) {
	for name, lock := range map[string]string{"lockfileVersion 3": linkedLockV3, "lockfileVersion 2": linkedLockV2} {
		t.Run(name, func(t *testing.T) {
			f := linkedFixture(t, map[string]string{"package.json": `{"name":"app","version":"1.0.0","dependencies":{"foo":"^1.0.0"}}`},
				map[string]string{"package.json": linkedManifest, "package-lock.json": lock})
			r := f.recorded()
			if len(r.lock) == 0 {
				t.Fatal("the lockfile was not kept")
			}
			for _, gone := range []string{"../../..", "node_modules/app", "file:"} {
				if strings.Contains(string(r.lock), gone) {
					t.Errorf("the lockfile still holds %q:\n%s", gone, r.lock)
				}
			}
			var parsed struct {
				Packages map[string]struct {
					Dependencies map[string]string `json:"dependencies"`
					Version      string            `json:"version"`
				} `json:"packages"`
			}
			if err := json.Unmarshal(r.lock, &parsed); err != nil {
				t.Fatalf("the lockfile does not read: %v\n%s", err, r.lock)
			}
			if got := parsed.Packages[""].Dependencies; len(got) != 1 || got["foo"] != "^1.0.0" {
				t.Errorf("the project's dependencies are %v, want only foo ^1.0.0", got)
			}
			if got := parsed.Packages["node_modules/foo"].Version; got != "1.2.3" {
				t.Errorf("foo is at %q, want 1.2.3", got)
			}
			if got := r.versions(); got["foo"] != "1.2.3" || len(got) != 1 {
				t.Errorf("versions = %v, want only foo 1.2.3", got)
			}

			// A lockfile with the project taken out is one a bare install can start from.
			run, finish := r.adopt("npm", []string{"install"}, "npm", []string{"install"})
			if finish == nil {
				t.Fatal("not adopted")
			}
			defer finish()
			if len(run) != 1 {
				t.Errorf("ran %v", run)
			}
		})
	}
}

// A lockfile npm 11 wrote has nothing to take out and is kept as it is.
func TestALockfileWithoutTheProjectIsKeptAsItIs(t *testing.T) {
	clean := `{"name":"app","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{` +
		`"":{"name":"app","version":"1.0.0","dependencies":{"foo":"^1.0.0"}},` +
		`"node_modules/foo":{"version":"1.2.3","resolved":"https://registry.npmjs.org/foo/-/foo-1.2.3.tgz","integrity":"sha512-AAAA"}}}`
	f := linkedFixture(t, map[string]string{"package.json": `{"name":"app"}`},
		map[string]string{"package.json": `{"name":"app","version":"1.0.0","dependencies":{"foo":"^1.0.0"}}`, "package-lock.json": clean})
	if got := string(f.recorded().lock); got != clean {
		t.Fatalf("the lockfile was changed:\n%s", got)
	}
}

// A dependency on a folder that is not the project is not this, and neither is a
// package of the same name that is not a link. Those lockfiles are not kept.
func TestOnlyTheProjectItselfIsTakenOut(t *testing.T) {
	for name, tc := range map[string]struct{ manifest, lock string }{
		"a file: dependency on another folder": {
			`{"name":"app","version":"1.0.0","dependencies":{"other":"file:../elsewhere","foo":"^1.0.0"}}`,
			strings.NewReplacer(`"app":"file:../../.."`, `"other":"file:../elsewhere"`, `"node_modules/app"`, `"node_modules/other"`).Replace(linkedLockV3),
		},
		"a package of that name that is not a link": {
			linkedManifest,
			strings.Replace(linkedLockV3, `{"resolved":"../../..","link":true}`, `{"version":"1.0.0","resolved":"https://registry.npmjs.org/app/-/app-1.0.0.tgz","integrity":"sha512-BBBB"}`, 1),
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := linkedFixture(t, map[string]string{"package.json": `{"name":"app"}`},
				map[string]string{"package.json": tc.manifest, "package-lock.json": tc.lock})
			if r := f.recorded(); len(r.lock) != 0 {
				t.Fatalf("kept a lockfile that was not the project's alone:\n%s", r.lock)
			}
		})
	}
}
