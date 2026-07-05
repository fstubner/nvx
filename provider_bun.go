package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// BunProvider implements RuntimeProvider for the Bun runtime. Bun ships a single
// binary per platform as a zip archive with a per-release SHASUMS256.txt, so it
// reuses the same download + checksum + extract path as Node.
type BunProvider struct{}

func (b BunProvider) Name() string { return "bun" }

func (b BunProvider) ShimCommands() []string { return []string{"bun", "bunx"} }

func (b BunProvider) SandboxImage(version string) string {
	return runtimeDockerImage("oven/bun", version)
}

func (b BunProvider) SessionEnv(versionDir string) map[string]string { return nil }

func (b BunProvider) DefaultNetworkAllow() []string {
	return []string{
		"registry.npmjs.org:443",
		"github.com:443",
		"objects.githubusercontent.com:443",
		"api.osv.dev:443",
	}
}

// --- version discovery -------------------------------------------------------

type bunReleaseCache struct {
	FetchedAt time.Time `json:"fetched_at"`
	Versions  []string  `json:"versions"` // newest first, e.g. ["v1.2.19", ...]
}

func bunCachePath(nvxHome string) string {
	return filepath.Join(nvxHome, "cache", "bun-releases.json")
}

func bunTagToVersion(tag string) string {
	tag = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tag), "bun-"))
	if tag == "" {
		return ""
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	if len(tag) < 2 || (tag[1] < '0' || tag[1] > '9') {
		return ""
	}
	return tag
}

// fetchBunReleases returns Bun versions newest-first, using a ~6h on-disk cache
// to stay well under GitHub's unauthenticated rate limit. On a network error it
// falls back to a stale cache when available.
func fetchBunReleases(nvxHome string) ([]string, error) {
	cachePath := bunCachePath(nvxHome)
	if data, err := os.ReadFile(cachePath); err == nil {
		var c bunReleaseCache
		if json.Unmarshal(data, &c) == nil && len(c.Versions) > 0 && time.Since(c.FetchedAt) < 6*time.Hour {
			return c.Versions, nil
		}
	}

	versions, err := fetchBunReleasesFromGitHub()
	if err != nil {
		if data, rerr := os.ReadFile(cachePath); rerr == nil {
			var c bunReleaseCache
			if json.Unmarshal(data, &c) == nil && len(c.Versions) > 0 {
				LogWarn("Using cached Bun release list (network fetch failed: %v)", err)
				return c.Versions, nil
			}
		}
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err == nil {
		if data, mErr := json.MarshalIndent(bunReleaseCache{FetchedAt: time.Now(), Versions: versions}, "", "  "); mErr == nil {
			_ = os.WriteFile(cachePath, data, 0600)
		}
	}
	return versions, nil
}

func fetchBunReleasesFromGitHub() ([]string, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/oven-sh/bun/releases?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nvx")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Bun releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch Bun releases: HTTP %s", resp.Status)
	}

	var raw []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse Bun releases: %w", err)
	}

	var versions []string
	for _, r := range raw {
		if r.Draft || r.Prerelease {
			continue
		}
		if v := bunTagToVersion(r.TagName); v != "" {
			versions = append(versions, v)
		}
	}
	return versions, nil
}

func isFullBunVersion(v string) bool {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func matchBunVersion(query string, versions []string) string {
	for _, v := range versions {
		if strings.EqualFold(v, query) {
			return v
		}
	}
	for _, v := range versions {
		if strings.HasPrefix(strings.ToLower(v), strings.ToLower(query)+".") {
			return v
		}
	}
	return ""
}

// resolveInstallVersion turns a query into a concrete vX.Y.Z. Fully-qualified
// versions skip the GitHub API entirely (the download URL is constructed
// directly); "latest" and partial queries consult the cached release list.
func (b BunProvider) resolveInstallVersion(query, nvxHome string) (string, error) {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" || q == "latest" || q == "current" {
		versions, err := fetchBunReleases(nvxHome)
		if err != nil {
			return "", err
		}
		if len(versions) == 0 {
			return "", fmt.Errorf("no Bun releases available")
		}
		return versions[0], nil
	}

	norm := q
	if !strings.HasPrefix(norm, "v") {
		norm = "v" + norm
	}
	if isFullBunVersion(norm) {
		return norm, nil
	}

	versions, err := fetchBunReleases(nvxHome)
	if err != nil {
		return "", err
	}
	if m := matchBunVersion(norm, versions); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("no Bun release matches query %q", query)
}

func (b BunProvider) ResolveVersion(query string) (string, error) {
	return b.resolveInstallVersion(query, GetHomeDir())
}

func (b BunProvider) ListRemote() ([]string, error) {
	return fetchBunReleases(GetHomeDir())
}

func (b BunProvider) ListLocal(nvxHome string) ([]string, error) {
	dir := filepath.Join(nvxHome, "versions", "bun")
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

// --- config detection --------------------------------------------------------

func (b BunProvider) DetectConfig(dir string) (version string, sourceFile string, err error) {
	d, aErr := filepath.Abs(dir)
	if aErr != nil {
		d = dir
	}
	for {
		bunVersion := filepath.Join(d, ".bun-version")
		if info, err := os.Stat(bunVersion); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(bunVersion); err == nil {
				return strings.TrimSpace(string(content)), bunVersion, nil
			}
		}

		pkgJSON := filepath.Join(d, "package.json")
		if info, err := os.Stat(pkgJSON); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(pkgJSON); err == nil {
				var pkg PackageJSON
				if json.Unmarshal(content, &pkg) == nil && pkg.Engines.Bun != "" {
					return CleanEngineRange(pkg.Engines.Bun), pkgJSON, nil
				}
			}
		}

		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "", "", nil
}

// --- binary resolution -------------------------------------------------------

func bunBinaryPath(versionDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(versionDir, "bun.exe")
	}
	return filepath.Join(versionDir, "bin", "bun")
}

func isBunVersionInstalled(nvxHome, version string) bool {
	info, err := os.Stat(bunBinaryPath(filepath.Join(nvxHome, "versions", "bun", version)))
	return err == nil && !info.IsDir()
}

func (b BunProvider) ResolveBinary(cmd, nvxHome, pinnedVer string) string {
	resolvedVer, err := resolveLocalVersion(b, pinnedVer, nvxHome)
	if err != nil {
		return ""
	}
	versionDir := filepath.Join(nvxHome, "versions", "bun", resolvedVer)

	var name string
	switch strings.ToLower(cmd) {
	case "bun":
		name = "bun"
	case "bunx":
		name = "bunx"
	default:
		return ""
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	var p string
	if runtime.GOOS == "windows" {
		p = filepath.Join(versionDir, name)
	} else {
		p = filepath.Join(versionDir, "bin", name)
	}
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// --- install / uninstall -----------------------------------------------------

func bunPlatform() (osName string, arch string, err error) {
	switch runtime.GOOS {
	case "windows":
		osName = "windows"
	case "darwin":
		osName = "darwin"
	case "linux":
		osName = "linux"
	default:
		return "", "", fmt.Errorf("Bun does not provide builds for OS %q", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "aarch64"
	default:
		return "", "", fmt.Errorf("Bun does not provide builds for architecture %q", runtime.GOARCH)
	}
	if osName == "windows" && arch != "x64" {
		return "", "", fmt.Errorf("Bun provides only x64 builds on Windows")
	}
	return osName, arch, nil
}

func (b BunProvider) Install(version string, nvxHome string) error {
	resolvedVer, err := b.resolveInstallVersion(version, nvxHome)
	if err != nil {
		return err
	}
	if isBunVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Bun %s is already installed.", resolvedVer)
		return nil
	}

	release, err := acquireRuntimeInstallLock(nvxHome, "bun", resolvedVer)
	if err != nil {
		return err
	}
	defer release()
	if isBunVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Bun %s is already installed.", resolvedVer)
		return nil
	}

	osName, arch, err := bunPlatform()
	if err != nil {
		return err
	}
	asset := fmt.Sprintf("bun-%s-%s.zip", osName, arch)
	base := "https://github.com/oven-sh/bun/releases/download/bun-" + resolvedVer
	url := base + "/" + asset
	shaURL := base + "/SHASUMS256.txt"

	tempFile := filepath.Join(GetDownloadsDir(), asset)
	LogInfo("Installing Bun %s (%s-%s)", resolvedVer, osName, arch)
	LogInfo("URL: %s", url)

	if err := DownloadFile(url, tempFile); err != nil {
		return err
	}
	defer os.Remove(tempFile)

	if err := VerifyChecksumFromShasums(shaURL, tempFile, asset); err != nil {
		return err
	}

	extractDir := filepath.Join(GetDownloadsDir(), fmt.Sprintf("bun-extract-%d", os.Getpid()))
	_ = os.RemoveAll(extractDir)
	defer os.RemoveAll(extractDir)
	if err := ExtractZip(tempFile, extractDir); err != nil {
		return err
	}

	srcBin := filepath.Join(extractDir, fmt.Sprintf("bun-%s-%s", osName, arch), "bun")
	if runtime.GOOS == "windows" {
		srcBin += ".exe"
	}
	if info, err := os.Stat(srcBin); err != nil || info.IsDir() {
		if found := findBunBinary(extractDir); found != "" {
			srcBin = found
		} else {
			return fmt.Errorf("extracted Bun archive did not contain a bun binary")
		}
	}

	destDir := filepath.Join(nvxHome, "versions", "bun", resolvedVer)
	staging := destDir + fmt.Sprintf(".tmp.%d", os.Getpid())
	_ = os.RemoveAll(staging)
	defer os.RemoveAll(staging)
	if err := placeBunBinary(srcBin, staging); err != nil {
		return err
	}
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("remove incomplete install directory: %w", err)
	}
	if err := os.Rename(staging, destDir); err != nil {
		return fmt.Errorf("activate installed Bun version: %w", err)
	}

	LogSuccess("Bun %s installed successfully to: %s", resolvedVer, destDir)
	return nil
}

func (b BunProvider) Uninstall(version string, nvxHome string) error {
	resolvedVer, err := resolveLocalVersion(b, version, nvxHome)
	if err != nil {
		return err
	}
	if getGlobalDefaultVersionFor(nvxHome, "bun") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Bun %s because it is the global default; set a different default first", resolvedVer)
	}
	if getActiveShellVersionFor(nvxHome, "bun") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Bun %s because it is active in this shell", resolvedVer)
	}

	destDir := filepath.Join(nvxHome, "versions", "bun", resolvedVer)
	LogInfo("Uninstalling Bun %s...", resolvedVer)
	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	LogSuccess("Bun %s uninstalled successfully.", resolvedVer)
	return nil
}

// placeBunBinary installs the bun binary into the nvx version layout
// (versionDir/bin/bun on unix, versionDir/bun.exe on windows) and creates a
// bunx alias next to it (symlink on unix, hardlink/copy on windows).
func placeBunBinary(srcBin, destDir string) error {
	var binDir, bunName, bunxName string
	if runtime.GOOS == "windows" {
		binDir, bunName, bunxName = destDir, "bun.exe", "bunx.exe"
	} else {
		binDir, bunName, bunxName = filepath.Join(destDir, "bin"), "bun", "bunx"
	}
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return err
	}

	dstBun := filepath.Join(binDir, bunName)
	if err := copyExecutable(srcBin, dstBun); err != nil {
		return err
	}

	dstBunx := filepath.Join(binDir, bunxName)
	_ = os.Remove(dstBunx)
	if runtime.GOOS == "windows" {
		if err := os.Link(dstBun, dstBunx); err != nil {
			return copyExecutable(dstBun, dstBunx)
		}
		return nil
	}
	if err := os.Symlink(bunName, dstBunx); err != nil {
		return copyExecutable(dstBun, dstBunx)
	}
	return nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0755)
}

func findBunBinary(root string) string {
	target := "bun"
	if runtime.GOOS == "windows" {
		target = "bun.exe"
	}
	var found string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || found != "" || info.IsDir() {
			return nil
		}
		if strings.EqualFold(info.Name(), target) {
			found = path
		}
		return nil
	})
	return found
}
