package nvx

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// What npm installs, against a registry that changes between its two runs.
//
// These run the real npm that is on the machine, uncontained, against the fake
// registry in npm_fake_registry_test.go. The registry has app-dep 1.0.0 when nvx
// runs npm's resolver and the checks, and after the resolver has finished it
// publishes app-dep, leaf-dep and extra-dep 1.0.1, the way a registry changes
// while a person waits. The tree that was checked holds 1.0.0 of each. An
// install that resolves again gets 1.0.1.

// realNodeDir finds a directory holding a real node and npm, skipping any
// directory nvx's own shims are linked in, which would run the wrong program.
func realNodeDir(t *testing.T) string {
	t.Helper()
	npm, node := "npm", "node"
	if runtime.GOOS == "windows" {
		npm, node = "npm.cmd", "node.exe"
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, nvxExecutableName())); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, npm)); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, node)); err != nil {
			continue
		}
		return dir
	}
	t.Skip("no node and npm outside nvx's own shims on PATH")
	return ""
}

// isolateNpm gives npm nothing of this machine's: its own config files, cache and
// proxy settings, and a PATH that holds only node and npm.
func isolateNpm(t *testing.T, nodeDir string) {
	t.Helper()
	t.Setenv("PATH", nodeDir)
	// Two files, because npm refuses to read one as both.
	configs := tempDir(t)
	for _, name := range []string{"user.npmrc", "global.npmrc"} {
		if err := os.WriteFile(filepath.Join(configs, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("npm_config_userconfig", filepath.Join(configs, "user.npmrc"))
	t.Setenv("npm_config_globalconfig", filepath.Join(configs, "global.npmrc"))
	t.Setenv("npm_config_cache", tempDir(t))
	t.Setenv("npm_config_update_notifier", "false")
	t.Setenv("npm_config_audit", "false")
	t.Setenv("npm_config_fund", "false")
	for _, k := range []string{"NPM_CONFIG_PREFIX", "NPM_CONFIG_REGISTRY", "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY"} {
		unsetEnv(t, k)
	}
	t.Setenv("NO_PROXY", "127.0.0.1")
	unsetEnv(t, nvxActiveEnvVar)
}

// An install project: a folder holding these files and an .npmrc that sends npm
// to the fake registry, followed by any .npmrc the files hold.
func writeInstallProject(t *testing.T, reg *fakeNpmRegistry, files map[string]string) string {
	t.Helper()
	dir := tempDir(t)
	files = mergeFiles(files, map[string]string{".npmrc": "registry=" + reg.url() + "\n" + files[".npmrc"]})
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func mergeFiles(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// publishFirstVersions is what the registry holds while the install is checked.
func publishFirstVersions(reg *fakeNpmRegistry) {
	reg.reset()
	reg.publish("leaf-dep", "1.0.0", nil)
	reg.publish("app-dep", "1.0.0", map[string]string{"leaf-dep": "^1.0.0"})
	reg.publish("extra-dep", "1.0.0", nil)
}

// publishNewVersions is what it holds once the checks are done.
func publishNewVersions(reg *fakeNpmRegistry) {
	reg.publish("leaf-dep", "1.0.1", nil)
	reg.publish("app-dep", "1.0.1", map[string]string{"leaf-dep": "^1.0.0"})
	reg.publish("extra-dep", "1.0.1", nil)
}

// installedVersions reads the version of each named package in the project's
// node_modules, "" for one that is not there.
func installedVersions(t *testing.T, dir string, names ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, "node_modules", name, "package.json"))
		if err != nil {
			out[name] = ""
			continue
		}
		var m struct{ Version string }
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s/package.json: %v", name, err)
		}
		out[name] = m.Version
	}
	return out
}

// installOutcome is what a run of npm left in the project.
type installOutcome struct {
	exit     int
	manifest string
	lock     string
	versions map[string]string
	output   string
}

var installedNames = []string{"app-dep", "leaf-dep", "extra-dep"}

func readOutcome(t *testing.T, dir string, exit int, output string) installOutcome {
	t.Helper()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return ""
		}
		return string(data)
	}
	return installOutcome{exit: exit, manifest: read("package.json"), lock: read("package-lock.json"),
		versions: installedVersions(t, dir, installedNames...), output: output}
}

// runNativeNpm runs npm as it is, with nvx nowhere, in a project made from files
// against the registry as it is now.
func runNativeNpm(t *testing.T, nodeDir string, reg *fakeNpmRegistry, files map[string]string, args ...string) installOutcome {
	t.Helper()
	dir := writeInstallProject(t, reg, files)
	npm := "npm"
	if runtime.GOOS == "windows" {
		npm = filepath.Join(nodeDir, "npm.cmd")
	}
	cmd := exec.Command(npm, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		exit = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		}
	}
	return readOutcome(t, dir, exit, string(out))
}

// runInstallThroughNvx runs the same command through runShim, which checks it
// first and then installs it. After npm's resolver has finished and before the
// install starts, the registry publishes its new versions. It returns the
// outcome and what npm asked the registry for once the new versions were out.
func runInstallThroughNvx(t *testing.T, reg *fakeNpmRegistry, files map[string]string, args ...string) (installOutcome, []fakeNpmRequest) {
	t.Helper()
	home := tempDir(t)
	if err := os.WriteFile(filepath.Join(home, "policy.json"), []byte(`{"isolation":{"enabled":false},"typosquatting":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "popular_packages.json"), []byte(`["react","lodash"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := writeInstallProject(t, reg, files)
	inProjectDir(t, dir)
	t.Setenv("NVX_NONINTERACTIVE", "1")
	t.Setenv("NVX_YES", "")
	packuments.Range(func(k, _ any) bool { packuments.Delete(k); return true })

	published := -1
	orig := launchNpmResolution
	t.Cleanup(func() { launchNpmResolution = orig })
	launchNpmResolution = func(cfg SandboxConfig, contain bool) int {
		code := orig(cfg, contain)
		if code == 0 {
			publishNewVersions(reg)
			published = len(reg.requests())
		}
		return code
	}

	var code int
	var out string
	stderr := captureStderrHere(t, func() {
		out = captureStdout(t, func() { code = runShim("npm", args, home) })
	})
	if published < 0 {
		t.Fatalf("npm's resolver never ran, so the registry never changed:\n%s", stderr)
	}
	var after []fakeNpmRequest
	for _, r := range reg.requests()[published:] {
		if r.byNpm() {
			after = append(after, r)
		}
	}
	return readOutcome(t, dir, code, out+stderr), after
}

type installScenario struct {
	name  string
	files map[string]string
	args  []string
	// packuments is how many metadata requests npm may make once the registry
	// has changed. A bare install makes none. Each named package is asked
	// about once, because npm reads a package it was told to install as a
	// request, whatever the lockfile says.
	packuments int
}

func installScenarios() []installScenario {
	app := `{"name":"app","version":"1.0.0","dependencies":{"app-dep":"^1.0.0"}}`
	return []installScenario{
		{
			name:  "bare install with no lockfile",
			files: map[string]string{"package.json": app},
			args:  []string{"install"},
		},
		{
			name:       "install of a name, a tag and -D",
			files:      map[string]string{"package.json": `{"name":"app","version":"1.0.0"}`},
			args:       []string{"install", "-D", "app-dep@latest", "extra-dep"},
			packuments: 2,
		},
	}
}

// The tree npm installs is the tree that was checked, and the project is left
// the way npm would have left it. The registry changes between npm's resolver and
// the install, so an install that resolved again would get the new versions, and
// the project's package.json and lockfile would say so.
func TestAnInstallGetsTheTreeThatWasChecked(t *testing.T) {
	nodeDir := realNodeDir(t)
	for _, sc := range installScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			isolateNpm(t, nodeDir)
			reg := newFakeNpmRegistry(t)

			// What npm leaves when the registry holds the first versions throughout.
			publishFirstVersions(reg)
			native := runNativeNpm(t, nodeDir, reg, sc.files, sc.args...)
			if native.exit != 0 {
				t.Fatalf("npm on its own failed:\n%s", native.output)
			}

			publishFirstVersions(reg)
			got, asked := runInstallThroughNvx(t, reg, sc.files, sc.args...)
			if got.exit != 0 {
				t.Fatalf("the install exited %d:\n%s", got.exit, got.output)
			}
			if got.manifest != native.manifest {
				t.Errorf("package.json is not what npm leaves:\n%s\nnpm leaves:\n%s", got.manifest, native.manifest)
			}
			if got.lock != native.lock {
				t.Errorf("package-lock.json is not what npm leaves:\n%s\nnpm leaves:\n%s", got.lock, native.lock)
			}
			for name, want := range native.versions {
				if got.versions[name] != want {
					t.Errorf("%s is installed at %q, and the tree that was checked holds %q", name, got.versions[name], want)
				}
			}
			var packuments int
			for _, r := range asked {
				if r.forPackument() {
					packuments++
				}
			}
			if packuments > sc.packuments {
				t.Errorf("npm asked the registry for %d packages' metadata after the checks, want at most %d: %v", packuments, sc.packuments, asked)
			}
		})
	}
}

// A control for the test above. With the lockfile not handed over, the same
// install resolves again and gets what the registry holds by then, so the test
// above can fail, and does when the install resolves afresh.
func TestWithoutTheHandoffAnInstallGetsTheNewVersions(t *testing.T) {
	nodeDir := realNodeDir(t)
	isolateNpm(t, nodeDir)
	reg := newFakeNpmRegistry(t)

	orig := adoptCheckedLockfile
	t.Cleanup(func() { adoptCheckedLockfile = orig })
	adoptCheckedLockfile = false

	for _, sc := range installScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			publishFirstVersions(reg)
			got, _ := runInstallThroughNvx(t, reg, sc.files, sc.args...)
			if got.exit != 0 {
				t.Fatalf("the install exited %d:\n%s", got.exit, got.output)
			}
			if got.versions["app-dep"] != "1.0.1" {
				t.Fatalf("app-dep is installed at %q: the registry changed and the install did not see it, so the test above shows nothing\n%s", got.versions["app-dep"], got.output)
			}
		})
	}
}

// A project whose settings leave the lockfile alone, or a command that does not
// save, ends up as it would with npm alone, and the lockfile nvx handed over is
// not left behind.
func TestTheHandedOverLockfileIsNotLeftWhereNpmWouldNotWriteOne(t *testing.T) {
	nodeDir := realNodeDir(t)
	app := `{"name":"app","version":"1.0.0","dependencies":{"app-dep":"^1.0.0"}}`
	cases := []struct {
		name  string
		files map[string]string
		args  []string
	}{
		// npm reads no lockfile and writes none.
		{"package-lock=false in .npmrc", map[string]string{"package.json": app, ".npmrc": "package-lock=false\n"}, []string{"install"}},
		// npm reads the lockfile and writes none.
		{"--no-save", map[string]string{"package.json": app}, []string{"install", "--no-save"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateNpm(t, nodeDir)
			reg := newFakeNpmRegistry(t)
			publishFirstVersions(reg)
			native := runNativeNpm(t, nodeDir, reg, tc.files, tc.args...)
			if native.exit != 0 {
				t.Fatalf("npm on its own failed:\n%s", native.output)
			}

			publishFirstVersions(reg)
			got, _ := runInstallThroughNvx(t, reg, tc.files, tc.args...)
			if got.exit != 0 {
				t.Fatalf("the install exited %d:\n%s", got.exit, got.output)
			}
			if got.manifest != native.manifest {
				t.Errorf("package.json is not what npm leaves:\n%s\nnpm leaves:\n%s", got.manifest, native.manifest)
			}
			if got.lock != native.lock {
				t.Errorf("package-lock.json is not what npm leaves:\n%q\nnpm leaves:\n%q", got.lock, native.lock)
			}
		})
	}
}
