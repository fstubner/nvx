package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// dotenvNameCases are names and whether a contained process may not read them.
var dotenvNameCases = map[string]bool{
	".env":                    true,
	".env.local":              true,
	".env.production":         true,
	".env.development.local":  true,
	".env.":                   true,
	".ENV":                    true,
	".Env.Local":              true,
	".env.example.local":      true,
	".ENV.EXAMPLE":            true,
	".env.example":            false,
	".env.sample":             false,
	".env.template":           false,
	".env.dist":               false,
	".envrc":                  false,
	".environment":            false,
	"production.env":          false,
	"env":                     false,
	"package.json":            false,
	"dotenv.config.js":        false,
	".env-cmdrc":              false,
	".env.example.sample.txt": true,
}

func TestIsDotenvName(t *testing.T) {
	for name, want := range dotenvNameCases {
		if got := isDotenvName(name); got != want {
			t.Errorf("isDotenvName(%q) = %v, want %v", name, got, want)
		}
	}
}

// The Seatbelt profile and the Linux walk must hide the same files. The
// profile's regular expressions are plain enough that Go's engine reads them
// the way Seatbelt does, so each name is run through both.
func TestSeatbeltDotenvRulesAgreeWithIsDotenvName(t *testing.T) {
	if len(seatbeltDotenvRules) != 1 {
		t.Fatalf("expected one deny, got %q", seatbeltDotenvRules)
	}
	m := regexp.MustCompile(`^\(deny file-read-data file-write\* \(require-all \(regex #"([^"]*)"\) \(require-not \(regex #"([^"]*)"\)\)\)\)$`).
		FindStringSubmatch(seatbeltDotenvRules[0])
	if m == nil {
		t.Fatalf("the rule is not a deny of a name minus the templates: %s", seatbeltDotenvRules[0])
	}
	names, templates := regexp.MustCompile(m[1]), regexp.MustCompile(m[2])
	for name, want := range dotenvNameCases {
		for _, dir := range []string{"/Users/dev/app", "/Users/dev/app/packages/web", "/private/var/folders/x/T/tmp.1"} {
			p := dir + "/" + name
			if got := names.MatchString(p) && !templates.MatchString(p); got != want {
				t.Errorf("Seatbelt hides %s: %v, want %v", p, got, want)
			}
		}
	}
	// A file inside a directory named like one is an ordinary file.
	if names.MatchString("/Users/dev/app/.env/bin/python") {
		t.Error("the deny matches a file inside a directory named .env")
	}
}

// The dotenv deny comes after every allow it overrides: the blanket read
// allow, the reads reopened under the home directory and the write allow.
func TestSeatbeltProfileHidesDotenvFiles(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	nvxHome := filepath.Join(home, ".nvx")
	profile := buildSeatbeltProfile(NetworkLaunchContext{Mode: "proxy"},
		filepath.Join(nvxHome, "sandbox_home", "s1"), filepath.Join(home, "projects", "app"), nvxHome, nil)

	deny := strings.Index(profile, seatbeltDotenvRules[0]+"\n")
	if deny < 0 {
		t.Fatalf("profile does not hide dotenv files:\n%s", profile)
	}
	for _, allow := range []string{"(allow file-read*)\n", "(allow file-read* (subpath ", "(allow file-write*\n"} {
		at := strings.LastIndex(profile, allow)
		if at < 0 {
			t.Fatalf("profile has no %q:\n%s", allow, profile)
		}
		if deny < at {
			t.Errorf("the dotenv deny comes before %q, which it has to override:\n%s", allow, profile)
		}
	}
}

func TestFindDotenvFilesSkipsPackagesAndGit(t *testing.T) {
	root := tempDir(t)
	files := []string{
		".env", ".env.local", ".env.example", "package.json",
		"apps/web/.env.production", "apps/web/.env.sample", "apps/web/src/index.js",
		"node_modules/pkg/.env", "apps/web/node_modules/pkg/.env.local",
		".git/.env", ".venv-named/.ENV",
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with a dotenv name is entered, and is not itself hidden.
	if err := os.MkdirAll(filepath.Join(root, ".env.d", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.d", "nested", ".env"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	found, complete := findDotenvFiles(root, dotenvScanLimit)
	if !complete {
		t.Fatal("the walk stopped early on a small tree")
	}
	var got []string
	for _, f := range found {
		rel, _ := filepath.Rel(root, f)
		got = append(got, filepath.ToSlash(rel))
	}
	slices.Sort(got)
	want := []string{".env", ".env.d/nested/.env", ".env.local", ".venv-named/.ENV", "apps/web/.env.production"}
	if !slices.Equal(got, want) {
		t.Errorf("found %q, want %q", got, want)
	}
}

// The walk is breadth first, so a tree that reaches the limit still has its
// root's .env found.
func TestFindDotenvFilesStopsAtTheLimitShallowestFirst(t *testing.T) {
	root := tempDir(t)
	deep := root
	for i := 0; i < 5; i++ {
		deep = filepath.Join(deep, fmt.Sprintf("d%d", i))
		for j := 0; j < 10; j++ {
			if err := os.MkdirAll(filepath.Join(deep, fmt.Sprintf("f%d", j)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, p := range []string{filepath.Join(root, ".env"), filepath.Join(deep, ".env")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	found, complete := findDotenvFiles(root, 20)
	if complete {
		t.Fatal("the walk reported a complete scan past its limit")
	}
	if !slices.Contains(found, filepath.Join(root, ".env")) {
		t.Errorf("the root .env was not found before the limit: %q", found)
	}
	if slices.Contains(found, filepath.Join(deep, ".env")) {
		t.Errorf("the walk went past its limit: %q", found)
	}
}
