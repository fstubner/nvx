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

// GoProvider implements RuntimeProvider for the Go toolchain — the first
// non-JavaScript runtime, proving the interface generalizes. Go differs from the
// JS runtimes in several ways the interface accommodates: its own go1.X.Y
// version scheme (mapped to nvx's internal vX.Y.Z), a bin/ layout on every
// platform (including Windows), a GOROOT activation variable exposed via
// SessionEnv, and a JSON download index carrying per-file SHA-256 (so no
// separate checksum file is fetched).
type GoProvider struct{}

func (g GoProvider) Name() string { return "go" }

func (g GoProvider) ShimCommands() []string { return []string{"go", "gofmt"} }

func (g GoProvider) SandboxImage(version string) string {
	return runtimeDockerImage("golang", version)
}

// SessionEnv points GOROOT at the active version. Modern go binaries locate
// GOROOT relative to themselves, but setting it explicitly keeps older tooling
// correct — and exercises the runtime-specific session-env hook.
func (g GoProvider) SessionEnv(versionDir string) map[string]string {
	return map[string]string{"GOROOT": versionDir}
}

func (g GoProvider) DefaultNetworkAllow() []string {
	return []string{
		"proxy.golang.org:443",
		"sum.golang.org:443",
		"go.dev:443",
		"api.osv.dev:443",
	}
}

// --- version discovery / internal <-> upstream mapping -------------------------

// goVersionToInternal maps upstream "go1.23.4" to nvx-internal "v1.23.4" so the
// shared version helpers (which assume a leading v) work unchanged.
func goVersionToInternal(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "go") {
		return "v" + strings.TrimPrefix(v, "go")
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

type goFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
}

type goRelease struct {
	Version string   `json:"version"`
	Stable  bool     `json:"stable"`
	Files   []goFile `json:"files"`
}

type goReleaseCache struct {
	FetchedAt time.Time   `json:"fetched_at"`
	Releases  []goRelease `json:"releases"`
}

func goCachePath(nvxHome string) string {
	return filepath.Join(nvxHome, "cache", "go-releases.json")
}

// fetchGoReleases returns the go.dev release index (newest-first), cached ~6h.
func fetchGoReleases(nvxHome string) ([]goRelease, error) {
	cachePath := goCachePath(nvxHome)
	if data, err := os.ReadFile(cachePath); err == nil {
		var c goReleaseCache
		if json.Unmarshal(data, &c) == nil && len(c.Releases) > 0 && time.Since(c.FetchedAt) < 6*time.Hour {
			return c.Releases, nil
		}
	}

	releases, err := fetchGoReleasesFromWeb()
	if err != nil {
		if data, rerr := os.ReadFile(cachePath); rerr == nil {
			var c goReleaseCache
			if json.Unmarshal(data, &c) == nil && len(c.Releases) > 0 {
				LogWarn("Using cached Go release list (network fetch failed: %v)", err)
				return c.Releases, nil
			}
		}
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err == nil {
		if data, mErr := json.MarshalIndent(goReleaseCache{FetchedAt: time.Now(), Releases: releases}, "", "  "); mErr == nil {
			_ = os.WriteFile(cachePath, data, 0600)
		}
	}
	return releases, nil
}

func fetchGoReleasesFromWeb() ([]goRelease, error) {
	req, err := http.NewRequest("GET", "https://go.dev/dl/?mode=json&include=all", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nvx")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Go releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch Go releases: HTTP %s", resp.Status)
	}

	var releases []goRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to parse Go releases: %w", err)
	}
	return releases, nil
}

// resolveInstallRelease resolves a query to a concrete go.dev release entry.
func (g GoProvider) resolveInstallRelease(query, nvxHome string) (goRelease, error) {
	releases, err := fetchGoReleases(nvxHome)
	if err != nil {
		return goRelease{}, err
	}
	if len(releases) == 0 {
		return goRelease{}, fmt.Errorf("no Go releases available")
	}

	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" || q == "latest" || q == "current" {
		for _, r := range releases {
			if r.Stable {
				return r, nil
			}
		}
		return releases[0], nil
	}

	// Build internal-version list preserving order (newest first).
	internal := make([]string, 0, len(releases))
	byInternal := map[string]goRelease{}
	for _, r := range releases {
		iv := goVersionToInternal(r.Version)
		internal = append(internal, iv)
		if _, ok := byInternal[iv]; !ok {
			byInternal[iv] = r
		}
	}

	norm := q
	if !strings.HasPrefix(norm, "v") {
		norm = "v" + norm
	}
	if m := matchVersionPrefix(norm, internal); m != "" {
		return byInternal[m], nil
	}
	return goRelease{}, fmt.Errorf("no Go release matches query %q", query)
}

func (g GoProvider) ResolveVersion(query string) (string, error) {
	r, err := g.resolveInstallRelease(query, GetHomeDir())
	if err != nil {
		return "", err
	}
	return goVersionToInternal(r.Version), nil
}

func (g GoProvider) ListRemote() ([]string, error) {
	releases, err := fetchGoReleases(GetHomeDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range releases {
		out = append(out, goVersionToInternal(r.Version))
	}
	return out, nil
}

func (g GoProvider) ListLocal(nvxHome string) ([]string, error) {
	dir := filepath.Join(nvxHome, "versions", "go")
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

func (g GoProvider) DetectConfig(dir string) (version string, sourceFile string, err error) {
	p, aErr := filepath.Abs(dir)
	if aErr != nil {
		p = dir
	}
	for {
		goVersionFile := filepath.Join(p, ".go-version")
		if info, err := os.Stat(goVersionFile); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(goVersionFile); err == nil {
				return goVersionQuery(strings.TrimSpace(string(content))), goVersionFile, nil
			}
		}

		goMod := filepath.Join(p, "go.mod")
		if info, err := os.Stat(goMod); err == nil && !info.IsDir() {
			if content, err := os.ReadFile(goMod); err == nil {
				if v := goModVersion(string(content)); v != "" {
					return v, goMod, nil
				}
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

func goVersionQuery(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "go")
}

// goModVersion extracts a version query from go.mod, preferring the toolchain
// directive (a concrete toolchain like go1.23.4) over the language go directive.
func goModVersion(content string) string {
	var langVersion string
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "toolchain":
			return goVersionQuery(fields[1])
		case "go":
			if langVersion == "" {
				langVersion = strings.TrimPrefix(fields[1], "go")
			}
		}
	}
	return langVersion
}

// --- binary resolution ---------------------------------------------------------

func goBinaryPath(versionDir, cmd string) string {
	name := cmd
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(versionDir, "bin", name)
}

func isGoVersionInstalled(nvxHome, version string) bool {
	info, err := os.Stat(goBinaryPath(filepath.Join(nvxHome, "versions", "go", version), "go"))
	return err == nil && !info.IsDir()
}

func (g GoProvider) ResolveBinary(cmd, nvxHome, pinnedVer string) string {
	lc := strings.ToLower(cmd)
	if lc != "go" && lc != "gofmt" {
		return ""
	}
	resolvedVer, err := resolveLocalVersion(g, pinnedVer, nvxHome)
	if err != nil {
		return ""
	}
	p := goBinaryPath(filepath.Join(nvxHome, "versions", "go", resolvedVer), lc)
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// --- install / uninstall -------------------------------------------------------

func (g GoProvider) Install(version string, nvxHome string) error {
	release, err := g.resolveInstallRelease(version, nvxHome)
	if err != nil {
		return err
	}
	resolvedVer := goVersionToInternal(release.Version)

	if isGoVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Go %s is already installed.", resolvedVer)
		return nil
	}

	relLock, err := acquireRuntimeInstallLock(nvxHome, "go", resolvedVer)
	if err != nil {
		return err
	}
	defer relLock()
	if isGoVersionInstalled(nvxHome, resolvedVer) {
		LogSuccess("Go %s is already installed.", resolvedVer)
		return nil
	}

	var file *goFile
	for i := range release.Files {
		f := &release.Files[i]
		if f.Kind == "archive" && f.OS == runtime.GOOS && f.Arch == runtime.GOARCH {
			file = f
			break
		}
	}
	if file == nil {
		return fmt.Errorf("Go %s has no archive for %s/%s", release.Version, runtime.GOOS, runtime.GOARCH)
	}

	url := "https://go.dev/dl/" + file.Filename
	tempFile := filepath.Join(GetDownloadsDir(), file.Filename)
	LogInfo("Installing Go %s (%s-%s)", resolvedVer, runtime.GOOS, runtime.GOARCH)
	LogInfo("URL: %s", url)

	if err := DownloadFile(url, tempFile); err != nil {
		return err
	}
	defer os.Remove(tempFile)

	// The go.dev index carries the SHA-256 directly, so verify against it.
	if err := verifyExpectedSHA256(tempFile, file.SHA256); err != nil {
		return err
	}

	extractDir := filepath.Join(GetDownloadsDir(), fmt.Sprintf("go-extract-%d", os.Getpid()))
	_ = os.RemoveAll(extractDir)
	defer os.RemoveAll(extractDir)
	// Archives wrap everything in a top-level go/ folder, which the extractors
	// strip, leaving bin/ src/ pkg/ at extractDir.
	if strings.HasSuffix(file.Filename, ".zip") {
		err = ExtractZip(tempFile, extractDir)
	} else {
		err = ExtractTarGz(tempFile, extractDir)
	}
	if err != nil {
		return err
	}
	if info, err := os.Stat(goBinaryPath(extractDir, "go")); err != nil || info.IsDir() {
		return fmt.Errorf("extracted Go archive did not contain bin/go")
	}

	destDir := filepath.Join(nvxHome, "versions", "go", resolvedVer)
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("remove incomplete install directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0700); err != nil {
		return err
	}
	if err := os.Rename(extractDir, destDir); err != nil {
		return fmt.Errorf("activate installed Go version: %w", err)
	}

	LogSuccess("Go %s installed successfully to: %s", resolvedVer, destDir)
	return nil
}

func (g GoProvider) Uninstall(version string, nvxHome string) error {
	resolvedVer, err := resolveLocalVersion(g, version, nvxHome)
	if err != nil {
		return err
	}
	if getGlobalDefaultVersionFor(nvxHome, "go") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Go %s because it is the global default; set a different default first", resolvedVer)
	}
	if getActiveShellVersionFor(nvxHome, "go") == resolvedVer {
		return fmt.Errorf("refusing to uninstall Go %s because it is active in this shell", resolvedVer)
	}

	destDir := filepath.Join(nvxHome, "versions", "go", resolvedVer)
	LogInfo("Uninstalling Go %s...", resolvedVer)
	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	LogSuccess("Go %s uninstalled successfully.", resolvedVer)
	return nil
}
