package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PythonProvider implements RuntimeProvider for CPython using the
// python-build-standalone distribution (astral-sh), the same relocatable builds
// uv and mise use. It is the second non-JavaScript runtime and exercises the
// interface's rougher edges: releases are tagged by date and each carries many
// Python versions, so a version query resolves to an asset within the latest
// release; and on Windows pip lives in Scripts/ rather than next to python.exe.
//
// Scope: the interpreter (python, python3) is managed and shimmed. pip is used
// as `python -m pip` — python-build-standalone ships pip as a module, not a
// standalone launcher on every platform, and generating launchers is the same
// post-install-hook work noted in docs/runtime-providers.md.
type PythonProvider struct{}

func (p PythonProvider) Name() string { return "python" }

// ShimCommands covers the interpreter plus uv's CLIs. uv/uvx are shimmed when
// present on PATH (nvx does not install uv); pyx is an nvx alias for a sandboxed
// uvx (see runShim). This audits/contains the increasingly common case of
// running arbitrary PyPI tools via `uvx <tool>`.
func (p PythonProvider) ShimCommands() []string {
	return []string{"python", "python3", "uv", "uvx", "pyx"}
}

func (p PythonProvider) SandboxImage(version string) string {
	return runtimeDockerImage("python", version)
}

func (p PythonProvider) SessionEnv(versionDir string) map[string]string { return nil }

func (p PythonProvider) DefaultNetworkAllow() []string {
	return []string{
		"pypi.org:443",
		"files.pythonhosted.org:443",
		// uv fetches tools from PyPI and standalone Pythons from GitHub.
		"github.com:443",
		"objects.githubusercontent.com:443",
		"api.osv.dev:443",
	}
}

// --- release discovery ---------------------------------------------------------

type pythonAsset struct {
	Version  string `json:"version"` // internal, e.g. v3.12.13
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

type pythonReleaseCache struct {
	FetchedAt time.Time     `json:"fetched_at"`
	Tag       string        `json:"tag"`
	Assets    []pythonAsset `json:"assets"`
}

func pythonCachePath(nvxHome string) string {
	return filepath.Join(nvxHome, "cache", "python-releases.json")
}

// pythonTargetTriple maps GOOS/GOARCH to a python-build-standalone triple.
func pythonTargetTriple() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "amd64" {
			return "x86_64-pc-windows-msvc", nil
		}
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
	return "", fmt.Errorf("python-build-standalone has no build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func fetchPythonRelease(nvxHome string) (pythonReleaseCache, error) {
	cachePath := pythonCachePath(nvxHome)
	if data, err := os.ReadFile(cachePath); err == nil {
		var c pythonReleaseCache
		if json.Unmarshal(data, &c) == nil && len(c.Assets) > 0 && time.Since(c.FetchedAt) < 6*time.Hour {
			return c, nil
		}
	}

	rel, err := fetchPythonReleaseFromGitHub()
	if err != nil {
		if data, rerr := os.ReadFile(cachePath); rerr == nil {
			var c pythonReleaseCache
			if json.Unmarshal(data, &c) == nil && len(c.Assets) > 0 {
				LogWarn("Using cached Python release list (network fetch failed: %v)", err)
				return c, nil
			}
		}
		return pythonReleaseCache{}, err
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err == nil {
		if data, mErr := json.MarshalIndent(rel, "", "  "); mErr == nil {
			_ = os.WriteFile(cachePath, data, 0600)
		}
	}
	return rel, nil
}

func fetchPythonReleaseFromGitHub() (pythonReleaseCache, error) {
	triple, err := pythonTargetTriple()
	if err != nil {
		return pythonReleaseCache{}, err
	}

	req, err := http.NewRequest("GET", "https://api.github.com/repos/astral-sh/python-build-standalone/releases/latest", nil)
	if err != nil {
		return pythonReleaseCache{}, err
	}
	req.Header.Set("User-Agent", "nvx")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return pythonReleaseCache{}, fmt.Errorf("failed to fetch Python releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pythonReleaseCache{}, fmt.Errorf("failed to fetch Python releases: HTTP %s", resp.Status)
	}

	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return pythonReleaseCache{}, fmt.Errorf("failed to parse Python releases: %w", err)
	}

	// Match exactly the non-stripped, non-freethreaded install_only build.
	suffix := "-" + triple + "-install_only.tar.gz"
	var assets []pythonAsset
	for _, a := range raw.Assets {
		if !strings.HasPrefix(a.Name, "cpython-") || !strings.HasSuffix(a.Name, suffix) {
			continue
		}
		ver := a.Name[len("cpython-"):]
		if i := strings.IndexAny(ver, "+-"); i >= 0 {
			ver = ver[:i]
		}
		if ver == "" {
			continue
		}
		assets = append(assets, pythonAsset{Version: "v" + ver, Filename: a.Name, URL: a.URL})
	}
	sort.Slice(assets, func(i, j int) bool {
		return compareInternalVersions(assets[i].Version, assets[j].Version) > 0
	})
	return pythonReleaseCache{FetchedAt: time.Now(), Tag: raw.TagName, Assets: assets}, nil
}

// compareInternalVersions compares vA.B.C strings numerically; >0 if a>b.
func compareInternalVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var na, nb int
		if i < len(pa) {
			na, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			nb, _ = strconv.Atoi(pb[i])
		}
		if na != nb {
			return na - nb
		}
	}
	return 0
}

func (p PythonProvider) resolveInstallAsset(query, nvxHome string) (pythonReleaseCache, pythonAsset, error) {
	rel, err := fetchPythonRelease(nvxHome)
	if err != nil {
		return rel, pythonAsset{}, err
	}
	if len(rel.Assets) == 0 {
		return rel, pythonAsset{}, fmt.Errorf("no Python builds available for this platform")
	}

	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" || q == "latest" || q == "current" {
		return rel, rel.Assets[0], nil // sorted newest-first
	}

	norm := q
	if !strings.HasPrefix(norm, "v") {
		norm = "v" + norm
	}
	// Exact match, then newest matching prefix (assets are sorted desc).
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Version, norm) {
			return rel, a, nil
		}
	}
	for _, a := range rel.Assets {
		if strings.HasPrefix(strings.ToLower(a.Version), strings.ToLower(norm)+".") {
			return rel, a, nil
		}
	}
	return rel, pythonAsset{}, fmt.Errorf("no Python build matches query %q", query)
}

func (p PythonProvider) ResolveVersion(query string) (string, error) {
	_, a, err := p.resolveInstallAsset(query, GetHomeDir())
	if err != nil {
		return "", err
	}
	return a.Version, nil
}

func (p PythonProvider) ListRemote() ([]string, error) {
	rel, err := fetchPythonRelease(GetHomeDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range rel.Assets {
		out = append(out, a.Version)
	}
	return out, nil
}

func (p PythonProvider) ListLocal(nvxHome string) ([]string, error) {
	dir := filepath.Join(nvxHome, "versions", "python")
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

// --- config detection ----------------------------------------------------------

func (p PythonProvider) DetectConfig(dir string) (version string, sourceFile string, err error) {
	d, aErr := filepath.Abs(dir)
	if aErr != nil {
		d = dir
	}
	for {
		pyVersion := filepath.Join(d, ".python-version")
		if info, err := os.Stat(pyVersion); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(pyVersion); err == nil {
				// The file may list several versions; use the first line.
				line := strings.TrimSpace(content2FirstLine(string(content)))
				if line != "" {
					return line, pyVersion, nil
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

func content2FirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// --- binary resolution ---------------------------------------------------------

// pythonCommandPath returns the interpreter path. On Windows python.exe sits at
// the version root; on Unix it is under bin/python3.
func pythonCommandPath(versionDir, cmd string) string {
	switch strings.ToLower(cmd) {
	case "python", "python3":
		if runtime.GOOS == "windows" {
			return filepath.Join(versionDir, "python.exe")
		}
		return filepath.Join(versionDir, "bin", "python3")
	}
	return ""
}

func isPythonVersionInstalled(nvxHome, version string) bool {
	info, err := os.Stat(pythonCommandPath(filepath.Join(nvxHome, "versions", "python", version), "python"))
	return err == nil && !info.IsDir()
}

func (p PythonProvider) ResolveBinary(cmd, nvxHome, pinnedVer string) string {
	resolvedVer, err := resolveLocalVersion(p, pinnedVer, nvxHome)
	if err != nil {
		return ""
	}
	path := pythonCommandPath(filepath.Join(nvxHome, "versions", "python", resolvedVer), cmd)
	if path == "" {
		return ""
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}

// --- install / uninstall -------------------------------------------------------

func (p PythonProvider) Install(version string, nvxHome string) error {
	rel, asset, err := p.resolveInstallAsset(version, nvxHome)
	if err != nil {
		return err
	}
	resolvedVer := asset.Version

	if isPythonVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Python %s is already installed.", resolvedVer)
		return nil
	}

	relLock, err := acquireRuntimeInstallLock(nvxHome, "python", resolvedVer)
	if err != nil {
		return err
	}
	defer relLock()
	if isPythonVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Python %s is already installed.", resolvedVer)
		return nil
	}

	tempFile := filepath.Join(GetDownloadsDir(), asset.Filename)
	LogInfo("Installing Python %s (%s)", resolvedVer, runtime.GOOS+"/"+runtime.GOARCH)
	LogInfo("URL: %s", asset.URL)

	if err := DownloadFile(asset.URL, tempFile); err != nil {
		return err
	}
	defer os.Remove(tempFile)

	shaURL := fmt.Sprintf("https://github.com/astral-sh/python-build-standalone/releases/download/%s/SHA256SUMS", rel.Tag)
	if err := VerifyChecksumFromShasums(shaURL, tempFile, asset.Filename); err != nil {
		return err
	}

	extractDir := filepath.Join(GetDownloadsDir(), fmt.Sprintf("python-extract-%d", os.Getpid()))
	_ = os.RemoveAll(extractDir)
	defer os.RemoveAll(extractDir)
	// install_only archives wrap everything in a top-level python/ folder, which
	// ExtractTarGz strips, leaving bin/ (unix) or python.exe + Scripts/ (windows).
	if err := ExtractTarGz(tempFile, extractDir); err != nil {
		return err
	}
	if info, err := os.Stat(pythonCommandPath(extractDir, "python")); err != nil || info.IsDir() {
		return fmt.Errorf("extracted Python archive did not contain the expected interpreter")
	}

	destDir := filepath.Join(nvxHome, "versions", "python", resolvedVer)
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("remove incomplete install directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0700); err != nil {
		return err
	}
	if err := os.Rename(extractDir, destDir); err != nil {
		return fmt.Errorf("activate installed Python version: %w", err)
	}

	LogSuccess("Python %s installed successfully to: %s", resolvedVer, destDir)
	return nil
}

func (p PythonProvider) Uninstall(version string, nvxHome string) error {
	resolvedVer, err := resolveLocalVersion(p, version, nvxHome)
	if err != nil {
		return err
	}
	if getGlobalDefaultVersionFor(nvxHome, "python") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Python %s because it is the global default; set a different default first", resolvedVer)
	}
	if getActiveShellVersionFor(nvxHome, "python") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Python %s because it is active in this shell", resolvedVer)
	}

	destDir := filepath.Join(nvxHome, "versions", "python", resolvedVer)
	LogInfo("Uninstalling Python %s...", resolvedVer)
	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	LogSuccess("Python %s uninstalled successfully.", resolvedVer)
	return nil
}
