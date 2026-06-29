package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	res := CleanAndBuildPath(currentPath, nvwHome, targetVersionDir)
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
			Enabled:  false,
			Provider: "native",
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
			Enabled:  true,
			Provider: "custom",
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
	if merged.Isolation.Provider != "custom" {
		t.Errorf("expected Isolation.Provider to be 'custom', got %q", merged.Isolation.Provider)
	}
}

func TestCleanAndBuildPath_EdgeCases(t *testing.T) {
	nvHome := `/home/user/.nvx`
	targetVer := `/home/user/.nvx/versions/node/v18.16.0`

	// Test with empty path
	res := CleanAndBuildPath("", nvHome, targetVer)
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

	res2 := CleanAndBuildPath(duplicatedPath, nvHome, targetVer)
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

func TestCleanupStaleSandboxes(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "nvx-test-cleanup")
	defer os.RemoveAll(tmpDir)

	sandboxDir := getSandboxHomeDir(tmpDir)
	fakeSandboxPath := filepath.Join(sandboxDir, "stale-session-123")
	
	err := os.MkdirAll(fakeSandboxPath, 0755)
	if err != nil {
		t.Fatalf("failed to create fake stale sandbox path: %v", err)
	}

	// Verify it exists
	if _, err := os.Stat(fakeSandboxPath); os.IsNotExist(err) {
		t.Fatal("expected fake sandbox path to exist")
	}

	// Run cleanup
	cleanupStaleSandboxes(tmpDir)

	// Verify it is gone
	if _, err := os.Stat(fakeSandboxPath); !os.IsNotExist(err) {
		t.Error("expected fake sandbox path to be deleted by cleanupStaleSandboxes")
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

	// Write parent policy: block "parent-blocked" and add trusted package "trusted-parent"
	parentPolicy := Policy{
		BlockedPackages: []string{"parent-blocked"},
		Typosquatting: TyposquattingPolicy{
			TrustedPackages: []string{"trusted-parent"},
		},
	}
	pData, _ := json.Marshal(parentPolicy)
	_ = os.WriteFile(filepath.Join(parentDir, ".nvx-policy.json"), pData, 0644)

	// Write child policy: block "child-blocked"
	childPolicy := Policy{
		BlockedPackages: []string{"child-blocked"},
	}
	cData, _ := json.Marshal(childPolicy)
	_ = os.WriteFile(filepath.Join(childDir, "policy.json"), cData, 0644)

	// Change working directory to childDir
	err = os.Chdir(childDir)
	if err != nil {
		t.Fatalf("failed to change wd to childDir: %v", err)
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

