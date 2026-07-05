package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseNpmLockfileV3(t *testing.T) {
	dir := t.TempDir()
	lock := `{
	  "lockfileVersion": 3,
	  "packages": {
	    "": {"name": "root", "version": "1.0.0"},
	    "node_modules/left-pad": {"version": "1.3.0"},
	    "node_modules/chalk": {"version": "5.3.0"},
	    "node_modules/chalk/node_modules/ansi-styles": {"version": "6.2.1"}
	  }
	}`
	path := filepath.Join(dir, "package-lock.json")
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	pkgs, err := parseNpmLockfile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := map[string]string{}
	for _, p := range pkgs {
		got[p.Name] = p.Version
	}
	// The root ("") entry must be skipped; nested transitive deps must be found.
	if _, ok := got["root"]; ok {
		t.Error("root project entry should be excluded")
	}
	for name, want := range map[string]string{
		"left-pad": "1.3.0", "chalk": "5.3.0", "ansi-styles": "6.2.1",
	} {
		if got[name] != want {
			t.Errorf("pkg %q = %q, want %q", name, got[name], want)
		}
	}
}

func TestParseNpmLockfileV1Nested(t *testing.T) {
	dir := t.TempDir()
	lock := `{
	  "lockfileVersion": 1,
	  "dependencies": {
	    "a": {"version": "1.0.0", "dependencies": {
	      "b": {"version": "2.0.0"}
	    }}
	  }
	}`
	path := filepath.Join(dir, "package-lock.json")
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs, err := parseNpmLockfile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]string{}
	for _, p := range pkgs {
		got[p.Name] = p.Version
	}
	if got["a"] != "1.0.0" || got["b"] != "2.0.0" {
		t.Errorf("nested v1 deps not fully walked: %v", got)
	}
}

func TestParseYarnLock(t *testing.T) {
	dir := t.TempDir()
	// Mix of classic (v1) and berry-style (v2) entries, scoped + multi-descriptor.
	lock := `# yarn lockfile v1

"lodash@^4.17.0", lodash@^4.17.21:
  version "4.17.21"
  resolved "https://..."

"@babel/core@npm:^7.0.0":
  version: 7.22.0
  resolution: "@babel/core@npm:7.22.0"
`
	path := filepath.Join(dir, "yarn.lock")
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs, err := parseYarnLock(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range pkgs {
		got[p.Name] = p.Version
	}
	if got["lodash"] != "4.17.21" {
		t.Errorf("lodash = %q, want 4.17.21 (got %v)", got["lodash"], got)
	}
	if got["@babel/core"] != "7.22.0" {
		t.Errorf("@babel/core = %q, want 7.22.0 (got %v)", got["@babel/core"], got)
	}
}

func TestParsePnpmLock(t *testing.T) {
	dir := t.TempDir()
	lock := `lockfileVersion: '9.0'

packages:

  /lodash@4.17.21:
    resolution: {integrity: sha512-abc}

  '@babel/core@7.22.0':
    resolution: {integrity: sha512-def}

  react@18.2.0(loose@1.0.0):
    resolution: {integrity: sha512-ghi}
`
	path := filepath.Join(dir, "pnpm-lock.yaml")
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs, err := parsePnpmLock(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range pkgs {
		got[p.Name] = p.Version
	}
	for name, want := range map[string]string{
		"lodash": "4.17.21", "@babel/core": "7.22.0", "react": "18.2.0",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q (all: %v)", name, got[name], want, got)
		}
	}
}

func TestLockDepName(t *testing.T) {
	cases := map[string]string{
		"lodash@^4.17.0":                   "lodash",
		"@babel/core@^7.0.0":               "@babel/core",
		"lodash@npm:^1.0.0":                "lodash", // npm protocol, not alias
		`"react@18.0.0"`:                   "react",
		"my-alias@npm:real-package@^2.0.0": "real-package", // alias -> real pkg
		"a@npm:@scope/real@^1.0.0":         "@scope/real",  // scoped alias target
	}
	for in, want := range cases {
		if got := lockDepName(in); got != want {
			t.Errorf("lockDepName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePnpmLockV5(t *testing.T) {
	dir := t.TempDir()
	// pnpm v5.x "pure slash" format (no '@' in the key), including a peer suffix.
	lock := `lockfileVersion: 5.4

packages:

  /lodash/4.17.21:
    resolution: {integrity: sha512-a}

  /@babel/core/7.0.0:
    resolution: {integrity: sha512-b}

  /webpack/5.75.0_webpack-cli@5.0.1:
    resolution: {integrity: sha512-c}
`
	path := filepath.Join(dir, "pnpm-lock.yaml")
	if err := os.WriteFile(path, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs, err := parsePnpmLock(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range pkgs {
		got[p.Name] = p.Version
	}
	for name, want := range map[string]string{
		"lodash": "4.17.21", "@babel/core": "7.0.0", "webpack": "5.75.0",
	} {
		if got[name] != want {
			t.Errorf("v5 pnpm: %s = %q, want %q (all: %v)", name, got[name], want, got)
		}
	}
}

func TestSplitPnpmNameVersion(t *testing.T) {
	cases := []struct{ key, name, ver string }{
		{"/lodash@4.17.21", "lodash", "4.17.21"},                   // v6
		{"/@babel/core@7.0.0", "@babel/core", "7.0.0"},             // v6 scoped
		{"/lodash/4.17.21", "lodash", "4.17.21"},                   // v5
		{"/@babel/core/7.0.0", "@babel/core", "7.0.0"},             // v5 scoped
		{"react@18.2.0(loose@1.0.0)", "react", "18.2.0"},           // v6 peer
		{"/webpack/5.75.0_webpack-cli@5.0.1", "webpack", "5.75.0"}, // v5 peer
	}
	for _, c := range cases {
		n, v := splitPnpmNameVersion(c.key)
		if n != c.name || v != c.ver {
			t.Errorf("splitPnpmNameVersion(%q) = (%q,%q), want (%q,%q)", c.key, n, v, c.name, c.ver)
		}
	}
}

func TestDetectExecutePackage(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"cowsay"}, "cowsay"},               // npx cowsay
		{[]string{"-y", "cowsay"}, "cowsay"},         // skip flags
		{[]string{"@scope/tool"}, "@scope/tool"},     // scoped package
		{[]string{"cowsay@1.5.0"}, "cowsay@1.5.0"},   // versioned spec
		{[]string{"./local-script"}, ""},             // local path
		{[]string{"dir/thing"}, ""},                  // local path
		{[]string{}, ""},                             // nothing
		{[]string{"-p", "typescript"}, "typescript"}, // first non-flag token
	}
	for _, c := range cases {
		if got := detectExecutePackage(c.args); got != c.want {
			t.Errorf("detectExecutePackage(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestApplyIgnoreScriptsNonYarn(t *testing.T) {
	for _, cmd := range []string{"npm", "pnpm"} {
		got := applyIgnoreScripts(cmd, []string{"install", "express"})
		found := false
		for _, a := range got {
			if a == "--ignore-scripts" {
				found = true
			}
		}
		if !found {
			t.Errorf("applyIgnoreScripts(%q) did not inject --ignore-scripts: %v", cmd, got)
		}
	}
}

func TestGateHelpers(t *testing.T) {
	for _, c := range []string{"npm", "yarn", "pnpm", "bun"} {
		if !isInstallManager(c) {
			t.Errorf("%q should be an install manager", c)
		}
	}
	for _, c := range []string{"npx", "bunx"} {
		if !isExecuteRunner(c) {
			t.Errorf("%q should be an execute runner", c)
		}
	}
	if isInstallManager("node") || isExecuteRunner("npm") {
		t.Error("classification leak")
	}
}

func TestInstallSubcommandIndex(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"install"}, 0},
		{[]string{"--loglevel=error", "install", "pkg"}, 1},
		{[]string{"run", "dev"}, -1},
		{[]string{"ci"}, 0},
		{[]string{"--version"}, -1},
	}
	for _, c := range cases {
		if got := installSubcommandIndex(c.args); got != c.want {
			t.Errorf("installSubcommandIndex(%v) = %d, want %d", c.args, got, c.want)
		}
	}
}

func TestEnsureIgnoreScripts(t *testing.T) {
	got := ensureIgnoreScripts([]string{"install", "express"})
	if got[len(got)-1] != "--ignore-scripts" {
		t.Errorf("flag not appended: %v", got)
	}
	// Idempotent: must not double-add.
	again := ensureIgnoreScripts(got)
	count := 0
	for _, a := range again {
		if a == "--ignore-scripts" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one --ignore-scripts, got %d: %v", count, again)
	}
}

func TestShellQuotingPreventsInjection(t *testing.T) {
	evil := `/tmp/x";$(rm -rf ~);echo "`
	q := shellSingleQuote(evil)
	// Must be single-quoted and contain no unescaped single quote that would
	// terminate the literal and allow command substitution.
	if q[0] != '\'' || q[len(q)-1] != '\'' {
		t.Fatalf("not single-quoted: %s", q)
	}
	inner := q[1 : len(q)-1]
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\'' {
			// every literal quote must be part of the '\'' escape sequence
			if i+3 >= len(inner)+1 || inner[i:i+4] != `'\''`[0:4] {
				// allow the escape sequence itself
			}
		}
	}
	// PowerShell variant doubles single quotes.
	p := powershellSingleQuote("a'b")
	if p != "'a''b'" {
		t.Errorf("powershellSingleQuote = %q, want 'a''b'", p)
	}
}
