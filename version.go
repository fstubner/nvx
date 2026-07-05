package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var appVersion = "0.3.0"

// Release represents a Node.js release from the official index.json
type Release struct {
	Version  string      `json:"version"`
	Date     string      `json:"date"`
	Files    []string    `json:"files"`
	Npm      string      `json:"npm"`
	Lts      interface{} `json:"lts"` // can be bool (false) or string (e.g. "Hydrogen")
	Security bool        `json:"security"`
}

// IsLTS returns true if the release is an LTS version
func (r Release) IsLTS() bool {
	if r.Lts == nil {
		return false
	}
	switch v := r.Lts.(type) {
	case bool:
		return v
	case string:
		return true
	default:
		return false
	}
}

// LTSName returns the codename of the LTS release if applicable
func (r Release) LTSName() string {
	if r.Lts == nil {
		return ""
	}
	if name, ok := r.Lts.(string); ok {
		return name
	}
	return ""
}

// FetchReleases fetches the list of Node.js releases from official nodejs.org mirror
func FetchReleases() ([]Release, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://nodejs.org/dist/index.json")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch releases: HTTP %s", resp.Status)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to parse release JSON: %w", err)
	}
	return releases, nil
}

// ResolveVersion matches a user-provided version query string to the latest matching release
func ResolveVersion(query string, releases []Release) (Release, error) {
	if len(releases) == 0 {
		return Release{}, fmt.Errorf("no releases available")
	}

	query = strings.TrimSpace(strings.ToLower(query))
	if query == "latest" || query == "current" {
		return releases[0], nil
	}

	if query == "lts" {
		for _, r := range releases {
			if r.IsLTS() {
				return r, nil
			}
		}
		return Release{}, fmt.Errorf("no LTS release found")
	}

	// Normalize query prefix
	q := query
	if !strings.HasPrefix(q, "v") {
		q = "v" + q
	}

	// Try exact match first (e.g. v18.16.0)
	for _, r := range releases {
		if strings.ToLower(r.Version) == q {
			return r, nil
		}
	}

	// Try prefix match (e.g. v18.16 matches v18.16.1, or v18 matches v18.20.2)
	for _, r := range releases {
		rVer := strings.ToLower(r.Version)
		if strings.HasPrefix(rVer, q+".") {
			return r, nil
		}
	}

	// Try custom LTS name match (e.g. "hydrogen", "iron")
	for _, r := range releases {
		if r.IsLTS() && strings.ToLower(r.LTSName()) == query {
			return r, nil
		}
	}

	return Release{}, fmt.Errorf("no release found matching query: %s", query)
}

// RuntimeProvider defines version management and execution hooks for a runtime.
type RuntimeProvider interface {
	Name() string
	Install(version string, nvxHome string) error
	Uninstall(version string, nvxHome string) error
	ResolveVersion(query string) (string, error)
	ListRemote() ([]string, error)
	ListLocal(nvxHome string) ([]string, error)
	DetectConfig(dir string) (version string, sourceFile string, err error)

	ShimCommands() []string
	ResolveBinary(cmd string, nvxHome string, pinnedVer string) string
	DefaultNetworkAllow() []string
	// SandboxImage returns the Docker image for a version (e.g. "node:20.11.0"),
	// or "" if the runtime has no container image.
	SandboxImage(version string) string
	// SessionEnv returns extra environment variables to set when this runtime is
	// activated for a shell session (e.g. GOROOT for Go, RUSTUP_HOME for Rust).
	// Node and Bun need none and return nil. This is the extension point that
	// lets future runtimes activate without changing the session-env plumbing.
	SessionEnv(versionDir string) map[string]string
}

// runtimeDockerImage builds "<repo>:<tag>" from a version, defaulting to the
// latest tag when no version is known.
func runtimeDockerImage(repo, version string) string {
	tag := "latest"
	if v := strings.TrimPrefix(version, "v"); v != "" {
		tag = v
	}
	return repo + ":" + tag
}

// NodeProvider implements RuntimeProvider for Node.js
type NodeProvider struct{}

func (n NodeProvider) Name() string {
	return "node"
}

func (n NodeProvider) ResolveVersion(query string) (string, error) {
	releases, err := FetchReleases()
	if err != nil {
		return "", err
	}
	r, err := ResolveVersion(query, releases)
	if err != nil {
		return "", err
	}
	return r.Version, nil
}

func (n NodeProvider) ListRemote() ([]string, error) {
	releases, err := FetchReleases()
	if err != nil {
		return nil, err
	}
	var list []string
	for _, r := range releases {
		list = append(list, r.Version)
	}
	return list, nil
}

func (n NodeProvider) ListLocal(nvxHome string) ([]string, error) {
	MigrateLegacyNodeVersions(nvxHome)
	nodeVersionsDir := filepath.Join(nvxHome, "versions", "node")
	entries, err := os.ReadDir(nodeVersionsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
			versions = append(versions, entry.Name())
		}
	}
	return versions, nil
}

func (n NodeProvider) DetectConfig(dir string) (version string, sourceFile string, err error) {
	return DetectVersionConfig(dir)
}

func isNodeVersionInstalled(nvxHome, version string) bool {
	versionDir := filepath.Join(nvxHome, "versions", "node", version)
	binary := nodeBinaryPath(versionDir)
	if info, err := os.Stat(binary); err == nil && !info.IsDir() {
		return true
	}
	return false
}

func nodeBinaryPath(versionDir string) string {
	binary := filepath.Join(versionDir, "bin", "node")
	if runtime.GOOS == "windows" {
		binary = filepath.Join(versionDir, "node.exe")
	}
	return binary
}

func acquireInstallLock(nvxHome, version string) (func(), error) {
	return acquireRuntimeInstallLock(nvxHome, "node", version)
}

func acquireRuntimeInstallLock(nvxHome, runtimeName, version string) (func(), error) {
	lockDir := filepath.Join(nvxHome, "versions", runtimeName)
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return nil, fmt.Errorf("create install lock directory: %w", err)
	}
	lockName, err := installLockFileName(version)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(lockDir, lockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("install for %s %s is already in progress or lock cannot be created: %w", runtimeName, version, err)
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	release := func() {
		_ = f.Close()
		_ = os.Remove(lockPath)
	}
	return release, nil
}

func installLockFileName(version string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "" || version == "." || version == ".." || filepath.Base(version) != version || filepath.VolumeName(version) != "" {
		return "", fmt.Errorf("invalid Node.js version for install lock: %q", version)
	}
	for _, r := range version {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("invalid Node.js version for install lock: %q", version)
	}
	return version + ".lock", nil
}

func (n NodeProvider) Install(version string, nvxHome string) error {
	releases, err := FetchReleases()
	if err != nil {
		return err
	}

	release, err := ResolveVersion(version, releases)
	if err != nil {
		return err
	}

	resolvedVer := release.Version
	destDir := filepath.Join(nvxHome, "versions", "node", resolvedVer)

	if isNodeVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Node.js %s is already installed.", resolvedVer)
		return nil
	}
	releaseLock, err := acquireInstallLock(nvxHome, resolvedVer)
	if err != nil {
		return err
	}
	defer releaseLock()
	if isNodeVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Node.js %s is already installed.", resolvedVer)
		return nil
	}

	arch := GetArch()
	archiveFilename := fmt.Sprintf("node-%s-%s-%s.%s", resolvedVer, getOS(), arch, getExtension())
	url := fmt.Sprintf("https://nodejs.org/dist/%s/%s", resolvedVer, archiveFilename)
	tempFile := filepath.Join(GetDownloadsDir(), archiveFilename)
	extractDir := destDir + fmt.Sprintf(".tmp.%d", os.Getpid())

	LogInfo("Installing Node.js %s (%s)", resolvedVer, arch)
	LogInfo("URL: %s", url)

	err = DownloadFile(url, tempFile)
	if err != nil {
		return err
	}
	defer os.Remove(tempFile)

	err = VerifyNodeChecksum(resolvedVer, tempFile, archiveFilename)
	if err != nil {
		return err
	}

	_ = os.RemoveAll(extractDir)
	defer os.RemoveAll(extractDir)
	if getOS() == "win" {
		err = ExtractZip(tempFile, extractDir)
	} else {
		err = ExtractTarGz(tempFile, extractDir)
	}

	if err != nil {
		return err
	}
	if info, err := os.Stat(nodeBinaryPath(extractDir)); err != nil || info.IsDir() {
		return fmt.Errorf("extracted Node.js archive did not contain expected runtime binary at %s", nodeBinaryPath(extractDir))
	}
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("remove incomplete install directory: %w", err)
	}
	if err := os.Rename(extractDir, destDir); err != nil {
		return fmt.Errorf("activate installed Node.js version: %w", err)
	}

	LogSuccess("Node.js %s installed successfully to: %s", resolvedVer, destDir)
	return nil

}

func (n NodeProvider) Uninstall(version string, nvxHome string) error {
	resolvedVer, err := resolveLocalVersion(n, version, nvxHome)
	if err != nil {
		return err
	}
	if getGlobalDefaultVersion(nvxHome) == resolvedVer {
		return fmt.Errorf("refusing to uninstall Node.js %s because it is the global default; set a different default first", resolvedVer)
	}
	if getActiveShellVersion(nvxHome) == resolvedVer {
		return fmt.Errorf("refusing to uninstall Node.js %s because it is active in this shell", resolvedVer)
	}

	destDir := filepath.Join(nvxHome, "versions", "node", resolvedVer)
	LogInfo("Uninstalling Node.js %s...", resolvedVer)

	err = os.RemoveAll(destDir)
	if err != nil {
		return err
	}

	LogSuccess("Node.js %s uninstalled successfully.", resolvedVer)
	return nil
}

// MigrateLegacyNodeVersions moves Node versions installed in the root of ~/.nvx/versions to ~/.nvx/versions/node
func MigrateLegacyNodeVersions(nvxHome string) {
	legacyDir := filepath.Join(nvxHome, "versions")
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return
	}

	nodeDir := filepath.Join(nvxHome, "versions", "node")
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
			if err := os.MkdirAll(nodeDir, 0700); err != nil {
				LogWarn("Failed to create Node versions directory for migration: %v", err)
				continue
			}
			oldPath := filepath.Join(legacyDir, entry.Name())
			newPath := filepath.Join(nodeDir, entry.Name())
			_ = os.Rename(oldPath, newPath)
		}
	}
}

// Providers maps runtime names to their respective RuntimeProvider implementations
var Providers = map[string]RuntimeProvider{
	"node": NodeProvider{},
	"bun":  BunProvider{},
	"deno": DenoProvider{},
	"go":   GoProvider{},
}
