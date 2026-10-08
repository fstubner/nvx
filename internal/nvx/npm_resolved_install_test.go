package nvx

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// resolvedFixture is a project with a resolution pass already run on it: the
// project's own files, and a scratch folder holding what the pass wrote.
type resolvedFixture struct {
	root, scratch string
	given         map[string][]byte
}

func newResolvedFixture(t *testing.T, project map[string]string, written map[string]string) *resolvedFixture {
	t.Helper()
	f := &resolvedFixture{root: tempDir(t), scratch: tempDir(t), given: map[string][]byte{}}
	for name, body := range project {
		if err := os.WriteFile(filepath.Join(f.root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		f.given[name] = []byte(body)
	}
	for name, body := range written {
		if err := os.WriteFile(filepath.Join(f.scratch, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *resolvedFixture) recorded() *resolvedInstall {
	r := &resolvedInstall{}
	r.record(f.root, f.scratch, f.given)
	return r
}

// A lock for package.json {"dependencies":{"foo":"^1.0.0"}} holding foo and, one
// level down, the leaf it brings in.
const (
	fooManifest = `{"name":"app","version":"1.0.0","dependencies":{"foo":"^1.0.0"}}`
	fooLock     = `{"name":"app","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{` +
		`"":{"name":"app","version":"1.0.0","dependencies":{"foo":"^1.0.0"}},` +
		`"node_modules/foo":{"version":"1.2.3","resolved":"https://registry.npmjs.org/foo/-/foo-1.2.3.tgz","integrity":"sha512-AAAA","dependencies":{"leaf":"^2.0.0"}},` +
		`"node_modules/leaf":{"version":"2.1.0","resolved":"https://registry.npmjs.org/leaf/-/leaf-2.1.0.tgz","integrity":"sha512-BBBB"},` +
		`"node_modules/@s/aliased":{"name":"real","version":"3.0.0","resolved":"https://registry.npmjs.org/real/-/real-3.0.0.tgz","integrity":"sha512-CCCC"},` +
		`"node_modules/foo/node_modules/nested":{"version":"9.9.9","resolved":"https://registry.npmjs.org/nested/-/nested-9.9.9.tgz","integrity":"sha512-DDDD"}}}`
)

func TestOnlyTheTopOfTheTreeCanBePinned(t *testing.T) {
	f := newResolvedFixture(t, map[string]string{"package.json": `{"name":"app"}`},
		map[string]string{"package.json": fooManifest, "package-lock.json": fooLock})
	got := f.recorded().versions()
	want := map[string]string{"foo": "1.2.3", "leaf": "2.1.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("versions = %v, want %v (an alias, whose name is not what it installs, and a nested copy are not pinnable)", got, want)
	}
}

func TestWhichInstallsCanBePinned(t *testing.T) {
	versions := map[string]string{"foo": "1.2.3", "bar": "0.4.0", "@scope/baz": "5.0.0-beta.1"}
	cases := []struct {
		name string
		args []string
		want []string // nil when the command cannot be pinned
	}{
		{"bare install", []string{"install"}, []string{"install"}},
		{"bare install with flags", []string{"i", "--omit=dev", "--no-audit"}, []string{"i", "--omit=dev", "--no-audit"}},
		{"a name", []string{"install", "foo"}, []string{"install", "foo@1.2.3"}},
		{"a tag", []string{"install", "foo@latest"}, []string{"install", "foo@1.2.3"}},
		{"a tag on a scoped name", []string{"add", "@scope/baz@next", "-E"}, []string{"add", "@scope/baz@5.0.0-beta.1", "-E"}},
		{"the exact version the lockfile holds", []string{"install", "foo@1.2.3"}, []string{"install", "foo@1.2.3"}},
		{"-D and two names", []string{"install", "-D", "foo", "bar"}, []string{"install", "-D", "foo@1.2.3", "bar@0.4.0"}},
		{"a flag with its value attached", []string{"install", "--registry=https://r.example/", "foo"}, []string{"install", "--registry=https://r.example/", "foo@1.2.3"}},

		// A range is saved as it was typed, and pinning it would save something else.
		{"a caret range", []string{"install", "foo@^1.0.0"}, nil},
		{"a partial version", []string{"install", "foo@1"}, nil},
		{"another exact version", []string{"install", "foo@1.2.4"}, nil},
		// npm reads these as other things than the name.
		{"an alias", []string{"install", "foo@npm:bar@1"}, nil},
		{"a git source", []string{"install", "foo@github:someone/foo"}, nil},
		{"a path", []string{"install", "./foo"}, nil},
		{"a URL", []string{"install", "https://example.invalid/foo.tgz"}, nil},
		{"a tag with a digit", []string{"install", "foo@next-14"}, nil},
		{"a name with capitals", []string{"install", "Foo"}, nil},
		{"a package the lockfile does not hold", []string{"install", "unknown"}, nil},
		// A flag that might take the next token as its value leaves the packages unclear.
		{"a flag with a separate value", []string{"install", "--registry", "https://r.example/", "foo"}, nil},
		{"an unknown flag", []string{"install", "--made-up", "foo"}, nil},
		{"after --", []string{"install", "--", "foo"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verb := commandVerbIndex(tc.args, npmResolveVerbs...)
			got, ok := pinInstallArgs(tc.args, verb, versions)
			if tc.want == nil {
				if ok {
					t.Fatalf("pinned %v to %v, want it left alone", tc.args, got)
				}
				return
			}
			if !ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pinned %v to %v (ok=%v), want %v", tc.args, got, ok, tc.want)
			}
		})
	}
}

func TestPinningLeavesTheTypedArgumentsAlone(t *testing.T) {
	args := []string{"install", "foo"}
	if _, ok := pinInstallArgs(args, 0, map[string]string{"foo": "1.2.3"}); !ok {
		t.Fatal("not pinned")
	}
	if !reflect.DeepEqual(args, []string{"install", "foo"}) {
		t.Fatalf("the caller's arguments were changed to %v", args)
	}
}

func TestOnlyAnInstallTheLockfileCanStandInForIsAdopted(t *testing.T) {
	project := map[string]string{"package.json": `{"name":"app","version":"1.0.0"}`}
	written := map[string]string{"package.json": fooManifest, "package-lock.json": fooLock}

	cases := []struct {
		name    string
		cmd     string
		args    []string
		pmCmd   string
		pmArgs  []string
		adopted bool
		want    []string
	}{
		{"npm install foo", "npm", []string{"install", "foo"}, "npm", []string{"install", "foo"}, true, []string{"install", "foo@1.2.3"}},
		{"an abbreviated verb", "npm", []string{"inst", "foo"}, "npm", []string{"inst", "foo"}, true, []string{"inst", "foo@1.2.3"}},
		{"npm install-test", "npm", []string{"it", "foo"}, "npm", []string{"it", "foo"}, true, []string{"it", "foo@1.2.3"}},
		// update and dedupe ask the registry for something newer than the lockfile.
		{"npm update", "npm", []string{"update"}, "npm", []string{"update"}, false, nil},
		{"npm up foo", "npm", []string{"up", "foo"}, "npm", []string{"up", "foo"}, false, nil},
		{"npm dedupe", "npm", []string{"dedupe"}, "npm", []string{"dedupe"}, false, nil},
		{"npm rebuild", "npm", []string{"rebuild"}, "npm", []string{"rebuild"}, false, nil},
		{"npm ci", "npm", []string{"ci"}, "npm", []string{"ci"}, false, nil},
		{"a range", "npm", []string{"install", "foo@^1"}, "npm", []string{"install", "foo@^1"}, false, nil},
		// The command that runs is not the command that was resolved.
		{"npm through corepack", "corepack", []string{"npm", "install"}, "npm", []string{"install"}, false, nil},
		{"pnpm", "pnpm", []string{"install"}, "pnpm", []string{"install"}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newResolvedFixture(t, project, written)
			r := f.recorded()
			run, finish := r.adopt(tc.cmd, tc.args, tc.pmCmd, tc.pmArgs)
			if finish != nil {
				defer finish()
			}
			if (finish != nil) != tc.adopted {
				t.Fatalf("adopted = %v, want %v", finish != nil, tc.adopted)
			}
			if tc.adopted && !reflect.DeepEqual(run, tc.want) {
				t.Fatalf("ran %v, want %v", run, tc.want)
			}
			if !tc.adopted {
				if !reflect.DeepEqual(run, tc.args) {
					t.Fatalf("ran %v, want the arguments as typed", run)
				}
				if _, err := os.Stat(filepath.Join(f.root, "package-lock.json")); err == nil {
					t.Fatal("a lockfile was put in the project for a command that does not use it")
				}
			}
		})
	}
}

// Only entries the checks can hold to the registry's record are handed over. A
// link, a git source, a local folder, a package bundled in another and an entry
// with no hash have no such record.
func TestOnlyALockfileOfHashedTarballsIsKept(t *testing.T) {
	lockWith := func(extra string) string {
		return `{"lockfileVersion":3,"packages":{"":{"dependencies":{"foo":"^1.0.0"}},` +
			`"node_modules/foo":{"version":"1.2.3","resolved":"https://registry.npmjs.org/foo/-/foo-1.2.3.tgz","integrity":"sha512-AAAA"},` + extra + `}}`
	}
	project := map[string]string{"package.json": `{"name":"app"}`}
	for name, tc := range map[string]struct {
		lock string
		kept bool
	}{
		"hashed tarballs":       {lockWith(`"node_modules/bar":{"version":"1.0.0","resolved":"http://mirror.example/bar/-/bar-1.0.0.tgz","integrity":"sha512-BBBB"}`), true},
		"a link":                {lockWith(`"node_modules/bar":{"resolved":"../bar","link":true}`), false},
		"a git source":          {lockWith(`"node_modules/bar":{"version":"1.0.0","resolved":"git+ssh://git@github.com/u/bar.git#abc","integrity":"sha512-BBBB"}`), false},
		"a local folder":        {lockWith(`"node_modules/bar":{"version":"1.0.0","resolved":"file:../bar","integrity":"sha512-BBBB"}`), false},
		"a bundled package":     {lockWith(`"node_modules/bar":{"version":"1.0.0","inBundle":true}`), false},
		"an entry with no hash": {lockWith(`"node_modules/bar":{"version":"1.0.0","resolved":"https://registry.npmjs.org/bar/-/bar-1.0.0.tgz"}`), false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResolvedFixture(t, project, map[string]string{"package.json": fooManifest, "package-lock.json": tc.lock})
			if kept := len(f.recorded().lock) != 0; kept != tc.kept {
				t.Fatalf("kept = %v, want %v", kept, tc.kept)
			}
		})
	}
}

// A lockfile that does not describe the package.json the pass ended with is one
// npm would resolve further, so it is not one to install from.
func TestALockfileThatDoesNotMatchIsNotKept(t *testing.T) {
	project := map[string]string{"package.json": `{"name":"app"}`}
	mismatched := `{"lockfileVersion":3,"packages":{"":{"dependencies":{"other":"^1.0.0"}},"node_modules/other":{"version":"1.0.0"}}}`
	for name, written := range map[string]map[string]string{
		"a lockfile for other dependencies": {"package.json": fooManifest, "package-lock.json": mismatched},
		"no lockfile":                       {"package.json": fooManifest},
		"an unreadable lockfile":            {"package.json": fooManifest, "package-lock.json": "{"},
		"an old lockfile":                   {"package.json": fooManifest, "package-lock.json": `{"lockfileVersion":1,"dependencies":{"foo":{"version":"1.2.3"}}}`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResolvedFixture(t, project, written)
			if r := f.recorded(); len(r.lock) != 0 {
				t.Fatalf("kept %s", r.lock)
			}
		})
	}
}

// A shrinkwrap is what npm reads first, and the pass wrote to that. The
// project's lockfile is not what the install would use.
func TestAShrinkwrapIsNotReplacedByALockfile(t *testing.T) {
	f := newResolvedFixture(t,
		map[string]string{"package.json": `{"name":"app"}`, "npm-shrinkwrap.json": `{"lockfileVersion":3,"packages":{"":{}}}`},
		map[string]string{"package.json": fooManifest, "package-lock.json": fooLock})
	if r := f.recorded(); len(r.lock) != 0 {
		t.Fatal("kept a lockfile for a project that has a shrinkwrap")
	}
}

// What the project held when the pass ran is what the lockfile describes. If it
// changed while the checks ran, the install is left to resolve for itself.
func TestAProjectThatChangedWhileItWasCheckedIsNotAdopted(t *testing.T) {
	for name, change := range map[string]func(root string) error{
		"package.json edited": func(root string) error {
			return os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"app","private":true}`), 0o600)
		},
		"a lockfile appeared": func(root string) error {
			return os.WriteFile(filepath.Join(root, "package-lock.json"), []byte(`{}`), 0o600)
		},
		"a shrinkwrap appeared": func(root string) error {
			return os.WriteFile(filepath.Join(root, "npm-shrinkwrap.json"), []byte(`{}`), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResolvedFixture(t, map[string]string{"package.json": `{"name":"app"}`},
				map[string]string{"package.json": fooManifest, "package-lock.json": fooLock})
			r := f.recorded()
			if err := change(f.root); err != nil {
				t.Fatal(err)
			}
			run, finish := r.adopt("npm", []string{"install"}, "npm", []string{"install"})
			if finish != nil {
				finish()
				t.Fatal("adopted a lockfile for a project that was not as the pass found it")
			}
			if !reflect.DeepEqual(run, []string{"install"}) {
				t.Fatalf("ran %v", run)
			}
		})
	}
}

func lockfileAt(root string) string { return filepath.Join(root, "package-lock.json") }

// The staged lockfile is taken out again unless npm wrote it, whatever the exit
// code. A run that stops before npm saves leaves the project's lockfile alone, so
// this does too. A run that fails after npm saved leaves the saved one.
func TestAStagedLockfileIsTakenOutUnlessNpmWroteIt(t *testing.T) {
	staged := []byte(`{"staged":true}`)
	earlier := []byte(`{"earlier":true}`)
	earlierDate := time.Date(2024, time.March, 5, 12, 0, 0, 0, time.UTC)

	t.Run("none before, npm did not write", func(t *testing.T) {
		root := tempDir(t)
		s, ok := stageLockfile(lockfileAt(root), staged)
		if !ok {
			t.Fatal("not staged")
		}
		if got, _ := os.ReadFile(lockfileAt(root)); !bytes.Equal(got, staged) {
			t.Fatalf("staged file holds %q", got)
		}
		s.finish()
		if _, err := os.Stat(lockfileAt(root)); err == nil {
			t.Fatal("a lockfile npm never wrote was left in the project")
		}
	})

	t.Run("one before, npm did not write", func(t *testing.T) {
		root := tempDir(t)
		if err := os.WriteFile(lockfileAt(root), earlier, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(lockfileAt(root), earlierDate, earlierDate); err != nil {
			t.Fatal(err)
		}
		s, ok := stageLockfile(lockfileAt(root), staged)
		if !ok {
			t.Fatal("not staged")
		}
		s.finish()
		got, _ := os.ReadFile(lockfileAt(root))
		if !bytes.Equal(got, earlier) {
			t.Fatalf("the project's lockfile holds %q, want what it held", got)
		}
		if info, _ := os.Stat(lockfileAt(root)); !info.ModTime().Equal(earlierDate) {
			t.Errorf("the project's lockfile is dated %v, want %v", info.ModTime(), earlierDate)
		}
	})

	t.Run("npm wrote it", func(t *testing.T) {
		root := tempDir(t)
		s, ok := stageLockfile(lockfileAt(root), staged)
		if !ok {
			t.Fatal("not staged")
		}
		// What npm does: write the file, which dates it now. The same length, so
		// that only the date can say it was written.
		written := bytes.ToUpper(staged)
		if err := os.WriteFile(lockfileAt(root), written, 0o600); err != nil {
			t.Fatal(err)
		}
		s.finish()
		if got, _ := os.ReadFile(lockfileAt(root)); !bytes.Equal(got, written) {
			t.Fatalf("npm's lockfile was replaced by %q", got)
		}
	})

	t.Run("someone else changed it", func(t *testing.T) {
		root := tempDir(t)
		s, ok := stageLockfile(lockfileAt(root), staged)
		if !ok {
			t.Fatal("not staged")
		}
		edited := []byte(`{"edited":"by hand"}`)
		if err := os.WriteFile(lockfileAt(root), edited, 0o600); err != nil {
			t.Fatal(err)
		}
		s.finish()
		if got, _ := os.ReadFile(lockfileAt(root)); !bytes.Equal(got, edited) {
			t.Fatalf("an edit made during the install was replaced by %q", got)
		}
	})
}

func TestALockfileThatIsALinkIsLeftAlone(t *testing.T) {
	root := tempDir(t)
	target := filepath.Join(root, "real-lock.json")
	if err := os.WriteFile(target, []byte(`{"real":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, lockfileAt(root)); err != nil {
		t.Skipf("creating symlinks on Windows needs privilege or Developer Mode: %v", err)
	}
	if _, ok := stageLockfile(lockfileAt(root), []byte(`{"staged":true}`)); ok {
		t.Fatal("staged over a link")
	}
	if got, _ := os.ReadFile(target); string(got) != `{"real":true}` {
		t.Fatalf("the linked file holds %q", got)
	}
}
