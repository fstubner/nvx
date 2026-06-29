package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

// RuntimeProvider defines version management operations for any language runtime
type RuntimeProvider interface {
	Name() string
	Install(version string, nvxHome string) error
	Uninstall(version string, nvxHome string) error
	ResolveVersion(query string) (string, error)
	ListRemote() ([]string, error)
	ListLocal(nvxHome string) ([]string, error)
	DetectConfig(dir string) (version string, sourceFile string, err error)
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

	if info, err := os.Stat(destDir); err == nil && info.IsDir() {
		LogSuccess("Node.js %s is already installed.", resolvedVer)
		return nil
	}

	arch := GetArch()
	archiveFilename := fmt.Sprintf("node-%s-%s-%s.%s", resolvedVer, getOS(), arch, getExtension())
	url := fmt.Sprintf("https://nodejs.org/dist/%s/%s", resolvedVer, archiveFilename)
	tempFile := filepath.Join(GetDownloadsDir(), archiveFilename)

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

	if getOS() == "win" {
		err = ExtractZip(tempFile, destDir)
	} else {
		err = ExtractTarGz(tempFile, destDir)
	}

	if err != nil {
		return err
	}

	LogSuccess("Node.js %s installed successfully to: %s", resolvedVer, destDir)
	return nil


}

func (n NodeProvider) Uninstall(version string, nvxHome string) error {
	resolvedVer, err := resolveLocalVersion(n, version, nvxHome)
	if err != nil {
		return err
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
			_ = os.MkdirAll(nodeDir, 0755)
			oldPath := filepath.Join(legacyDir, entry.Name())
			newPath := filepath.Join(nodeDir, entry.Name())
			_ = os.Rename(oldPath, newPath)
		}
	}
}

// Providers maps runtime names to their respective RuntimeProvider implementations
var Providers = map[string]RuntimeProvider{
	"node": NodeProvider{},
}

