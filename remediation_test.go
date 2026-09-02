package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestParseRuntimeSpecDefaultsBareVersionsToNode(t *testing.T) {
	cases := []struct {
		arg         string
		wantRuntime string
		wantVersion string
	}{
		{"20", "node", "20"},
		{"v20.11.0", "node", "v20.11.0"},
		{"lts", "node", "lts"},
		{"latest", "node", "latest"},
		{"node", "node", "latest"},
		{"node@18", "node", "18"},
		{"node@lts", "node", "lts"},
	}
	for _, tc := range cases {
		provider, version := parseRuntimeSpec(tc.arg)
		if provider.Name() != tc.wantRuntime {
			t.Errorf("parseRuntimeSpec(%q) runtime = %q, want %q", tc.arg, provider.Name(), tc.wantRuntime)
		}
		if version != tc.wantVersion {
			t.Errorf("parseRuntimeSpec(%q) version = %q, want %q", tc.arg, version, tc.wantVersion)
		}
	}
}

func TestBinResolveCacheHitAndInvalidation(t *testing.T) {
	nvxHome := tempDir(t)
	binDir := tempDir(t)
	bin := filepath.Join(binDir, "node.exe")
	if err := os.WriteFile(bin, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	storeBinCache(nvxHome, "node", bin)
	if got := lookupBinCache(nvxHome, "node"); got != bin {
		t.Fatalf("expected cache hit %q, got %q", bin, got)
	}

	// A changed PATH must invalidate the cache (different hash).
	t.Setenv("PATH", filepath.Join(tempDir(t), "other"))
	if got := lookupBinCache(nvxHome, "node"); got != "" {
		t.Fatalf("changed PATH must invalidate cache, got %q", got)
	}

	// Same PATH but the cached binary is gone -> miss (re-validated by stat).
	t.Setenv("PATH", binDir)
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if got := lookupBinCache(nvxHome, "node"); got != "" {
		t.Fatalf("removed binary must invalidate cache, got %q", got)
	}
}

func TestDockerRunArgsEnforcesOfflineAndHardening(t *testing.T) {
	cfg := SandboxConfig{Command: "node", Args: []string{"-e", "1"}}

	offline := dockerRunArgs("node:20", "/work", cfg, nil, NetworkLaunchContext{Mode: "offline"})
	joined := strings.Join(offline, " ")
	if !strings.Contains(joined, "--network none") {
		t.Fatalf("offline mode must add --network none, got: %v", offline)
	}
	for _, flag := range []string{"--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=512"} {
		if !strings.Contains(joined, flag) {
			t.Fatalf("expected hardening flag %s, got: %v", flag, offline)
		}
	}
	// image, then command, then its args, in order at the tail.
	tail := offline[len(offline)-4:]
	want := []string{"node:20", "node", "-e", "1"}
	if strings.Join(tail, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("expected image/command tail %v, got: %v", want, tail)
	}

	open := dockerRunArgs("node:20", "/work", cfg, nil, NetworkLaunchContext{Mode: "open"})
	if strings.Contains(strings.Join(open, " "), "--network none") {
		t.Fatalf("open mode must not add --network none, got: %v", open)
	}
}

func TestFilesystemProviderRegistryAndExperimentalGating(t *testing.T) {
	for _, alias := range []string{"native", "docker", "seatbelt", "sandbox-exec", "wslc", "container", "wsl", "nspawn", "systemd-nspawn"} {
		if _, ok := lookupFilesystemProvider(alias); !ok {
			t.Errorf("expected registry to resolve alias %q", alias)
		}
	}
	if _, ok := lookupFilesystemProvider("does-not-exist"); ok {
		t.Error("unknown provider must not resolve")
	}

	firstClass := []string{"native", "docker", "sandbox-exec"}
	for _, name := range firstClass {
		p, _ := lookupFilesystemProvider(name)
		if p.Experimental() {
			t.Errorf("%s must not be experimental", name)
		}
	}
	for _, name := range []string{"wsl", "wslc", "systemd-nspawn"} {
		p, _ := lookupFilesystemProvider(name)
		if !p.Experimental() {
			t.Errorf("%s must be experimental", name)
		}
	}
}

func TestProviderSupportsNetworkModeDockerEnforcesOffline(t *testing.T) {
	if !providerSupportsNetworkMode("docker", "offline") {
		t.Error("docker should enforce offline via --network none")
	}
	if !providerSupportsNetworkMode("docker", "loopback") {
		t.Error("docker should enforce loopback via --network none")
	}
	if providerSupportsNetworkMode("docker", "proxy") {
		t.Error("docker must not claim proxy-mode enforcement")
	}
}

func TestSandboxImageSelection(t *testing.T) {
	if got := (NodeProvider{}).SandboxImage("v20.11.0"); got != "node:20.11.0" {
		t.Errorf("node image = %q, want node:20.11.0", got)
	}
	if got := (BunProvider{}).SandboxImage("v1.2.19"); got != "oven/bun:1.2.19" {
		t.Errorf("bun image = %q, want oven/bun:1.2.19", got)
	}
	if got := (NodeProvider{}).SandboxImage(""); got != "node:latest" {
		t.Errorf("node image (no version) = %q, want node:latest", got)
	}
}

func TestParseRuntimeSpecSelectsBun(t *testing.T) {
	cases := []struct {
		arg         string
		wantRuntime string
		wantVersion string
	}{
		{"bun", "bun", "latest"},
		{"bun@1.2", "bun", "1.2"},
		{"bun@1.2.19", "bun", "1.2.19"},
		{"bun@latest", "bun", "latest"},
	}
	for _, tc := range cases {
		provider, version := parseRuntimeSpec(tc.arg)
		if provider.Name() != tc.wantRuntime || version != tc.wantVersion {
			t.Errorf("parseRuntimeSpec(%q) = (%s, %q), want (%s, %q)", tc.arg, provider.Name(), version, tc.wantRuntime, tc.wantVersion)
		}
	}
}

func TestBunxRoutesToBunProviderAndNotNode(t *testing.T) {
	if got := runtimeForShim("bunx").Name(); got != "bun" {
		t.Fatalf("runtimeForShim(bunx) = %q, want bun", got)
	}
	if got := runtimeForShim("bun").Name(); got != "bun" {
		t.Fatalf("runtimeForShim(bun) = %q, want bun", got)
	}
	for _, cmd := range (NodeProvider{}).ShimCommands() {
		if cmd == "bunx" || cmd == "bun" {
			t.Fatalf("NodeProvider must not own %q shim after Bun provider added", cmd)
		}
	}
}

func TestBunTagToVersion(t *testing.T) {
	cases := map[string]string{
		"bun-v1.2.19": "v1.2.19",
		"v1.2.19":     "v1.2.19",
		"1.2.19":      "v1.2.19",
		"bun-canary":  "",
		"":            "",
	}
	for in, want := range cases {
		if got := bunTagToVersion(in); got != want {
			t.Errorf("bunTagToVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsFullBunVersionAndMatch(t *testing.T) {
	if !isExactSemver("v1.2.19") || !isExactSemver("1.0.0") {
		t.Fatal("expected 3-part versions to be full")
	}
	if isExactSemver("v1.2") || isExactSemver("v1") || isExactSemver("v1.2.x") {
		t.Fatal("partial or non-numeric versions must not be full")
	}
	versions := []string{"v1.2.19", "v1.2.5", "v1.1.40", "v0.8.1"}
	if got := matchVersionPrefix("v1.2", versions); got != "v1.2.19" {
		t.Errorf("matchVersionPrefix(v1.2) = %q, want v1.2.19 (newest match)", got)
	}
	if got := matchVersionPrefix("v1", versions); got != "v1.2.19" {
		t.Errorf("matchVersionPrefix(v1) = %q, want v1.2.19", got)
	}
	if got := matchVersionPrefix("v1.2.5", versions); got != "v1.2.5" {
		t.Errorf("matchVersionPrefix(v1.2.5) = %q, want exact", got)
	}
}

func TestBunProviderDetectConfig(t *testing.T) {
	tmp := tempDir(t)
	if err := os.WriteFile(filepath.Join(tmp, ".bun-version"), []byte("1.2.19\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ver, src, err := (BunProvider{}).DetectConfig(tmp)
	if err != nil || ver != "1.2.19" {
		t.Fatalf("DetectConfig(.bun-version) = (%q, %q, %v), want 1.2.19", ver, src, err)
	}

	tmp2 := tempDir(t)
	if err := os.WriteFile(filepath.Join(tmp2, "package.json"), []byte(`{"engines":{"bun":">=1.1.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	ver2, _, err := (BunProvider{}).DetectConfig(tmp2)
	if err != nil || ver2 != ">=1.1.0" {
		t.Fatalf("DetectConfig(engines.bun) = %q, want >=1.1.0", ver2)
	}
}

func TestFindShasumEntryFormats(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	cases := []struct {
		name    string
		content string
		file    string
		want    string
	}{
		{"standard two-field", hash + "  deno-x.zip\n", "deno-x.zip", hash},
		{"binary-mode star", hash + " *deno-x.zip\n", "deno-x.zip", hash},
		{"dot-slash prefix", hash + "  ./deno-x.zip\n", "deno-x.zip", hash},
		{"lone hash single entry", hash + "\n", "deno-x.zip", hash},
		{"wrong filename", hash + "  other.zip\n", "deno-x.zip", ""},
		{"lone hash but multiple lines", hash + "\n" + hash + "  other.zip\n", "deno-x.zip", ""},
		{"not a hash", "hello  deno-x.zip\n", "deno-x.zip", ""},
		{"get-filehash format", "\r\nAlgorithm : SHA256\r\nHash      : " + hash + "\r\nPath      : C:\\w\\deno-x.zip\r\n", "deno-x.zip", hash},
		{"get-filehash no path line", "Algorithm : SHA256\nHash : " + hash + "\n", "deno-x.zip", hash},
		{"get-filehash wrong path", "Hash : " + hash + "\nPath : C:\\w\\other.zip\n", "deno-x.zip", ""},
	}
	for _, tc := range cases {
		if got := findShasumEntry(tc.content, tc.file); got != tc.want {
			t.Errorf("%s: findShasumEntry = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWindowsSetupStateRoundTrip(t *testing.T) {
	nvxHome := tempDir(t)
	if _, ok := readWindowsSetupState(nvxHome); ok {
		t.Fatal("no marker should exist initially")
	}
	want := windowsSetupState{
		AppContainerSID: "S-1-15-2-1-2-3",
		GrantedPaths:    []string{`C:\`, `C:\Users`},
		LoopbackExempt:  true,
	}
	if err := writeWindowsSetupState(nvxHome, want); err != nil {
		t.Fatal(err)
	}
	got, ok := readWindowsSetupState(nvxHome)
	if !ok || got.AppContainerSID != want.AppContainerSID || !got.LoopbackExempt || len(got.GrantedPaths) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.SetupAt == "" {
		t.Error("SetupAt should be stamped on write")
	}
	if err := clearWindowsSetupState(nvxHome); err != nil {
		t.Fatal(err)
	}
	if _, ok := readWindowsSetupState(nvxHome); ok {
		t.Fatal("marker should be gone after clear")
	}
}

func TestIsPackageManagerCommand(t *testing.T) {
	managers := []string{"npm", "npx", "yarn", "pnpm", "npm.cmd", "NPX.CMD"}
	// A resolved cmdPath is a full path, so cover that too -- but with a path the
	// host's filepath actually parses. `C:\x\npm.cmd` was asserted unconditionally
	// here, and on Linux/macOS filepath.Base does not treat `\` as a separator, so
	// the whole string came back as the basename and never matched. That failed
	// `go test ./...` on ubuntu-latest and macos-latest for 51 commits.
	if runtime.GOOS == "windows" {
		managers = append(managers, `C:\x\npm.cmd`)
	} else {
		managers = append(managers, "/usr/local/bin/npm")
	}
	for _, cmd := range managers {
		if !isPackageManagerCommand(cmd) {
			t.Errorf("expected %q to be a package manager", cmd)
		}
	}
	for _, cmd := range []string{"node", "bun", "deno", "go", "python", "cowsay"} {
		if isPackageManagerCommand(cmd) {
			t.Errorf("%q should not be a package manager", cmd)
		}
	}
}

func TestRuntimeFromVersionDirRecognizesKnownRuntimes(t *testing.T) {
	nvxHome := filepath.Join("some", "home")
	nodeDir := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
	if got := runtimeFromVersionDir(nvxHome, nodeDir); got != "node" {
		t.Errorf("runtimeFromVersionDir(node path) = %q, want node", got)
	}
	bunDir := filepath.Join(nvxHome, "versions", "bun", "v1.2.19")
	if got := runtimeFromVersionDir(nvxHome, bunDir); got != "bun" {
		t.Errorf("runtimeFromVersionDir(bun path) = %q, want bun", got)
	}
	// Legacy flat layout (no runtime segment) resolves to "" so callers keep
	// node-compatible PATH behavior.
	legacy := filepath.Join(nvxHome, "versions", "v20.0.0")
	if got := runtimeFromVersionDir(nvxHome, legacy); got != "" {
		t.Errorf("runtimeFromVersionDir(legacy path) = %q, want empty", got)
	}
}

func TestParseStartupFlagsDoesNotConsumeShimPayloadFlags(t *testing.T) {
	args, yes, noSandbox, strict, standard := parseStartupFlags([]string{"nvx", "shim", "npx", "-y", "create-vite", "--no-sandbox"})

	if yes {
		t.Fatal("shim payload -y must not enable nvx --yes")
	}
	if noSandbox {
		t.Fatal("shim payload --no-sandbox must not disable nvx sandboxing")
	}
	if strict || standard {
		t.Fatal("shim payload --strict/--standard must not set the leading nvx flags")
	}
	want := []string{"nvx", "shim", "npx", "-y", "create-vite", "--no-sandbox"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args changed unexpectedly: got %v want %v", args, want)
	}
}

func TestParseStartupFlagsOnlyConsumesLeadingGlobalFlags(t *testing.T) {
	args, yes, noSandbox, strict, standard := parseStartupFlags([]string{"nvx", "--yes", "--no-sandbox", "--strict", "shim", "node", "-e", "1"})

	if !yes {
		t.Fatal("leading --yes should enable nvx yes mode")
	}
	if !noSandbox {
		t.Fatal("leading --no-sandbox should enable nvx no-sandbox mode")
	}
	if !strict {
		t.Fatal("leading --strict should enable nvx strict mode")
	}
	if standard {
		t.Fatal("standard should not be set when only --strict was passed")
	}
	want := []string{"nvx", "shim", "node", "-e", "1"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args mismatch: got %v want %v", args, want)
	}
}

func TestShellEnvAssignmentEscapesBashValues(t *testing.T) {
	got := shellEnvAssignment("bash", "PATH", `/tmp/nvx$(touch pwned)'/bin`)
	want := "export PATH='/tmp/nvx$(touch pwned)'\"'\"'/bin'\n"
	if got != want {
		t.Fatalf("unexpected bash assignment:\ngot  %q\nwant %q", got, want)
	}
}

func TestShellEnvAssignmentEscapesPowerShellValues(t *testing.T) {
	got := shellEnvAssignment("powershell", "PATH", `C:\nvx'; Write-Error pwned; #'`)
	want := "$env:PATH = 'C:\\nvx''; Write-Error pwned; #'''\n"
	if got != want {
		t.Fatalf("unexpected PowerShell assignment:\ngot  %q\nwant %q", got, want)
	}
}

func TestPersistedAllowHostsMergeWithoutDisablingDefaultProtections(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	// Written through the store directly rather than through the egress prompt.
	// Nothing writes AllowHosts any more -- an approval is for one run -- but the
	// READ path still has to merge grants an older nvx persisted, without that
	// merge switching anything else off. That is what this checks.
	//
	// Keyed by projectScopeDir() rather than by projectDir, because that is what
	// the reader resolves and the two are not the same string everywhere: on macOS
	// /var is a symlink to /private/var, so MkdirTemp hands back an unresolved path
	// while Getwd after the chdir returns the resolved one. Using the literal
	// filed the grant under a key LoadPolicy never looks up, and the test passed on
	// Windows -- where the paths do match -- and failed on macOS CI.
	scope := projectScopeDir()
	if scope == "" {
		t.Fatal("projectScopeDir() is empty, so the grant cannot be filed where the reader will look")
	}
	if err := saveProjectGrants(nvxHome, projectGrants{
		ProjectPath: scope,
		AllowHosts:  []string{"example.com:443"},
	}); err != nil {
		t.Fatalf("saveProjectGrants: %v", err)
	}

	// The grant must be stored under nvxHome, never written into the project tree.
	if _, err := os.Stat(filepath.Join(projectDir, ".nvx-policy.json")); err == nil {
		t.Fatal("persisted allow host must not create a policy file inside the project")
	}
	entries, err := os.ReadDir(filepath.Join(nvxHome, "grants"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a grant file under nvxHome/grants, err=%v entries=%v", err, entries)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatalf("LoadPolicy returned error: %v", err)
	}
	if !loaded.Typosquatting.Enabled {
		t.Fatal("persisted allow host must not disable typosquatting")
	}
	if !loaded.Isolation.Enabled {
		t.Fatal("persisted allow host must not disable isolation")
	}
	found := false
	for _, host := range loaded.Isolation.Network.AllowHosts {
		if host == "example.com:443" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected persisted allow host, got %v", loaded.Isolation.Network.AllowHosts)
	}
}

func TestLoadPolicyIgnoresUntrustedLooseningProjectPolicy(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}
	// A project policy that weakens several protections at once.
	body := `{
  "isolation": {"network": {"mode": "open", "allow_hosts": ["untrusted.example:443"]}},
  "typosquatting": {"enabled": false}
}`
	if err := os.WriteFile(filepath.Join(projectDir, ".nvx-policy.json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(loaded.Isolation.Network.Mode, "open") {
		t.Fatal("untrusted project policy must not switch network mode to open")
	}
	if !loaded.Typosquatting.Enabled {
		t.Fatal("untrusted project policy must not disable typosquatting")
	}
	for _, host := range loaded.Isolation.Network.AllowHosts {
		if host == "untrusted.example:443" {
			t.Fatal("untrusted project policy must not add egress hosts")
		}
	}
}

func TestLoadPolicyHonorsTrustedLooseningProjectPolicy(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(projectDir, ".nvx-policy.json")
	body := `{"isolation": {"network": {"allow_hosts": ["trusted.example:443"]}}}`
	if err := os.WriteFile(policyPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	// Pre-record trust for the exact file contents (as an accepted prompt would).
	// Pin the exact path LoadPolicy discovers, via the same helper, so the key
	// matches on every platform regardless of symlink/short-path spelling.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	g.ProjectPath = scope
	for _, p := range collectProjectPolicyPaths(cwd, nvxHome) {
		if strings.HasSuffix(p, ".nvx-policy.json") {
			if hash, ok := hashPolicyFile(p); ok {
				g.PolicyPins[filepath.Clean(p)] = hash
			}
		}
	}
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, host := range loaded.Isolation.Network.AllowHosts {
		if host == "trusted.example:443" {
			found = true
		}
	}
	if !found {
		t.Fatalf("trusted project policy should be applied, got %v", loaded.Isolation.Network.AllowHosts)
	}
}

func TestLoadPolicyReturnsErrorForMalformedPolicy(t *testing.T) {
	tmp := tempDir(t)
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvxHome, "policy.json"), []byte(`{"typosquatting":`), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadPolicy(nvxHome); err == nil {
		t.Fatal("expected malformed global policy to return an error")
	}
}

func TestDetectShimPackagesForVerificationUsesPackageLockForPlainInstall(t *testing.T) {
	tmp := tempDir(t)
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	lock := `{
  "packages": {
    "": {"name": "app", "version": "1.0.0"},
    "node_modules/left-pad": {"version": "1.3.0"},
    "node_modules/@types/node": {"version": "20.0.0"}
  }
}`
	if err := os.WriteFile(filepath.Join(tmp, "package-lock.json"), []byte(lock), 0644); err != nil {
		t.Fatal(err)
	}

	got := detectShimPackagesForVerification("npm", []string{"ci"})
	sort.Strings(got)
	want := []string{"@types/node@20.0.0", "left-pad@1.3.0"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("packages mismatch: got %v want %v", got, want)
	}
}

func TestDetectShimPackagesForVerificationFallsBackToPackageJSON(t *testing.T) {
	tmp := tempDir(t)
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	pkg := `{
  "dependencies": {"left-pad": "^1.3.0"},
  "devDependencies": {"typescript": "~5.0.0"},
  "optionalDependencies": {"fsevents": "2.3.3"}
}`
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}

	got := detectShimPackagesForVerification("npm", []string{"install"})
	sort.Strings(got)
	want := []string{"fsevents", "left-pad", "typescript"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("packages mismatch: got %v want %v", got, want)
	}
}

func TestDetectShimPackagesForVerificationHandlesNpxAndBunx(t *testing.T) {
	tests := []struct {
		cmd  string
		args []string
		want []string
	}{
		{"npx", []string{"-y", "create-vite@latest", "app"}, []string{"create-vite@latest"}},
		{"npx", []string{"--package", "cowsay@1.5.0", "cowsay", "hello"}, []string{"cowsay@1.5.0"}},
		{"bunx", []string{"tsx@4.0.0", "script.ts"}, []string{"tsx@4.0.0"}},
	}

	for _, tc := range tests {
		got := detectShimPackagesForVerification(tc.cmd, tc.args)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Fatalf("%s %v: got %v want %v", tc.cmd, tc.args, got, tc.want)
		}
	}
}

func TestRunVerifyInstallFailsClosedOnMetadataFailure(t *testing.T) {
	if os.Getenv("NVX_TEST_VERIFY_METADATA_FAILURE") == "1" {
		resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
			return "", time.Time{}, false, fmt.Errorf("metadata unavailable")
		}
		// os.Exit here, not inside runVerifyInstall: the function returns its
		// exit code now so that callers can record the run before exiting. What
		// this test asserts is unchanged -- the child must still exit non-zero.
		code, _ := runVerifyInstall([]string{"not-a-typo-risk"}, testNvxHomeWithTyposquattingDisabled(t))
		os.Exit(code)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRunVerifyInstallFailsClosedOnMetadataFailure")
	cmd.Env = append(os.Environ(), "NVX_TEST_VERIFY_METADATA_FAILURE=1", "NVX_NONINTERACTIVE=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected metadata failure to deny installation")
	}
}

func TestRunVerifyInstallFailsClosedOnOSVFailure(t *testing.T) {
	if os.Getenv("NVX_TEST_VERIFY_OSV_FAILURE") == "1" {
		resolveNpmPackageDetailsForVerify = func(pkgName, versionQuery string) (string, time.Time, bool, error) {
			return "1.0.0", time.Time{}, false, nil
		}
		scanVulnerabilitiesBatchForVerify = func(packages []OSVQuery) (map[string][]OSVVuln, error) {
			return nil, fmt.Errorf("osv unavailable")
		}
		// os.Exit here, not inside runVerifyInstall: the function returns its
		// exit code now so that callers can record the run before exiting. What
		// this test asserts is unchanged -- the child must still exit non-zero.
		code, _ := runVerifyInstall([]string{"not-a-typo-risk"}, testNvxHomeWithTyposquattingDisabled(t))
		os.Exit(code)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRunVerifyInstallFailsClosedOnOSVFailure")
	cmd.Env = append(os.Environ(), "NVX_TEST_VERIFY_OSV_FAILURE=1", "NVX_NONINTERACTIVE=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected OSV failure to deny installation")
	}
}

func testNvxHomeWithTyposquattingDisabled(t *testing.T) string {
	t.Helper()
	nvxHome := tempDir(t)
	policy := `{"typosquatting":{"enabled":false}}`
	if err := os.WriteFile(filepath.Join(nvxHome, "policy.json"), []byte(policy), 0644); err != nil {
		t.Fatal(err)
	}
	return nvxHome
}

func TestShouldSandboxHonorsSandboxEnvironment(t *testing.T) {
	t.Setenv("NVX_SANDBOX", "1")
	if shouldSandbox("node", nil, DefaultPolicy(), shimOptions{}) {
		t.Fatal("nested shim invocation inside an existing sandbox must not start another sandbox")
	}
}

func TestScrubEnvironmentDropsHostProxyCredentials(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.internal:8080")
	t.Setenv("HTTPS_PROXY", "http://user:pass@proxy.internal:8080")
	t.Setenv("ALL_PROXY", "socks5://user:pass@proxy.internal:1080")

	env := scrubEnvironment("")
	for _, entry := range env {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		switch key {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
			t.Fatalf("host proxy credential env leaked into sandbox: %s", entry)
		}
	}
}

func TestBuildSeatbeltProfileContainsWritesAndEgress(t *testing.T) {
	profile := buildSeatbeltProfile(NetworkLaunchContext{Mode: "offline"}, "/guest/home", "/work/dir")
	if strings.Contains(profile, "(allow default)") {
		t.Fatal("Seatbelt profile must be default-deny, not allow-all")
	}
	if !strings.Contains(profile, "(deny default)") {
		t.Fatalf("expected default-deny profile, got:\n%s", profile)
	}
	// Writes are the enforced containment: restricted to an explicit allowlist
	// that includes the working directory.
	if !strings.Contains(profile, "(allow file-write*") {
		t.Fatalf("expected an explicit file-write allowlist, got:\n%s", profile)
	}
	if !strings.Contains(profile, `(subpath "/work/dir")`) {
		t.Fatalf("expected workdir write allowlist entry, got:\n%s", profile)
	}
	// Offline mode must not grant open network access.
	if strings.Contains(profile, "(allow network*)") {
		t.Fatalf("offline mode must not allow open network, got:\n%s", profile)
	}
}

func TestProviderSupportsNetworkModeFailsClosedForUnenforcedProviders(t *testing.T) {
	blocked := []struct {
		provider string
		mode     string
	}{
		{"docker", "proxy"},
		{"wsl", "offline"},
		{"wslc", "loopback"},
		{"systemd-nspawn", "proxy"},
	}
	for _, tc := range blocked {
		if providerSupportsNetworkMode(tc.provider, tc.mode) {
			t.Fatalf("%s must not claim support for restricted network mode %s", tc.provider, tc.mode)
		}
	}
	if !providerSupportsNetworkMode("native", "proxy") {
		t.Fatal("native provider should support proxy mode")
	}
	if !providerSupportsNetworkMode("docker", "open") {
		t.Fatal("non-native providers may run with open network mode")
	}
}

func TestPolicyExplicitEmptyDefaultAllowRemovesProviderDefaults(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".nvx-policy.json"), []byte(`{
  "isolation": {"network": {"default_allow": []}}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	policy, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatal(err)
	}
	allow := policy.NetworkAllowlist(Providers["node"])
	for _, host := range allow {
		if host == "registry.npmjs.org:443" || host == "api.osv.dev:443" {
			t.Fatalf("explicit empty default_allow should remove provider defaults, got %v", allow)
		}
	}
}

func TestPolicyExplicitFalseCanOverridePromptUnknown(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".nvx-policy.json"), []byte(`{
  "isolation": {"network": {"prompt_unknown": false}}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	policy, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Isolation.Network.PromptUnknown {
		t.Fatal("explicit prompt_unknown:false should override the default true value")
	}
}

func TestIsNodeVersionInstalledRequiresRuntimeBinary(t *testing.T) {
	nvxHome := tempDir(t)
	version := "v20.0.0"
	versionDir := filepath.Join(nvxHome, "versions", "node", version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		t.Fatal(err)
	}
	if isNodeVersionInstalled(nvxHome, version) {
		t.Fatal("directory without runtime binary must not be treated as an installed Node version")
	}

	binaryName := "node"
	if runtime.GOOS == "windows" {
		binaryName = "node.exe"
	} else {
		versionDir = filepath.Join(versionDir, "bin")
		if err := os.MkdirAll(versionDir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(versionDir, binaryName), []byte(""), 0755); err != nil {
		t.Fatal(err)
	}
	if !isNodeVersionInstalled(nvxHome, version) {
		t.Fatal("version with runtime binary should be treated as installed")
	}
}

func TestAcquireInstallLockPreventsConcurrentInstall(t *testing.T) {
	nvxHome := tempDir(t)
	release, err := acquireInstallLock(nvxHome, "v20.0.0")
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	if _, err := acquireInstallLock(nvxHome, "v20.0.0"); err == nil {
		t.Fatal("second lock should fail while first lock is held")
	}
	release()
	releaseAgain, err := acquireInstallLock(nvxHome, "v20.0.0")
	if err != nil {
		t.Fatalf("lock should be reusable after release: %v", err)
	}
	releaseAgain()
}

func TestAcquireInstallLockRejectsUnsafeVersionName(t *testing.T) {
	if _, err := acquireInstallLock(tempDir(t), `..\outside`); err == nil {
		t.Fatal("install lock should reject path-like version names")
	}
}

func TestGetGlobalDefaultVersionUsesProvidedHome(t *testing.T) {
	nvxHome := tempDir(t)
	target := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(filepath.Join(nvxHome, "current"), target); err != nil {
		t.Fatal(err)
	}
	if got := getGlobalDefaultVersion(nvxHome); got != "v20.0.0" {
		t.Fatalf("getGlobalDefaultVersion(%q) = %q, want v20.0.0", nvxHome, got)
	}
}

func TestNodeUninstallRefusesGlobalDefaultVersion(t *testing.T) {
	nvxHome := tempDir(t)
	target := filepath.Join(nvxHome, "versions", "node", "v20.0.0")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := CreateLink(filepath.Join(nvxHome, "current"), target); err != nil {
		t.Fatal(err)
	}

	err := (NodeProvider{}).Uninstall("20", nvxHome)
	if err == nil {
		t.Fatal("expected uninstall of global default to be refused")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("default version directory should remain after refused uninstall: %v", statErr)
	}
}

// TestAppVersionMatchesNewestChangelogEntry replaces an assertion that appVersion
// equalled a hardcoded "0.3.0". That restated the constant, so it could only fail
// when someone deliberately bumped the version -- and it had to be edited at every
// release, which made it a chore rather than a check. It also held the drift in
// place: version.go claimed 0.3.0 for months while no v0.3.0 tag existed.
//
// The invariant worth enforcing is that the shipped version and the newest
// documented version agree, so bumping one without the other fails here.
func TestAppVersionMatchesNewestChangelogEntry(t *testing.T) {
	data, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}

	// The newest released heading, skipping "## [Unreleased]".
	re := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+[^\]]*)\]`)
	match := re.FindSubmatch(data)
	if match == nil {
		t.Fatal("no versioned '## [x.y.z]' heading found in CHANGELOG.md")
	}
	newest := string(match[1])

	if appVersion != newest {
		t.Fatalf("appVersion = %q but the newest CHANGELOG entry is %q.\n"+
			"Bump both together: a version with no changelog entry ships undocumented, "+
			"and an entry with no bump means users cannot tell what they are running.",
			appVersion, newest)
	}
}

func TestEgressProxyCloseClosesListeners(t *testing.T) {
	proxy, err := startEgressProxy(context.Background(), DefaultPolicy(), Providers["node"], tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	addr := proxy.httpAddr
	proxy.Close()

	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("expected listener %s to be closed", addr)
	}
}

func TestCommandHelpTextExistsForPrimaryCommands(t *testing.T) {
	for _, command := range []string{"use", "policy", "verify-install"} {
		if text := commandHelpText(command); !strings.Contains(text, command) {
			t.Fatalf("expected help text for %s, got %q", command, text)
		}
	}
	if text := commandHelpText("does-not-exist"); text != "" {
		t.Fatalf("unknown command help should be empty, got %q", text)
	}
}

func TestSafeArchiveTargetRejectsEscapes(t *testing.T) {
	dest := tempDir(t)
	for _, name := range []string{
		"node-v20/../evil.txt",
		`node-v20/..\evil.txt`,
		"node-v20/C:/evil.txt",
	} {
		if _, _, err := safeArchiveTarget(dest, name); err == nil {
			t.Fatalf("safeArchiveTarget(%q) should reject escaping path", name)
		}
	}

	target, skip, err := safeArchiveTarget(dest, "node-v20/bin/node")
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("valid archive member should not be skipped")
	}
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
		t.Fatalf("target %q should stay below %q", target, dest)
	}
}

func TestProjectBinShimQuotesCommandNames(t *testing.T) {
	shimDir := tempDir(t)
	cmdName := "bad %PATH% & name"
	if runtime.GOOS != "windows" {
		cmdName = "bad name'; touch nope; #"
	}

	if err := writeProjectBinShim(shimDir, "/tmp/nvx test", cmdName); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(shimDir, cmdName)
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if runtime.GOOS == "windows" {
		if strings.Contains(text, `"bad %PATH% & name"`) || !strings.Contains(text, `"bad %%PATH%% & name"`) {
			t.Fatalf("Windows shim did not escape batch expansion safely:\n%s", text)
		}
	} else if !strings.Contains(text, quotePOSIXShell(cmdName)) {
		t.Fatalf("POSIX shim did not quote command name safely:\n%s", text)
	}
}

func TestTrustedToolGrantPersistsUnderNvxHome(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	scope := projectScopeDir()
	g := loadProjectGrants(nvxHome, scope)
	if g.hasTrustedTool("wrangler") {
		t.Fatal("fresh grants must not already trust wrangler")
	}

	g.TrustedTools = append(g.TrustedTools, "wrangler")
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		t.Fatalf("saveProjectGrants: %v", err)
	}

	// Never written into the project tree.
	if _, err := os.Stat(filepath.Join(projectDir, ".nvx-policy.json")); err == nil {
		t.Fatal("trusted-tool grant must not create a policy file inside the project")
	}

	reloaded := loadProjectGrants(nvxHome, scope)
	if !reloaded.hasTrustedTool("wrangler") {
		t.Fatal("expected wrangler to be a persisted trusted tool after reload")
	}
	if reloaded.hasTrustedTool("Wrangler") == false {
		t.Fatal("hasTrustedTool must be case-insensitive")
	}
	if reloaded.hasTrustedTool("gh") {
		t.Fatal("unrelated tool must not be trusted")
	}
}

// A trailing "@" is a lost version, not a request for the latest.
//
// parseRuntimeSpec turned "node@" into "latest", so `nvx install node@`
// downloaded whatever the newest release happened to be -- measured as v26.8.1,
// a major nobody asked for, from what is far more likely a truncated line.
// `nvx install node` already means latest and reads like it.
func TestATrailingAtSignYieldsNoVersion(t *testing.T) {
	for _, arg := range []string{"node@", "bun@", "node@   "} {
		provider, version := parseRuntimeSpec(arg)
		if strings.TrimSpace(version) != "" {
			t.Errorf("parseRuntimeSpec(%q) = (%s, %q); a bare '@' must not resolve to a version, "+
				"or a truncated command line installs a major at random", arg, provider.Name(), version)
		}
	}
	// Naming the runtime with no "@" still means latest -- that is the spelling
	// that says so.
	for _, arg := range []string{"node", "bun"} {
		if _, version := parseRuntimeSpec(arg); version != "latest" {
			t.Errorf("parseRuntimeSpec(%q) should still mean latest, got %q", arg, version)
		}
	}
}
