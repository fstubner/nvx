package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// npmBinName returns the platform-correct filename for a version-dir-relative
// npm/npx binary, mirroring ResolveBinary's own naming.
func npmBinName(cmd string) string {
	if runtime.GOOS != "windows" {
		return cmd
	}
	if cmd == "node" {
		return cmd + ".exe"
	}
	return cmd + ".cmd"
}

func writeStubBinary(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { // #nosec G301 -- test fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0755); err != nil { // #nosec G306 -- test fixture
		t.Fatal(err)
	}
}

// bundledBinPath returns where ResolveBinary looks for cmd when there is no
// npm_global override, matching its own windows/unix branching.
func bundledBinPath(versionDir, cmd string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(versionDir, npmBinName(cmd))
	}
	return filepath.Join(versionDir, "bin", cmd)
}

func TestResolveBinaryPrefersNpmGlobalOverBundled(t *testing.T) {
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v20.0.0")

	// Bundled npm (ships with every Node download) — always present.
	writeStubBinary(t, bundledBinPath(versionDir, "npm"))

	provider := NodeProvider{}

	// No self-update yet: resolves to the bundled binary.
	got := provider.ResolveBinary("npm", nvxHome, "v20.0.0")
	want := bundledBinPath(versionDir, "npm")
	if got != want {
		t.Fatalf("before self-update: ResolveBinary(npm) = %q, want %q", got, want)
	}

	// Simulate `npm install -g npm@x` with NPM_CONFIG_PREFIX set to
	// versionDir/npm_global (what a real interactive session sets) — npm
	// lands in the prefix's bin dir, not the bundled node_modules/npm.
	overridePath := filepath.Join(GetNpmPrefixBinDir(filepath.Join(versionDir, "npm_global")), npmBinName("npm"))
	writeStubBinary(t, overridePath)

	got = provider.ResolveBinary("npm", nvxHome, "v20.0.0")
	if got != overridePath {
		t.Fatalf("after self-update: ResolveBinary(npm) = %q, want the npm_global override %q", got, overridePath)
	}
}

func TestResolveBinaryNpxAlsoPrefersNpmGlobal(t *testing.T) {
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
	writeStubBinary(t, bundledBinPath(versionDir, "npx"))

	overridePath := filepath.Join(GetNpmPrefixBinDir(filepath.Join(versionDir, "npm_global")), npmBinName("npx"))
	writeStubBinary(t, overridePath)

	provider := NodeProvider{}
	got := provider.ResolveBinary("npx", nvxHome, "v20.0.0")
	if got != overridePath {
		t.Fatalf("ResolveBinary(npx) = %q, want the npm_global override %q", got, overridePath)
	}
}

func TestResolveBinaryNodeNeverChecksNpmGlobal(t *testing.T) {
	// node itself is never replaced by `npm install -g` — a stray npm_global
	// entry named "node" (shouldn't normally exist, but be defensive) must
	// never be preferred over the real bundled node binary.
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
	writeStubBinary(t, bundledBinPath(versionDir, "node"))

	decoyPath := filepath.Join(GetNpmPrefixBinDir(filepath.Join(versionDir, "npm_global")), npmBinName("node"))
	writeStubBinary(t, decoyPath)

	provider := NodeProvider{}
	got := provider.ResolveBinary("node", nvxHome, "v20.0.0")
	want := bundledBinPath(versionDir, "node")
	if got != want {
		t.Fatalf("ResolveBinary(node) = %q, want the bundled binary %q (npm_global must not apply to node)", got, want)
	}
}

func TestResolveBinaryFallsBackWhenNoOverrideExists(t *testing.T) {
	// No npm_global at all (npm_global directory doesn't exist) — must not
	// error or panic, just fall through to the bundled path.
	nvxHome := tempDir(t)
	versionDir := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
	writeStubBinary(t, bundledBinPath(versionDir, "npm"))

	provider := NodeProvider{}
	got := provider.ResolveBinary("npm", nvxHome, "v20.0.0")
	want := bundledBinPath(versionDir, "npm")
	if got != want {
		t.Fatalf("ResolveBinary(npm) with no npm_global = %q, want bundled %q", got, want)
	}
}
