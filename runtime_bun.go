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

// BunProvider implements RuntimeProvider for the Bun runtime, installing
// prebuilt binaries from the oven-sh/bun GitHub releases. It is a worked
// example of a second runtime: everything Node-specific lives in NodeProvider,
// everything Bun-specific lives here, and both are wired in only via
// RegisterRuntimeProvider — see docs/EXTENDING.md.
type BunProvider struct{}

func init() { RegisterRuntimeProvider(BunProvider{}) }

func (BunProvider) Name() string { return "bun" }

// ShimCommands: the Bun runtime owns `bun` and `bunx`.
func (BunProvider) ShimCommands() []string { return []string{"bun", "bunx"} }

// DefaultNetworkAllow lists the hosts Bun needs inside the sandbox: the npm
// registry (Bun installs npm packages) and the OSV database for scanning.
func (BunProvider) DefaultNetworkAllow() []string {
	return []string{"registry.npmjs.org:443", "api.osv.dev:443"}
}

const bunReleasesAPI = "https://api.github.com/repos/oven-sh/bun/releases"

// bunHTTPGet issues a GET with the User-Agent GitHub's API requires.
func bunHTTPGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nvx-runtime-manager")
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	return client.Do(req)
}

type bunRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
}

// bunTagToVersion converts a "bun-v1.1.30" tag to a "v1.1.30" directory name,
// matching the "v"-prefixed convention the shared local-version helpers expect.
func bunTagToVersion(tag string) string {
	v := strings.TrimPrefix(tag, "bun-")
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

func fetchBunReleases() ([]bunRelease, error) {
	resp, err := bunHTTPGet(bunReleasesAPI + "?per_page=100")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch bun releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch bun releases: HTTP %s", resp.Status)
	}
	var releases []bunRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to parse bun release JSON: %w", err)
	}
	return releases, nil
}

func (BunProvider) ListRemote() ([]string, error) {
	releases, err := fetchBunReleases()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range releases {
		if strings.HasPrefix(r.TagName, "bun-v") {
			out = append(out, bunTagToVersion(r.TagName))
		}
	}
	return out, nil
}

func (BunProvider) ResolveVersion(query string) (string, error) {
	query = strings.TrimSpace(strings.ToLower(query))

	if query == "latest" || query == "" || query == "current" {
		resp, err := bunHTTPGet(bunReleasesAPI + "/latest")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to resolve latest bun: HTTP %s", resp.Status)
		}
		var r bunRelease
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return "", err
		}
		if !strings.HasPrefix(r.TagName, "bun-v") {
			return "", fmt.Errorf("unexpected latest bun tag %q", r.TagName)
		}
		return bunTagToVersion(r.TagName), nil
	}

	releases, err := fetchBunReleases()
	if err != nil {
		return "", err
	}
	versions := make([]string, 0, len(releases))
	for _, r := range releases {
		if strings.HasPrefix(r.TagName, "bun-v") {
			versions = append(versions, bunTagToVersion(r.TagName))
		}
	}
	// Exact match, then highest matching prefix, using the shared semver-aware compare.
	want := query
	if !strings.HasPrefix(want, "v") {
		want = "v" + want
	}
	for _, v := range versions {
		if strings.EqualFold(v, want) {
			return v, nil
		}
	}
	best := ""
	for _, v := range versions {
		if strings.HasPrefix(strings.ToLower(v), want+".") {
			if best == "" || CompareVersions(v, best) > 0 {
				best = v
			}
		}
	}
	if best != "" {
		return best, nil
	}
	return "", fmt.Errorf("no bun release found matching %q", query)
}

func (BunProvider) ListLocal(nvxHome string) ([]string, error) {
	dir := filepath.Join(nvxHome, "versions", "bun")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			versions = append(versions, e.Name())
		}
	}
	return versions, nil
}

// DetectConfig looks for a .bun-version file (Bun's native version pin) walking
// up from dir, then falls back to package.json "packageManager": "bun@x".
func (BunProvider) DetectConfig(dir string) (string, string, error) {
	d := dir
	for {
		bv := filepath.Join(d, ".bun-version")
		if data, err := os.ReadFile(bv); err == nil {
			return strings.TrimSpace(string(data)), bv, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "", "", nil
}

// bunAssetName returns the release asset for the current OS/arch, e.g.
// "bun-linux-x64" / "bun-darwin-aarch64" / "bun-windows-x64".
func bunAssetName() (string, error) {
	var osPart string
	switch runtime.GOOS {
	case "linux":
		osPart = "linux"
	case "darwin":
		osPart = "darwin"
	case "windows":
		osPart = "windows"
	default:
		return "", fmt.Errorf("bun: unsupported OS %q", runtime.GOOS)
	}
	var archPart string
	switch runtime.GOARCH {
	case "amd64":
		archPart = "x64"
	case "arm64":
		archPart = "aarch64"
	default:
		return "", fmt.Errorf("bun: unsupported arch %q", runtime.GOARCH)
	}
	return fmt.Sprintf("bun-%s-%s", osPart, archPart), nil
}

func (b BunProvider) Install(version string, nvxHome string) error {
	resolved, err := b.ResolveVersion(version)
	if err != nil {
		return err
	}
	destDir := filepath.Join(nvxHome, "versions", "bun", resolved)
	if info, err := os.Stat(destDir); err == nil && info.IsDir() {
		LogSuccess("Bun %s is already installed.", resolved)
		return nil
	}

	asset, err := bunAssetName()
	if err != nil {
		return err
	}
	tag := "bun-" + resolved // resolved is "v1.1.30" -> tag "bun-v1.1.30"
	base := fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/%s", tag)
	zipURL := fmt.Sprintf("%s/%s.zip", base, asset)
	archiveName := asset + ".zip"
	tempZip := filepath.Join(GetDownloadsDir(), archiveName)

	LogInfo("Installing Bun %s (%s)", resolved, asset)
	LogInfo("URL: %s", zipURL)
	if err := DownloadFile(zipURL, tempZip); err != nil {
		return err
	}
	defer os.Remove(tempZip)

	if err := verifyBunChecksum(base, archiveName, tempZip); err != nil {
		return err
	}

	extractRoot := destDir + ".tmp"
	_ = os.RemoveAll(extractRoot)
	if err := ExtractZip(tempZip, extractRoot); err != nil {
		return err
	}
	defer os.RemoveAll(extractRoot)

	// The zip contains "<asset>/bun" (bun.exe on Windows); ExtractZip strips the
	// leading "<asset>/" component, so the binary lands at extractRoot/bun.
	binName := "bun"
	if runtime.GOOS == "windows" {
		binName = "bun.exe"
	}
	srcBin := filepath.Join(extractRoot, binName)
	if _, err := os.Stat(srcBin); err != nil {
		// Fallback: some archives may not have a leading dir to strip.
		alt := filepath.Join(extractRoot, asset, binName)
		if _, aErr := os.Stat(alt); aErr == nil {
			srcBin = alt
		} else {
			return fmt.Errorf("bun binary not found in archive (looked at %s and %s)", srcBin, alt)
		}
	}
	binDir := filepath.Join(destDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	dstBin := filepath.Join(binDir, binName)
	if err := os.Rename(srcBin, dstBin); err != nil {
		// Fallback to copy across filesystems.
		if cErr := copyFileMode(srcBin, dstBin, 0o755); cErr != nil {
			return cErr
		}
	}
	_ = os.Chmod(dstBin, 0o755)

	LogSuccess("Bun %s installed successfully to: %s", resolved, destDir)
	return nil
}

func (b BunProvider) Uninstall(version string, nvxHome string) error {
	resolved, err := resolveLocalVersion(b, version, nvxHome)
	if err != nil {
		return err
	}
	destDir := filepath.Join(nvxHome, "versions", "bun", resolved)
	LogInfo("Uninstalling Bun %s...", resolved)
	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	LogSuccess("Bun %s uninstalled successfully.", resolved)
	return nil
}

func (BunProvider) ResolveBinary(cmd, nvxHome, pinnedVer string) string {
	b := BunProvider{}
	resolved, err := resolveLocalVersion(b, pinnedVer, nvxHome)
	if err != nil {
		return ""
	}
	binName := "bun"
	if runtime.GOOS == "windows" {
		binName = "bun.exe"
	}
	// Both `bun` and `bunx` route through the bun binary (bunx == `bun x`).
	p := filepath.Join(nvxHome, "versions", "bun", resolved, "bin", binName)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// verifyBunChecksum downloads the release SHASUMS256.txt and verifies the
// archive's SHA-256, mirroring the Node checksum flow.
func verifyBunChecksum(releaseBaseURL, archiveName, filePath string) error {
	resp, err := bunHTTPGet(releaseBaseURL + "/SHASUMS256.txt")
	if err != nil {
		return fmt.Errorf("bun checksum fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bun checksum fetch failed: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archiveName {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("bun checksum for %s not found in SHASUMS256.txt", archiveName)
	}
	got, err := ComputeSHA256(filePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("bun checksum mismatch for %s: got %s want %s", archiveName, got, want)
	}
	LogSuccess("Bun archive checksum verified.")
	return nil
}

// copyFileMode copies src to dst with the given mode (rename fallback).
func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
