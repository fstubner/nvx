package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// unsetEnv removes key for the test and puts it back afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

// npmPrefixFixture is a home with a default Node.js, a user home with no .npmrc
// and an empty project directory, with no prefix in the environment.
func npmPrefixFixture(t *testing.T) (nvxHome, versionDir string) {
	t.Helper()
	unsetEnv(t, "NPM_CONFIG_PREFIX")
	unsetEnv(t, "npm_config_prefix")
	unsetEnv(t, "NPM_CONFIG_USERCONFIG")
	userHome := tempDir(t)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	inEmptyDir(t)
	nvxHome = tempDir(t)
	installStubNodes(t, nvxHome, "v22.1.0", "v22.1.0")
	return nvxHome, filepath.Join(nvxHome, "versions", "node", "v22.1.0")
}

// npmListableDir is what has to exist under a prefix for `npm ls -g` to succeed:
// the prefix on Windows, and its lib directory elsewhere (see makeNpmPrefixDir).
func npmListableDir(prefix string) string {
	if runtime.GOOS == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "lib")
}

// npmIn is where the shim would find npm in a version directory.
func npmIn(versionDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(versionDir, "npm.cmd")
	}
	return filepath.Join(versionDir, "bin", "npm")
}

func prefixIn(env []string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "NPM_CONFIG_PREFIX") {
			return v
		}
	}
	return ""
}

// `nvx use` points NPM_CONFIG_PREFIX at npm_global, and nothing made it, so `npm
// ls -g` failed with ENOENT until a first global install did.
func TestUseMakesTheNpmGlobalDirectoryItPointsAt(t *testing.T) {
	nvxHome, versionDir := npmPrefixFixture(t)
	if _, err := os.Stat(filepath.Join(versionDir, "npm_global")); err == nil {
		t.Fatal("the fixture already has an npm_global")
	}

	_ = captureStdout(t, func() { emitSessionEnv("bash", nvxHome, versionDir) })

	if info, err := os.Stat(npmListableDir(filepath.Join(versionDir, "npm_global"))); err != nil || !info.IsDir() {
		t.Errorf("after `nvx use` the prefix NPM_CONFIG_PREFIX points at is not one `npm ls -g` can read: %v", err)
	}
}

// Without the integration npm used its own default, the Node.js install itself,
// so a global install landed somewhere else than in a shell with it.
func TestTheShimGivesNpmThePrefixTheIntegrationSets(t *testing.T) {
	nvxHome, versionDir := npmPrefixFixture(t)

	got := prefixIn(withDefaultNpmPrefix(nil, "npm", nvxHome, npmIn(versionDir), Policy{}))
	want := filepath.Join(versionDir, "npm_global")
	if got != want {
		t.Fatalf("NPM_CONFIG_PREFIX = %q, want %q", got, want)
	}
	if info, err := os.Stat(npmListableDir(want)); err != nil || !info.IsDir() {
		t.Errorf("the prefix npm was pointed at is not one `npm ls -g` can read: %v", err)
	}

	// The same answer the integration gives, so the two kinds of shell agree.
	out := captureStdout(t, func() { emitSessionEnv("bash", nvxHome, versionDir) })
	if line := shellEnvAssignment("bash", "NPM_CONFIG_PREFIX", FormatPathForShell("bash", want)); !strings.Contains(out, line) {
		t.Errorf("the integration does not set %q, which is what the shim gave npm:\n%s", line, out)
	}
}

// What the person chose stays chosen, and only npm is touched.
func TestTheShimLeavesAChosenNpmPrefixAlone(t *testing.T) {
	nvxHome, versionDir := npmPrefixFixture(t)
	npm := npmIn(versionDir)

	t.Run("in the environment", func(t *testing.T) {
		env := withDefaultNpmPrefix([]string{"PATH=x", "NPM_CONFIG_PREFIX=/mine"}, "npm", nvxHome, npm, Policy{})
		if got := prefixIn(env); got != "/mine" {
			t.Errorf("the prefix became %q", got)
		}
		env = withDefaultNpmPrefix([]string{"npm_config_prefix=/mine"}, "npm", nvxHome, npm, Policy{})
		if len(env) != 1 {
			t.Errorf("a lower-case npm_config_prefix was added to: %v", env)
		}
	})
	t.Run("in the user's .npmrc", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte("; mine\nregistry=https://example.test/\nprefix = /mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(filepath.Join(home, ".npmrc")) })
		if got := prefixIn(withDefaultNpmPrefix([]string{"PATH=x"}, "npm", nvxHome, npm, Policy{})); got != "" {
			t.Errorf("the prefix became %q though ~/.npmrc sets one", got)
		}
	})
	// npm 10.9.9 refuses a prefix in a project's .npmrc ("config prefix cannot be
	// changed from project config") and `npm prefix -g` ignores it, so it chooses
	// nothing and must not switch the default off.
	t.Run("a project's .npmrc is not a choice", func(t *testing.T) {
		if err := os.WriteFile(".npmrc", []byte("prefix=/theirs\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(".npmrc") })
		if got := prefixIn(withDefaultNpmPrefix([]string{"PATH=x"}, "npm", nvxHome, npm, Policy{})); got == "" {
			t.Error("a prefix in the project's .npmrc switched nvx's default off, though npm ignores it")
		}
	})
	t.Run("a commented prefix is not a choice", func(t *testing.T) {
		if err := os.WriteFile(".npmrc", []byte("# prefix=/mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(".npmrc") })
		if got := prefixIn(withDefaultNpmPrefix([]string{"PATH=x"}, "npm", nvxHome, npm, Policy{})); got == "" {
			t.Error("a commented-out prefix was read as a choice")
		}
	})
	t.Run("other commands", func(t *testing.T) {
		for _, cmd := range []string{"node", "npx", "yarn", "pnpm"} {
			if env := withDefaultNpmPrefix([]string{"PATH=x"}, cmd, nvxHome, npm, Policy{}); len(env) != 1 {
				t.Errorf("%s was given %v", cmd, env)
			}
		}
	})
	t.Run("an npm that nvx does not manage", func(t *testing.T) {
		system := filepath.Join(tempDir(t), "bin", "npm")
		if env := withDefaultNpmPrefix([]string{"PATH=x"}, "npm", nvxHome, system, Policy{}); len(env) != 1 {
			t.Errorf("a prefix was invented for a system npm: %v", env)
		}
		// Nor for a half-extracted install.
		staging := filepath.Join(nvxHome, "versions", "node", "v22.9.0.tmp.123", "bin", "npm")
		if env := withDefaultNpmPrefix([]string{"PATH=x"}, "npm", nvxHome, staging, Policy{}); len(env) != 1 {
			t.Errorf("a prefix was invented for a staging directory: %v", env)
		}
	})
}

// The wiring, end to end. An npm started by the shim has the prefix in its
// environment, and a stand-in npm prints what it was given.
func TestTheNpmShimStartsNpmWithThePrefix(t *testing.T) {
	nvxHome, versionDir := npmPrefixFixture(t)
	unsetEnv(t, nvxActiveEnvVar)
	t.Setenv("PATH", "")
	stub := filepath.Join(GetVersionBinDir(versionDir), "npm")
	body := "#!/bin/sh\necho \"PREFIX=$NPM_CONFIG_PREFIX\"\n"
	if runtime.GOOS == "windows" {
		stub = filepath.Join(versionDir, "npm.cmd")
		body = "@echo PREFIX=%NPM_CONFIG_PREFIX%\r\n"
	}
	if err := os.MkdirAll(filepath.Dir(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub, []byte(body), 0o700); err != nil { // #nosec G306 -- fixture
		t.Fatal(err)
	}

	var code int
	out := captureStdout(t, func() {
		_ = captureStderrHere(t, func() { code = runShim("npm", []string{"ls", "-g"}, nvxHome) })
	})
	if code != 0 {
		t.Fatalf("the stand-in npm exited %d:\n%s", code, out)
	}
	want := "PREFIX=" + filepath.Join(versionDir, "npm_global")
	if !strings.Contains(out, want) {
		t.Errorf("npm was started with %q, want it to contain %q", strings.TrimSpace(out), want)
	}
}
