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

// DenoProvider implements RuntimeProvider for the Deno runtime. Like Bun, Deno
// ships a single static binary per platform as a zip archive; each release
// asset has a sibling <asset>.sha256sum file, so installs reuse the shared
// download + checksum + extract pipeline.
type DenoProvider struct{}

func (d DenoProvider) Name() string { return "deno" }

func (d DenoProvider) ShimCommands() []string { return []string{"deno"} }

func (d DenoProvider) DefaultNetworkAllow() []string {
	return []string{
		"deno.land:443",
		"jsr.io:443",
		"registry.npmjs.org:443",
		"api.osv.dev:443",
	}
}

func (d DenoProvider) SandboxImage(version string) string {
	return runtimeDockerImage("denoland/deno", version)
}

func (d DenoProvider) SessionEnv(versionDir string) map[string]string { return nil }

// --- version discovery ---------------------------------------------------------

func denoCachePath(nvxHome string) string {
	return filepath.Join(nvxHome, "cache", "deno-releases.json")
}

// fetchDenoReleases returns Deno versions newest-first, using the same ~6h
// on-disk cache strategy as Bun to stay under GitHub's unauthenticated limit.
func fetchDenoReleases(nvxHome string) ([]string, error) {
	cachePath := denoCachePath(nvxHome)
	if data, err := os.ReadFile(cachePath); err == nil {
		var c githubReleaseCache
		if json.Unmarshal(data, &c) == nil && len(c.Versions) > 0 && time.Since(c.FetchedAt) < 6*time.Hour {
			return c.Versions, nil
		}
	}

	versions, err := fetchDenoReleasesFromGitHub()
	if err != nil {
		if data, rerr := os.ReadFile(cachePath); rerr == nil {
			var c githubReleaseCache
			if json.Unmarshal(data, &c) == nil && len(c.Versions) > 0 {
				LogWarn("Using cached Deno release list (network fetch failed: %v)", err)
				return c.Versions, nil
			}
		}
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err == nil {
		if data, mErr := json.MarshalIndent(githubReleaseCache{FetchedAt: time.Now(), Versions: versions}, "", "  "); mErr == nil {
			_ = os.WriteFile(cachePath, data, 0600)
		}
	}
	return versions, nil
}

func fetchDenoReleasesFromGitHub() ([]string, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/denoland/deno/releases?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nvx")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Deno releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch Deno releases: HTTP %s", resp.Status)
	}

	var raw []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse Deno releases: %w", err)
	}

	var versions []string
	for _, r := range raw {
		if r.Draft || r.Prerelease {
			continue
		}
		tag := strings.TrimSpace(r.TagName)
		if strings.HasPrefix(tag, "v") && len(tag) > 1 && tag[1] >= '0' && tag[1] <= '9' {
			versions = append(versions, tag)
		}
	}
	return versions, nil
}

// resolveInstallVersion mirrors Bun's strategy: fully-qualified versions skip
// the release API entirely; "latest" and partial queries consult the cache.
func (d DenoProvider) resolveInstallVersion(query, nvxHome string) (string, error) {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" || q == "latest" || q == "current" {
		versions, err := fetchDenoReleases(nvxHome)
		if err != nil {
			return "", err
		}
		if len(versions) == 0 {
			return "", fmt.Errorf("no Deno releases available")
		}
		return versions[0], nil
	}

	norm := q
	if !strings.HasPrefix(norm, "v") {
		norm = "v" + norm
	}
	if isExactSemver(norm) { // 3-part numeric check, runtime-agnostic
		return norm, nil
	}

	versions, err := fetchDenoReleases(nvxHome)
	if err != nil {
		return "", err
	}
	if m := matchVersionPrefix(norm, versions); m != "" { // prefix match, runtime-agnostic
		return m, nil
	}
	return "", fmt.Errorf("no Deno release matches query %q", query)
}

func (d DenoProvider) ResolveVersion(query string) (string, error) {
	return d.resolveInstallVersion(query, GetHomeDir())
}

func (d DenoProvider) ListRemote() ([]string, error) {
	return fetchDenoReleases(GetHomeDir())
}

func (d DenoProvider) ListLocal(nvxHome string) ([]string, error) {
	dir := filepath.Join(nvxHome, "versions", "deno")
	entries, err := os.ReadDir(dir)
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

// --- config detection ------------------------------------------------------------

func (d DenoProvider) DetectConfig(dir string) (version string, sourceFile string, err error) {
	p, aErr := filepath.Abs(dir)
	if aErr != nil {
		p = dir
	}
	for {
		denoVersion := filepath.Join(p, ".deno-version")
		if info, err := os.Stat(denoVersion); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(denoVersion); err == nil {
				return strings.TrimSpace(string(content)), denoVersion, nil
			}
		}

		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return "", "", nil
}

// --- binary resolution ------------------------------------------------------------

func denoBinaryPath(versionDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(versionDir, "deno.exe")
	}
	return filepath.Join(versionDir, "bin", "deno")
}

func isDenoVersionInstalled(nvxHome, version string) bool {
	info, err := os.Stat(denoBinaryPath(filepath.Join(nvxHome, "versions", "deno", version)))
	return err == nil && !info.IsDir()
}

func (d DenoProvider) ResolveBinary(cmd, nvxHome, pinnedVer string) string {
	if !strings.EqualFold(cmd, "deno") {
		return ""
	}
	resolvedVer, err := resolveLocalVersion(d, pinnedVer, nvxHome)
	if err != nil {
		return ""
	}
	p := denoBinaryPath(filepath.Join(nvxHome, "versions", "deno", resolvedVer))
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// --- install / uninstall ------------------------------------------------------------

// denoTarget maps GOOS/GOARCH to Deno's release target triple.
func denoTarget() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH != "amd64" {
			return "", fmt.Errorf("Deno provides only x86_64 builds on Windows")
		}
		return "x86_64-pc-windows-msvc", nil
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			return "x86_64-apple-darwin", nil
		case "arm64":
			return "aarch64-apple-darwin", nil
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return "x86_64-unknown-linux-gnu", nil
		case "arm64":
			return "aarch64-unknown-linux-gnu", nil
		}
	}
	return "", fmt.Errorf("Deno does not provide builds for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func (d DenoProvider) Install(version string, nvxHome string) error {
	resolvedVer, err := d.resolveInstallVersion(version, nvxHome)
	if err != nil {
		return err
	}
	if isDenoVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Deno %s is already installed.", resolvedVer)
		return nil
	}

	release, err := acquireRuntimeInstallLock(nvxHome, "deno", resolvedVer)
	if err != nil {
		return err
	}
	defer release()
	if isDenoVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Deno %s is already installed.", resolvedVer)
		return nil
	}

	target, err := denoTarget()
	if err != nil {
		return err
	}
	asset := "deno-" + target + ".zip"
	base := "https://github.com/denoland/deno/releases/download/" + resolvedVer
	url := base + "/" + asset
	shaURL := url + ".sha256sum"

	tempFile := filepath.Join(GetDownloadsDir(), asset)
	LogInfo("Installing Deno %s (%s)", resolvedVer, target)
	LogInfo("URL: %s", url)

	if err := DownloadFile(url, tempFile); err != nil {
		return err
	}
	defer os.Remove(tempFile)

	if err := VerifyChecksumFromShasums(shaURL, tempFile, asset); err != nil {
		return err
	}

	extractDir := filepath.Join(GetDownloadsDir(), fmt.Sprintf("deno-extract-%d", os.Getpid()))
	_ = os.RemoveAll(extractDir)
	defer os.RemoveAll(extractDir)
	if err := ExtractZipFlat(tempFile, extractDir); err != nil {
		return err
	}

	// The archive contains a single deno binary at its root.
	srcBin := filepath.Join(extractDir, "deno")
	if runtime.GOOS == "windows" {
		srcBin += ".exe"
	}
	if info, err := os.Stat(srcBin); err != nil || info.IsDir() {
		return fmt.Errorf("extracted Deno archive did not contain a deno binary")
	}

	destDir := filepath.Join(nvxHome, "versions", "deno", resolvedVer)
	staging := destDir + fmt.Sprintf(".tmp.%d", os.Getpid())
	_ = os.RemoveAll(staging)
	defer os.RemoveAll(staging)

	binDir := staging
	if runtime.GOOS != "windows" {
		binDir = filepath.Join(staging, "bin")
	}
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return err
	}
	dstName := "deno"
	if runtime.GOOS == "windows" {
		dstName = "deno.exe"
	}
	if err := copyExecutable(srcBin, filepath.Join(binDir, dstName)); err != nil {
		return err
	}

	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("remove incomplete install directory: %w", err)
	}
	if err := os.Rename(staging, destDir); err != nil {
		return fmt.Errorf("activate installed Deno version: %w", err)
	}

	LogSuccess("Deno %s installed successfully to: %s", resolvedVer, destDir)
	return nil
}

func (d DenoProvider) Uninstall(version string, nvxHome string) error {
	resolvedVer, err := resolveLocalVersion(d, version, nvxHome)
	if err != nil {
		return err
	}
	if getGlobalDefaultVersionFor(nvxHome, "deno") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Deno %s because it is the global default; set a different default first", resolvedVer)
	}
	if getActiveShellVersionFor(nvxHome, "deno") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Deno %s because it is active in this shell", resolvedVer)
	}

	destDir := filepath.Join(nvxHome, "versions", "deno", resolvedVer)
	LogInfo("Uninstalling Deno %s...", resolvedVer)
	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	LogSuccess("Deno %s uninstalled successfully.", resolvedVer)
	return nil
}
