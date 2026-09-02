package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEnvScriptFrontsShimDir(t *testing.T) {
	bash := envScript("bash", "/opt/nvx", "/home/u/.nvx/bin")
	if !strings.Contains(bash, "export PATH=") || !strings.Contains(bash, "/home/u/.nvx/bin") {
		t.Fatalf("bash env script must front the shim dir:\n%s", bash)
	}
	// The nvx shell function must still be emitted.
	if !strings.Contains(bash, "nvx() {") {
		t.Fatalf("bash env script must still define the nvx function")
	}

	// The shim dir is asserted through what the script decodes to: it is emitted
	// base64 so a non-UTF-8 console codepage cannot corrupt it on the way into the
	// shell. See TestPowerShellIntegrationSurvivesANonASCIIPath.
	ps := envScript("powershell", `C:\opt\nvx.exe`, `C:\Users\u\.nvx\bin`)
	if !strings.Contains(ps, "$env:PATH") || !decodedPathsContain(ps, `.nvx\bin`) {
		t.Fatalf("powershell env script must front the shim dir:\n%s", ps)
	}
	if !strings.Contains(ps, "function nvx {") {
		t.Fatalf("powershell env script must still define the nvx function")
	}
}

func TestIsLTS(t *testing.T) {
	tests := []struct {
		lts      interface{}
		expected bool
	}{
		{false, false},
		{true, true},
		{"Hydrogen", true},
		{nil, false},
	}

	for _, tc := range tests {
		r := Release{Lts: tc.lts}
		if r.IsLTS() != tc.expected {
			t.Errorf("expected IsLTS() for %v to be %v", tc.lts, tc.expected)
		}
	}
}

func TestResolveVersion(t *testing.T) {
	releases := []Release{
		{Version: "v20.11.0", Lts: "Iron"},
		{Version: "v20.10.0", Lts: "Iron"},
		{Version: "v19.0.0", Lts: false},
		{Version: "v18.16.1", Lts: "Hydrogen"},
		{Version: "v18.16.0", Lts: "Hydrogen"},
		{Version: "v16.4.0", Lts: "Gallium"},
	}

	tests := []struct {
		query    string
		expected string
		errStr   string
	}{
		{"latest", "v20.11.0", ""},
		{"lts", "v20.11.0", ""},
		{"v18.16.0", "v18.16.0", ""},
		{"18.16.0", "v18.16.0", ""},
		{"18.16", "v18.16.1", ""},
		{"18", "v18.16.1", ""},
		{"Hydrogen", "v18.16.1", ""},
		{"unknown", "", "no release found matching query"},
	}

	for _, tc := range tests {
		res, err := ResolveVersion(tc.query, releases)
		if tc.errStr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errStr) {
				t.Errorf("query %s: expected error containing %q, got %v", tc.query, tc.errStr, err)
			}
		} else {
			if err != nil {
				t.Errorf("query %s: unexpected error: %v", tc.query, err)
			} else if res.Version != tc.expected {
				t.Errorf("query %s: expected %s, got %s", tc.query, tc.expected, res.Version)
			}
		}
	}
}

func TestCleanEngineRange(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"^18.16.0", "18.16.0"},
		{">=16.0.0 <18.0.0", "16.0.0"},
		{"18.x", "18"},
		{"~20.11.0", "20.11.0"},
		{"=18.0.0", "18.0.0"},
		{"16.4.*", "16.4"},
		{"14.x || 16.x", "14"},
	}

	for _, tc := range tests {
		res := CleanEngineRange(tc.input)
		if res != tc.expected {
			t.Errorf("CleanEngineRange(%q) = %q, expected %q", tc.input, res, tc.expected)
		}
	}
}

func TestCleanAndBuildPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses Windows-style PATH entries")
	}
	nvwHome := `C:\Users\User\.nvw`
	versionsDir := filepath.Join(nvwHome, "versions")
	targetVersionDir := filepath.Join(versionsDir, "v18.16.0")

	currentPathList := []string{
		`C:\Windows\System32`,
		filepath.Join(versionsDir, "v20.11.0"),
		`C:\Program Files\Git\bin`,
		filepath.Join(nvwHome, "current"), // Test cleaning default fallback
	}
	currentPath := strings.Join(currentPathList, string(filepath.ListSeparator))

	res := CleanAndBuildPath(currentPath, nvwHome, targetVersionDir, "")
	parts := filepath.SplitList(res)

	// In nvx, target binary directory and its npm global bin directory should be prepended after shimDir
	expectedNpmDir := GetNpmGlobalBinDir(targetVersionDir)
	expectedBinDir := GetVersionBinDir(targetVersionDir)
	expectedShimDir := filepath.Join(nvwHome, "bin")
	if len(parts) < 3 || parts[0] != expectedShimDir || parts[1] != expectedNpmDir || parts[2] != expectedBinDir {
		t.Errorf("expected first path entries to be %s, %s and %s, got %s", expectedShimDir, expectedNpmDir, expectedBinDir, res)
	}

	for _, part := range parts {
		if strings.Contains(part, "v20.11.0") {
			t.Errorf("expected v20.11.0 to be removed from path: %s", res)
		}
		if strings.Contains(part, "current") {
			t.Errorf("expected current fallback to be removed when version is active: %s", res)
		}
	}
}

func TestParsePackageQuery(t *testing.T) {
	tests := []struct {
		input       string
		expectedPkg string
		expectedVer string
	}{
		{"lodash", "lodash", ""},
		{"express@4.18.2", "express", "4.18.2"},
		{"@types/node", "@types/node", ""},
		{"@types/node@18.0.0", "@types/node", "18.0.0"},
	}

	for _, tc := range tests {
		pkg, ver := parsePackageQuery(tc.input)
		if pkg != tc.expectedPkg || ver != tc.expectedVer {
			t.Errorf("parsePackageQuery(%q) = (%q, %q), expected (%q, %q)", tc.input, pkg, ver, tc.expectedPkg, tc.expectedVer)
		}
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		s        string
		t        string
		expected int
	}{
		{"lodash", "lodash", 0},
		{"lodas", "lodash", 1},
		{"express", "expres", 1},
		{"react", "riact", 1},
		{"react", "vue", 5},
	}

	for _, tc := range tests {
		dist := LevenshteinDistance(tc.s, tc.t)
		if dist != tc.expected {
			t.Errorf("LevenshteinDistance(%q, %q) = %d, expected %d", tc.s, tc.t, dist, tc.expected)
		}
	}
}

func TestCheckTyposquatting(t *testing.T) {
	// Without this stub the check makes two live HTTPS requests per near-match
	// name. That made the suite ~5x slower, sent traffic to api.npmjs.org from
	// every machine running `go test`, and -- worse -- silently chose a different
	// branch depending on whether the network happened to work.
	stubWeeklyDownloads(t, map[string]int{
		"lodash": 60_000_000, "express": 30_000_000,
		"lodas": 12, "expres": 30,
	})

	mockPopular := []string{"lodash", "express"}
	tests := []struct {
		input    string
		expected string
	}{
		{"lodash", ""},
		{"lodas", "lodash"},
		{"expres", "express"},
		{"something-unrelated-and-long", ""},
	}

	for _, tc := range tests {
		res := CheckTyposquatting(tc.input, mockPopular)
		if res != tc.expected {
			t.Errorf("CheckTyposquatting(%q) = %q, expected %q", tc.input, res, tc.expected)
		}
	}
}

func TestPolicyBlocked(t *testing.T) {
	p := Policy{
		BlockedPackages: []string{"bad-package", "danger-*", ""},
	}

	tests := []struct {
		input    string
		expected bool
	}{
		{"bad-package", true},
		{"good-package", false},
		{"danger-zone", true},
		{"danger-ous", true},
		{"safe-danger-zone", false},
	}

	for _, tc := range tests {
		res := p.IsBlocked(tc.input)
		if res != tc.expected {
			t.Errorf("Policy.IsBlocked(%q) = %v, expected %v", tc.input, res, tc.expected)
		}
	}
}

func TestGenerateSandboxID(t *testing.T) {
	id1, err := generateSandboxID()
	if err != nil {
		t.Fatalf("generateSandboxID() failed: %v", err)
	}
	if len(id1) != 16 { // 8 bytes = 16 hex characters
		t.Errorf("expected sandbox ID of length 16, got %d: %q", len(id1), id1)
	}

	// Ensure uniqueness across calls
	id2, err := generateSandboxID()
	if err != nil {
		t.Fatalf("generateSandboxID() failed on second call: %v", err)
	}
	if id1 == id2 {
		t.Errorf("expected unique sandbox IDs, both were %q", id1)
	}
}

func TestScrubEnvironment(t *testing.T) {
	// Save original env and set test values
	origEnv := make([]string, len(os.Environ()))
	copy(origEnv, os.Environ())

	// Set some sensitive variables
	os.Setenv("AWS_SECRET_ACCESS_KEY", "supersecret")
	os.Setenv("GITHUB_TOKEN", "ghp_fake")
	os.Setenv("SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")
	os.Setenv("NPM_TOKEN", "npm_fake")
	defer func() {
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		os.Unsetenv("GITHUB_TOKEN")
		os.Unsetenv("SSH_AUTH_SOCK")
		os.Unsetenv("NPM_TOKEN")
	}()

	guestHome := filepath.Join(os.TempDir(), "nvx-test-guest")
	defer os.RemoveAll(guestHome)

	env := scrubEnvironment(guestHome)

	// Check that sensitive variables are NOT present
	sensitiveKeys := []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "NPM_TOKEN"}
	for _, key := range sensitiveKeys {
		for _, envVar := range env {
			parts := strings.SplitN(envVar, "=", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], key) {
				t.Errorf("sensitive variable %q should have been scrubbed but was found in env", key)
			}
		}
	}

	// Check that NVX_SANDBOX=1 is present
	found := false
	for _, envVar := range env {
		if envVar == "NVX_SANDBOX=1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected NVX_SANDBOX=1 to be set in sandbox environment")
	}

	// Check that PATH is present (it's always allowed)
	pathFound := false
	for _, envVar := range env {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "PATH") {
			pathFound = true
			break
		}
	}
	if !pathFound {
		t.Error("expected PATH to be present in scrubbed environment")
	}
}

func TestCreateAndCleanupGuestProfile(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "nvx-test-home")
	defer os.RemoveAll(tmpDir)

	sandboxID := "test-session-abc123"

	guestHome, err := createGuestProfile(tmpDir, sandboxID)
	if err != nil {
		t.Fatalf("createGuestProfile() failed: %v", err)
	}

	// Verify the guest home directory was created
	info, err := os.Stat(guestHome)
	if err != nil {
		t.Fatalf("guest home directory not found: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected guest home to be a directory")
	}

	// Verify subdirectories were created
	for _, subdir := range []string{"tmp", ".config", ".cache"} {
		subdirPath := filepath.Join(guestHome, subdir)
		if _, err := os.Stat(subdirPath); os.IsNotExist(err) {
			t.Errorf("expected subdirectory %q to exist in guest home", subdir)
		}
	}

	// Cleanup and verify removal
	cleanupGuestProfile(tmpDir, sandboxID)
	if _, err := os.Stat(guestHome); !os.IsNotExist(err) {
		t.Error("expected guest home to be removed after cleanup")
	}
}

func TestGetSandboxHomeDir(t *testing.T) {
	home := getSandboxHomeDir("/home/user/.nvx")
	expected := filepath.Join("/home/user/.nvx", "sandbox_home")
	if home != expected {
		t.Errorf("getSandboxHomeDir() = %q, expected %q", home, expected)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"v18.16.0", "v18.16.1", -1},
		{"v20.0.0", "v18.1.0", 1},
		{"v16.14.0", "16.14", 0},
		{"v16.14.2", "v16.14.2", 0},
		{"v20.11.0", "v20.9.0", 1},
		{"10.0.0", "12.0.0", -1},
		{"v18", "v18.0.0", 0},
	}

	for _, tc := range tests {
		res := CompareVersions(tc.v1, tc.v2)
		if res != tc.expected {
			t.Errorf("CompareVersions(%q, %q) = %d, expected %d", tc.v1, tc.v2, res, tc.expected)
		}
	}
}

func TestEscapeScopedPackage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"@types/node", "@types%2Fnode"},
		{"lodash", "lodash"},
		{"@babel/core", "@babel%2Fcore"},
		{"react", "react"},
	}

	for _, tc := range tests {
		res := EscapeScopedPackage(tc.input)
		if res != tc.expected {
			t.Errorf("EscapeScopedPackage(%q) = %q, expected %q", tc.input, res, tc.expected)
		}
	}
}

func TestMergePolicies(t *testing.T) {
	global := Policy{
		BlockedPackages:      []string{"pkg-a", "pkg-b"},
		EnforceIgnoreScripts: false,
		Typosquatting: TyposquattingPolicy{
			Enabled:         true,
			MaxDistance:     2,
			TrustedPackages: []string{"trust-a"},
		},
		Isolation: IsolationPolicy{
			Enabled: false,
			Filesystem: FilesystemPolicy{
				Provider: "native",
			},
		},
	}

	local := Policy{
		BlockedPackages:      []string{"pkg-c", "pkg-a"}, // pkg-a is duplicate
		EnforceIgnoreScripts: true,
		Typosquatting: TyposquattingPolicy{
			Enabled:         false, // explicit disable overrides global
			MaxDistance:     3,
			TrustedPackages: []string{"trust-b", "trust-a"},
		},
		Isolation: IsolationPolicy{
			Enabled: true,
			Filesystem: FilesystemPolicy{
				Provider: "custom",
			},
		},
	}

	merged := MergePolicies(global, local)

	// Check blocked packages union (order-independent count check)
	if len(merged.BlockedPackages) != 3 {
		t.Errorf("expected 3 blocked packages, got %d: %v", len(merged.BlockedPackages), merged.BlockedPackages)
	}

	// Check ignore scripts OR
	if !merged.EnforceIgnoreScripts {
		t.Error("expected EnforceIgnoreScripts to be true")
	}

	// Check typosquatting disable overrides
	if merged.Typosquatting.Enabled {
		t.Error("expected Typosquatting.Enabled to be false")
	}

	// Check max distance overrides
	if merged.Typosquatting.MaxDistance != 3 {
		t.Errorf("expected MaxDistance to be 3, got %d", merged.Typosquatting.MaxDistance)
	}

	// Check trusted packages union
	if len(merged.Typosquatting.TrustedPackages) != 2 {
		t.Errorf("expected 2 trusted packages, got %d: %v", len(merged.Typosquatting.TrustedPackages), merged.Typosquatting.TrustedPackages)
	}

	// Check isolation overrides
	if !merged.Isolation.Enabled {
		t.Error("expected Isolation.Enabled to be true")
	}
	if merged.Isolation.Filesystem.Provider != "custom" {
		t.Errorf("expected Isolation.Filesystem.Provider to be 'custom', got %q", merged.Isolation.Filesystem.Provider)
	}
}

func TestCleanAndBuildPath_EdgeCases(t *testing.T) {
	nvHome := `/home/user/.nvx`
	targetVer := `/home/user/.nvx/versions/node/v18.16.0`

	// Test with empty path
	res := CleanAndBuildPath("", nvHome, targetVer, "")
	parts := filepath.SplitList(res)
	if len(parts) < 2 {
		t.Fatalf("expected at least 2 components in path, got %d", len(parts))
	}
	expectedShim := filepath.Join(nvHome, "bin")
	expectedNpm := GetNpmGlobalBinDir(targetVer)
	expectedBin := GetVersionBinDir(targetVer)
	if len(parts) < 3 || parts[0] != expectedShim || parts[1] != expectedNpm || parts[2] != expectedBin {
		t.Errorf("expected prepended paths, got: %v", parts)
	}

	// Test path containing duplicates and already-cleansed versions
	duplicatedPath := strings.Join([]string{
		`/usr/bin`,
		`/home/user/.nvx/versions/node/v20.0.0`,
		`/usr/bin`,
	}, string(filepath.ListSeparator))

	res2 := CleanAndBuildPath(duplicatedPath, nvHome, targetVer, "")
	parts2 := filepath.SplitList(res2)
	for _, p := range parts2 {
		if strings.Contains(p, "v20.0.0") {
			t.Errorf("expected v20.0.0 to be removed: %s", res2)
		}
	}
}

func TestParsePackageQuery_EdgeCases(t *testing.T) {
	tests := []struct {
		input       string
		expectedPkg string
		expectedVer string
	}{
		{"", "", ""},
		{"   ", "", ""},
		{"lodash@4.17.21-beta.1", "lodash", "4.17.21-beta.1"},
		{"@scoped/package@1.0.0", "@scoped/package", "1.0.0"},
		{"@scoped/package", "@scoped/package", ""},
		{"some-pkg@", "some-pkg", ""},
	}

	for _, tc := range tests {
		pkg, ver := parsePackageQuery(tc.input)
		if pkg != tc.expectedPkg || ver != tc.expectedVer {
			t.Errorf("parsePackageQuery(%q) = (%q, %q), expected (%q, %q)", tc.input, pkg, ver, tc.expectedPkg, tc.expectedVer)
		}
	}
}

// TestCleanupStaleSandboxes covers the half of cleanup that must still happen:
// a genuinely abandoned guest home is removed.
//
// It used to create a directory with no owner marker and a current timestamp,
// then assert it was deleted — which is indistinguishable from a session that
// started a millisecond ago, and is exactly the shape F35 destroyed in
// concurrent use. The session here is abandoned in the way a crashed one is:
// old, with nothing claiming it. See sandbox_session_owner_test.go for the
// in-use cases this deliberately no longer covers.
func TestCleanupStaleSandboxes(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "nvx-test-cleanup")
	defer os.RemoveAll(tmpDir)

	sandboxDir := getSandboxHomeDir(tmpDir)
	fakeSandboxPath := filepath.Join(sandboxDir, "stale-session-123")

	err := os.MkdirAll(fakeSandboxPath, 0755)
	if err != nil {
		t.Fatalf("failed to create fake stale sandbox path: %v", err)
	}
	abandoned := time.Now().Add(-unownedGuestHomeGrace - time.Hour)
	if err := os.Chtimes(fakeSandboxPath, abandoned, abandoned); err != nil {
		t.Fatalf("failed to back-date the stale sandbox: %v", err)
	}

	// Verify it exists
	if _, err := os.Stat(fakeSandboxPath); os.IsNotExist(err) {
		t.Fatal("expected fake sandbox path to exist")
	}

	// Run cleanup
	cleanupStaleSandboxes(tmpDir, 0)

	// Verify it is gone
	if _, err := os.Stat(fakeSandboxPath); !os.IsNotExist(err) {
		t.Error("expected fake sandbox path to be deleted by cleanupStaleSandboxes")
	}
}

func TestParseShellArg(t *testing.T) {
	def := defaultShell()
	tests := []struct {
		args     []string
		expected string
	}{
		{[]string{"--shell=bash"}, "bash"},
		{[]string{"--shell", "zsh"}, "zsh"},
		{[]string{"bash"}, "bash"},
		{[]string{"20", "--shell=zsh"}, "zsh"},
		{[]string{"20", "powershell"}, "powershell"},
		{[]string{"20"}, def},
		{nil, def},
		{[]string{"--shell="}, def},
	}

	for _, tc := range tests {
		res := parseShellArg(tc.args)
		if res != tc.expected {
			t.Errorf("parseShellArg(%v) = %q, expected %q", tc.args, res, tc.expected)
		}
	}
}

func TestDetectInstallPackages(t *testing.T) {
	tests := []struct {
		args     []string
		expected []string
	}{
		{[]string{"install", "lodash"}, []string{"lodash"}},
		{[]string{"i", "lodash", "express"}, []string{"lodash", "express"}},
		{[]string{"add", "react"}, []string{"react"}},
		// Leading flags must not bypass detection
		{[]string{"--loglevel=error", "install", "evil-pkg"}, []string{"evil-pkg"}},
		{[]string{"-g", "install", "evil-pkg"}, []string{"evil-pkg"}},
		// A flag this package has no specific knowledge of, taking a value,
		// must not hide the subcommand behind its own value.
		{[]string{"--loglevel", "verbose", "install", "evil-pkg"}, []string{"evil-pkg"}},
		// npm typo aliases
		{[]string{"isntall", "lodash"}, []string{"lodash"}},
		{[]string{"in", "lodash"}, []string{"lodash"}},
		// Non-install commands
		{[]string{"run", "build"}, nil},
		{[]string{"test"}, nil},
		{[]string{}, nil},
		// Install with only flags after (e.g. plain `npm install`)
		{[]string{"install", "--save-dev"}, nil},
	}

	for _, tc := range tests {
		res := detectInstallPackages(tc.args)
		if len(res) != len(tc.expected) {
			t.Errorf("detectInstallPackages(%v) = %v, expected %v", tc.args, res, tc.expected)
			continue
		}
		for i := range res {
			if res[i] != tc.expected[i] {
				t.Errorf("detectInstallPackages(%v) = %v, expected %v", tc.args, res, tc.expected)
				break
			}
		}
	}
}

func TestCleanAndBuildPathProjectTools(t *testing.T) {
	nvHome := filepath.Join("home", "user", ".nvx")
	targetVer := filepath.Join(nvHome, "versions", "node", "v20.0.0")
	projectPrefix := filepath.Join("projects", "app", ".nvx", "npm_global")
	staleProjectBin := filepath.Join("projects", "old-app", ".nvx", "npm_global", "bin")

	currentPath := strings.Join([]string{
		filepath.Join("usr", "bin"),
		staleProjectBin,
	}, string(filepath.ListSeparator))

	res := CleanAndBuildPath(currentPath, nvHome, targetVer, projectPrefix)
	parts := filepath.SplitList(res)

	expectedNpmBin := GetNpmPrefixBinDir(projectPrefix)
	if len(parts) < 3 || parts[1] != expectedNpmBin {
		t.Errorf("expected project npm prefix bin %q at index 1, got parts: %v", expectedNpmBin, parts)
	}

	for _, p := range parts {
		if strings.Contains(p, "old-app") {
			t.Errorf("expected stale project tools dir to be removed from PATH: %s", res)
		}
	}
}

func TestLoadPolicyProjectTools(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nvx-envpolicy-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(origWd) }()

	nvxHome := filepath.Join(tmpDir, ".nvx")
	projectDir := filepath.Join(tmpDir, "project")
	subDir := filepath.Join(projectDir, "sub")
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	policyJSON := `{"environment": {"isolated_tools": true}}`
	if err := os.WriteFile(filepath.Join(projectDir, ".nvx-policy.json"), []byte(policyJSON), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(subDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatalf("LoadPolicy failed: %v", err)
	}

	if !loaded.Environment.IsolatedTools {
		t.Error("expected Environment.IsolatedTools to be true")
	}
	// Resolve symlinks to tolerate macOS /var -> /private/var temp paths
	gotDir, _ := filepath.EvalSymlinks(loaded.ProjectDir)
	wantDir, _ := filepath.EvalSymlinks(projectDir)
	if gotDir != wantDir {
		t.Errorf("expected ProjectDir %q, got %q", wantDir, gotDir)
	}
}

func TestLoadPolicyNearestWins(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nvx-precedence-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(origWd) }()

	nvxHome := filepath.Join(tmpDir, ".nvx")
	parentDir := filepath.Join(tmpDir, "project")
	childDir := filepath.Join(parentDir, "sub")
	if err := os.MkdirAll(nvxHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Parent sets max_distance 3, child overrides with 4: the policy nearest
	// to the working directory must win.
	//
	// Both raise it above the default 2, and the direction is the point. Lowering
	// max_distance finds FEWER typosquats, so it is a loosening and now needs the
	// trust prompt like any other -- this test used to go 3 -> 1 and was measuring
	// cascade precedence through a value the trust gate refuses from an untrusted
	// project file. Raising keeps the cascade under test and the gate out of it.
	parentPolicy := `{"typosquatting": {"enabled": true, "max_distance": 3}}`
	childPolicy := `{"typosquatting": {"enabled": true, "max_distance": 4}}`
	if err := os.WriteFile(filepath.Join(parentDir, ".nvx-policy.json"), []byte(parentPolicy), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, ".nvx-policy.json"), []byte(childPolicy), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(childDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatalf("LoadPolicy failed: %v", err)
	}

	if loaded.Typosquatting.MaxDistance != 4 {
		t.Errorf("expected child policy MaxDistance 4 to win over parent, got %d", loaded.Typosquatting.MaxDistance)
	}
}

func TestExtractQuotedStrings(t *testing.T) {
	src := `export const top = [
  'lodash',
  'react-dom',
  "@types/node",
  'UPPER_INVALID',
  '.hidden',
]
`
	list := extractQuotedStrings(src, 100)
	expected := []string{"lodash", "react-dom", "@types/node"}
	if len(list) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, list)
	}
	for i := range expected {
		if list[i] != expected[i] {
			t.Errorf("expected %v, got %v", expected, list)
			break
		}
	}

	// Limit is respected
	limited := extractQuotedStrings(src, 2)
	if len(limited) != 2 {
		t.Errorf("expected limit of 2 entries, got %d", len(limited))
	}
}

func TestBuildSeatbeltProfile(t *testing.T) {
	netCtx := NetworkLaunchContext{Mode: "proxy", HTTPProxyPort: 8080}
	profile := buildSeatbeltProfile(netCtx, "/guest/home", "/work/dir")
	for _, expected := range []string{
		"(version 1)",
		"(deny default)",
		"(allow file-read*",
		"(allow file-write*",
		`(subpath "/guest/home")`,
		`(subpath "/work/dir")`,
		`(subpath "/private/tmp")`,
		`(allow network-outbound (remote tcp "localhost:8080"))`,
	} {
		if !strings.Contains(profile, expected) {
			t.Errorf("expected profile to contain %q, got:\n%s", expected, profile)
		}
	}
}

func TestResolveWslcNodeImage(t *testing.T) {
	nvxHome := filepath.Join("home", "user", ".nvx")

	tests := []struct {
		pinned   string
		expected string
	}{
		{"v20.11.0", "node:20.11.0"},
		{"20", "node:20"},
		{"", "node:latest"},
	}

	for _, tc := range tests {
		res := resolveWslcNodeImage(nvxHome, tc.pinned)
		if res != tc.expected {
			t.Errorf("resolveWslcNodeImage(pinned=%q) = %q, expected %q", tc.pinned, res, tc.expected)
		}
	}
}

func TestContainerSafeEnv(t *testing.T) {
	env := containerSafeEnv()
	if len(env) == 0 {
		t.Fatal("expected non-empty container env allowlist")
	}
	found := false
	for _, e := range env {
		if e == "NVX_SANDBOX=1" {
			found = true
		}
		if strings.HasPrefix(strings.ToUpper(e), "AWS_") || strings.HasPrefix(strings.ToUpper(e), "GITHUB_") {
			t.Errorf("container env must not include host secrets, got %q", e)
		}
	}
	if !found {
		t.Error("expected NVX_SANDBOX=1 in container env")
	}
}

func TestLoadPolicyCascading(t *testing.T) {
	// Create temporary workspace
	tmpDir, err := os.MkdirTemp("", "nvx-policy-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working dir: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	// Define folder structure:
	// tmpDir/ (global config home)
	// tmpDir/project/ (parent workspace)
	// tmpDir/project/sub/ (child directory)
	nvxHome := filepath.Join(tmpDir, ".nvx")
	parentDir := filepath.Join(tmpDir, "project")
	childDir := filepath.Join(parentDir, "sub")

	err = os.MkdirAll(nvxHome, 0755)
	if err != nil {
		t.Fatalf("failed to create nvxHome: %v", err)
	}
	err = os.MkdirAll(childDir, 0755)
	if err != nil {
		t.Fatalf("failed to create childDir: %v", err)
	}

	// Write global policy: block "global-blocked"
	globalPolicy := Policy{
		BlockedPackages: []string{"global-blocked"},
		Typosquatting: TyposquattingPolicy{
			Enabled: true,
		},
	}
	gData, _ := json.Marshal(globalPolicy)
	_ = os.WriteFile(filepath.Join(nvxHome, "policy.json"), gData, 0644)

	// Write parent policy: block "parent-blocked" and add trusted package "trusted-parent".
	// Adding a trusted package loosens protection, so this file must be trusted below.
	parentPath := filepath.Join(parentDir, ".nvx-policy.json")
	_ = os.WriteFile(parentPath, []byte(`{"blocked_packages":["parent-blocked"],"typosquatting":{"trusted_packages":["trusted-parent"]}}`), 0644)

	// Write child policy: block "child-blocked" (tightening only, no trust needed)
	_ = os.WriteFile(filepath.Join(childDir, "policy.json"), []byte(`{"blocked_packages":["child-blocked"]}`), 0644)

	// Change working directory to childDir
	err = os.Chdir(childDir)
	if err != nil {
		t.Fatalf("failed to change wd to childDir: %v", err)
	}

	// Trust the parent policy's current contents (as an accepted prompt would).
	// Pin the exact path LoadPolicy will discover from the resolved cwd, via the
	// same collectProjectPolicyPaths helper, so the key matches byte-for-byte on
	// every platform (macOS /var symlink, Windows 8.3 TEMP paths, etc.).
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

	// Load policy using nvxHome
	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatalf("LoadPolicy failed: %v", err)
	}

	// Check blocked packages (union of global, parent, and child)
	expectedBlocked := map[string]bool{
		"global-blocked": true,
		"parent-blocked": true,
		"child-blocked":  true,
	}
	for _, p := range loaded.BlockedPackages {
		if !expectedBlocked[p] {
			t.Errorf("unexpected blocked package found: %q", p)
		}
		delete(expectedBlocked, p)
	}
	if len(expectedBlocked) > 0 {
		t.Errorf("missing expected blocked packages: %v", expectedBlocked)
	}

	// Check trusted packages (union of global and local)
	foundTrusted := false
	for _, tp := range loaded.Typosquatting.TrustedPackages {
		if tp == "trusted-parent" {
			foundTrusted = true
		}
	}
	if !foundTrusted {
		t.Error("expected trusted-parent to be in trusted packages list")
	}
}

func TestParseStartupFlagsQuietAndAgentMode(t *testing.T) {
	quietFlag = false
	agentModeFlag = false
	args := []string{"nvx", "-q", "--agent-mode", "install", "20"}
	filtered, yes, _, _, _ := parseStartupFlags(args)

	if !quietFlag {
		t.Error("expected quietFlag to be true when -q is passed")
	}
	if !agentModeFlag {
		t.Error("expected agentModeFlag to be true when --agent-mode is passed")
	}
	if !yes {
		t.Error("expected yes to be true when --agent-mode is passed")
	}
	if len(filtered) != 3 || filtered[1] != "install" || filtered[2] != "20" {
		t.Errorf("unexpected filtered args: %v", filtered)
	}
}

func TestIsTrustedPackageWildcard(t *testing.T) {
	p := Policy{
		Typosquatting: TyposquattingPolicy{
			TrustedPackages: []string{"@myorg/*", "internal-*", "exact-pkg"},
		},
	}

	tests := []struct {
		pkg      string
		expected bool
	}{
		{"@myorg/component", true},
		{"@myorg/helpers", true},
		{"internal-tool", true},
		{"exact-pkg", true},
		{"other-pkg", false},
		{"@otherorg/component", false},
	}

	for _, tc := range tests {
		if got := p.IsTrustedPackage(tc.pkg); got != tc.expected {
			t.Errorf("IsTrustedPackage(%q) = %v; expected %v", tc.pkg, got, tc.expected)
		}
	}
}

// The uncontained ("your own code") path must announce its status exactly
// once per user-facing command, not once per process: a build script's own
// npm/node invocations are each a separate nvx.exe process, so only the
// outermost one should report — a per-process guard (e.g. sync.Once) would
// reprint on every nested invocation instead, one per level of the script tree.
func TestIsTopLevelShimInvocation(t *testing.T) {
	t.Setenv(nvxActiveEnvVar, "")
	if !isTopLevelShimInvocation() {
		t.Error("expected the first call in a fresh process tree to be top-level")
	}
	if isTopLevelShimInvocation() {
		t.Error("expected a second call in the same process to report nested, now that the tree is marked active")
	}

	// Simulates a nested nvx.exe process: the env var is already set because a
	// parent invocation (e.g. the outer `npm publish`) set it before spawning
	// this child, exactly as a real child process inherits it.
	t.Setenv(nvxActiveEnvVar, "1")
	if isTopLevelShimInvocation() {
		t.Error("expected a process that inherited the active marker to report nested")
	}
}

// TestEscapeScopedPackageNeutralisesHostileNames covers what the hand-rolled
// escaping let through. A package name is not always typed by the user -- it can
// come from a project policy file or a package.json in a cloned repo -- and it is
// interpolated into a registry URL path whose response feeds the typosquat and
// release-age gates.
func TestEscapeScopedPackageNeutralisesHostileNames(t *testing.T) {
	cases := []struct{ name, input string }{
		{"path traversal", "../../../etc/passwd"},
		{"traversal after a scope", "@scope/../../evil"},
		{"query truncation", "lodash?fake=1"},
		{"fragment truncation", "lodash#x"},
		{"embedded space", "lo dash"},
		{"double slash", "//evil.com/x"},
	}
	for _, tc := range cases {
		got := EscapeScopedPackage(tc.input)
		if strings.ContainsAny(got, "/? #") {
			t.Errorf("%s: EscapeScopedPackage(%q) = %q still carries a URL-significant character", tc.name, tc.input, got)
		}
	}
}

// stubWeeklyDownloads replaces the registry lookup for one test.
func stubWeeklyDownloads(t *testing.T, counts map[string]int) {
	t.Helper()
	prev := weeklyDownloads
	weeklyDownloads = func(pkg string) (int, error) {
		n, ok := counts[pkg]
		if !ok {
			return 0, fmt.Errorf("no stubbed download count for %q", pkg)
		}
		return n, nil
	}
	t.Cleanup(func() { weeklyDownloads = prev })
}

// TestCheckTyposquattingAuthorityThresholds covers the download comparison itself,
// which previously ran only when a live request happened to succeed and was never
// asserted deliberately: a package is a typosquat only when the lookalike is both
// popular in absolute terms (>50k/week) and vastly more downloaded (>100x).
func TestCheckTyposquattingAuthorityThresholds(t *testing.T) {
	cases := []struct {
		name    string
		counts  map[string]int
		query   string
		wantHit string
	}{
		{
			name:    "obscure name against a hugely popular lookalike is flagged",
			counts:  map[string]int{"lodash": 60_000_000, "lodas": 10},
			query:   "lodas",
			wantHit: "lodash",
		},
		{
			name:    "a lookalike that is not popular enough is not flagged",
			counts:  map[string]int{"lodash": 40_000, "lodas": 1},
			query:   "lodas",
			wantHit: "",
		},
		{
			name:    "a similarly-downloaded package is a peer, not a squat",
			counts:  map[string]int{"lodash": 60_000_000, "lodas": 5_000_000},
			query:   "lodas",
			wantHit: "",
		},
		{
			name:    "exactly at 100x is not over the threshold",
			counts:  map[string]int{"lodash": 10_000_000, "lodas": 100_000},
			query:   "lodas",
			wantHit: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubWeeklyDownloads(t, tc.counts)
			if got := CheckTyposquatting(tc.query, []string{"lodash"}); got != tc.wantHit {
				t.Errorf("CheckTyposquatting(%q) = %q, want %q", tc.query, got, tc.wantHit)
			}
		})
	}
}

// TestCheckTyposquattingFallsBackWhenLookupFails pins the offline behaviour: if the
// registry cannot be reached, a near-match is flagged on name similarity alone
// rather than being waved through.
func TestCheckTyposquattingFallsBackWhenLookupFails(t *testing.T) {
	prev := weeklyDownloads
	weeklyDownloads = func(string) (int, error) { return 0, fmt.Errorf("network unreachable") }
	t.Cleanup(func() { weeklyDownloads = prev })

	if got := CheckTyposquatting("lodas", []string{"lodash"}); got != "lodash" {
		t.Errorf("with the registry unreachable, a near-match must still be flagged; got %q", got)
	}
}

// TestDefaultShellDetectsGitBashOnWindows covers a silent no-op an independent
// acceptance pass found: `nvx use 20` in Git Bash emitted PowerShell, which bash
// cannot evaluate, so nothing applied — while nvx printed "Now using Node.js v20"
// anyway. Auto-switch on `cd`, an MVP bullet, never fired there either.
func TestDefaultShellDetectsGitBashOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("shell detection only branches on Windows")
	}
	cases := []struct {
		name    string
		msystem string
		shell   string
		want    string
	}{
		{"git bash", "MINGW64", "/usr/bin/bash", "bash"},
		{"msys2 with no SHELL", "MSYS", "", "bash"},
		{"zsh under an emulation", "", "/usr/bin/zsh", "zsh"},
		{"plain powershell", "", "", "powershell"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, "MSYSTEM", tc.msystem)
			withEnv(t, "SHELL", tc.shell)
			if got := defaultShell(); got != tc.want {
				t.Errorf("defaultShell() = %q, want %q; the wrong syntax is emitted and the "+
					"switch silently does nothing", got, tc.want)
			}
		})
	}
}

// A .nvmrc is a version, not a file to concatenate.
//
// The whole file was taken and trimmed, so a comment or any second line became
// part of the version query -- and the remedy nvx printed then carried an
// embedded newline and could not be run. Comments in .nvmrc are ordinary.
func TestVersionFileTakesTheFirstMeaningfulLine(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"plain", "22", "22"},
		{"trailing newline", "22\n", "22"},
		{"crlf", "22\r\n", "22"},
		{"comment after", "22\n# note\n", "22"},
		{"comment before", "# pin for CI\n22\n", "22"},
		{"blank lines", "\n\n22\n\n", "22"},
		{"surrounding space", "  22  \n", "22"},
		{"empty", "", ""},
		{"comments only", "# nothing here\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstVersionLine([]byte(tc.body)); got != tc.want {
				t.Errorf("firstVersionLine(%q) = %q, want %q", tc.body, got, tc.want)
			}
			if strings.Contains(firstVersionLine([]byte(tc.body)), "\n") {
				t.Error("a version query with a newline in it produces advice that cannot be run")
			}
		})
	}
}

// A half-finished install must not read as an installed runtime.
func TestStagingDirectoriesAreNotInstalledVersions(t *testing.T) {
	for _, name := range []string{"v18.20.8.tmp.40480", "v20.20.2.tmp.1", "v1.2.3.tmp.99999"} {
		if !isStagingVersionDir(name) {
			t.Errorf("%q is an interrupted extraction but would be listed as installed, and "+
				"`use` would select it -- node works from the partial tree while npm does not", name)
		}
	}
	for _, name := range []string{"v22.23.2", "v20.20.2", "v1.4.0"} {
		if isStagingVersionDir(name) {
			t.Errorf("%q is a real installed version but was treated as staging", name)
		}
	}
}
