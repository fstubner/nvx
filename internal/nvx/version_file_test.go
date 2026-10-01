package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// `nvx use` with no argument reads the project's version file.
//
// It said "Please specify a version to use" in a directory whose .nvmrc named
// one. Run as the real binary, since the argument handling is in Main.
func TestUseWithNoArgumentReadsTheProjectVersionFile(t *testing.T) {
	nvxHome := tempDir(t)
	installedLTSLayout(t, nvxHome, "v22.11.0")

	exe := filepath.Join(tempDir(t), "nvx"+exeSuffixForTest())
	if out, err := runGoBuild(exe); err != nil {
		t.Skipf("cannot build nvx here: %v\n%s", err, out)
	}
	proj := tempDir(t)
	for _, tc := range []struct{ nvmrc, wantVersion string }{
		{"lts/jod", "v22.11.0"},
		{"lts/*", "v22.11.0"},
		{"stable", "v24.1.0"},
		{"node", "v24.1.0"},
		{"v20.18.0", "v20.18.0"},
	} {
		writeFileT(t, filepath.Join(proj, ".nvmrc"), tc.nvmrc+"\n")
		cmd := execCommandForTest(exe, "use", "--shell=bash")
		cmd.Dir = proj
		cmd.Env = append(os.Environ(), "NVX_HOME="+nvxHome)
		out, err := cmd.Output()
		if err != nil {
			t.Errorf(".nvmrc %q: nvx use failed: %v", tc.nvmrc, err)
			continue
		}
		if !strings.Contains(string(out), tc.wantVersion) {
			t.Errorf(".nvmrc %q: expected the environment for %s, got:\n%s", tc.nvmrc, tc.wantVersion, out)
		}
	}

	// Nothing declared keeps the usage error.
	empty := tempDir(t)
	cmd := execCommandForTest(exe, "use", "--shell=bash")
	cmd.Dir = empty
	cmd.Env = append(os.Environ(), "NVX_HOME="+nvxHome)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "specify a version") {
		t.Errorf("no argument and no version file should be a usage error, got err=%v:\n%s", err, out)
	}
}

// What `nvx install` with no argument would install.
func TestProjectVersionSpecsReadTheFilesAutoReads(t *testing.T) {
	proj := tempDir(t)
	if got := projectVersionSpecs(proj); len(got) != 0 {
		t.Fatalf("an empty directory declared %v", got)
	}
	writeFileT(t, filepath.Join(proj, ".nvmrc"), "# comment\nlts/*\n")
	writeFileT(t, filepath.Join(proj, ".bun-version"), "1.2.0\n")
	got := projectVersionSpecs(proj)
	if len(got) != 2 || got[0].spec != "lts/*" || got[1].spec != "bun@1.2.0" {
		t.Fatalf("specs = %+v, want lts/* then bun@1.2.0", got)
	}
	if filepath.Base(got[0].source) != ".nvmrc" {
		t.Errorf("source = %s", got[0].source)
	}
}

// nvm's `node` and `stable` mean the newest version, in a .nvmrc as on the
// command line, and a bare codename names an installed LTS line.
func TestNvmAliasesResolveAgainstWhatIsInstalled(t *testing.T) {
	nvxHome := tempDir(t)
	installedLTSLayout(t, nvxHome, "v22.11.0")
	node := Providers["node"]
	for query, want := range map[string]string{
		"node":   "v24.1.0",
		"stable": "v24.1.0",
		"iron":   "v20.18.0",
		"Jod":    "v22.11.0",
	} {
		got, err := resolveLocalVersion(node, query, nvxHome)
		if err != nil || got != want {
			t.Errorf("%q resolved to %q (%v), want %s", query, got, err, want)
		}
	}
	if _, err := resolveLocalVersion(node, "hydrogen", nvxHome); err == nil {
		t.Error("a codename with nothing installed for it resolved to something")
	}

	releases := []Release{{Version: "v24.1.0"}, {Version: "v22.11.0", Lts: "Jod"}}
	for _, q := range []string{"node", "stable"} {
		r, err := ResolveVersion(q, releases)
		if err != nil || r.Version != "v24.1.0" {
			t.Errorf("remote %q resolved to %q (%v), want v24.1.0", q, r.Version, err)
		}
	}
}

// Files that look like a version declaration and are not read are named.
func TestIgnoredVersionFilesAreNamed(t *testing.T) {
	notQuiet(t)
	proj := tempDir(t)
	inProjectDir(t, proj)
	writeFileT(t, filepath.Join(proj, ".tool-versions"), "nodejs 22.11.0\n")
	writeFileT(t, filepath.Join(proj, "package.json"),
		`{"devEngines":{"runtime":{"name":"node","version":"^22"}}}`)

	got := captureStderrHere(t, func() { noteIgnoredVersionFiles(proj) })
	for _, want := range []string{".tool-versions", "devEngines.runtime"} {
		if !strings.Contains(got, want) {
			t.Errorf("no mention of %s:\n%s", want, got)
		}
	}

	writeFileT(t, filepath.Join(proj, "package.json"), `{"engines":{"node":"22"}}`)
	if err := os.Remove(filepath.Join(proj, ".tool-versions")); err != nil {
		t.Fatal(err)
	}
	if quiet := captureStderrHere(t, func() { noteIgnoredVersionFiles(proj) }); strings.TrimSpace(quiet) != "" {
		t.Errorf("nothing ignored, but nvx said:\n%s", quiet)
	}
}
