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

const nvxReleasesLatest = "https://api.github.com/repos/fstubner/nvx/releases/latest"

func githubGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nvx-self-update")
	req.Header.Set("Accept", "application/vnd.github+json")
	return (&http.Client{Timeout: 15 * time.Second}).Do(req)
}

func latestNvxTag() (string, error) {
	resp, err := githubGet(nvxReleasesLatest)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release lookup failed: HTTP %s", resp.Status)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", fmt.Errorf("no tag_name in latest release")
	}
	return r.TagName, nil
}

// nvxAssetName is the release asset for this OS/arch, matching build-release.ps1
// and the install scripts.
func nvxAssetName() string {
	if runtime.GOOS == "windows" {
		return "nvx.exe"
	}
	return fmt.Sprintf("nvx-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// isNewerTag reports whether remote is newer than the running build. A "dev"
// build (no injected version) always counts as upgradable.
func isNewerTag(remote string) bool {
	cur := nvxVersion()
	if strings.HasPrefix(cur, "dev") {
		return true
	}
	return CompareVersions(remote, cur) > 0
}

// runUpgrade downloads the latest release binary, verifies its SHA-256, and
// atomically replaces the running executable. Fail-closed: any verification
// failure aborts without touching the installed binary.
func runUpgrade(checkOnly bool) int {
	tag, err := latestNvxTag()
	if err != nil {
		LogError("Could not check for updates: %v", err)
		return 1
	}
	if !isNewerTag(tag) {
		LogSuccess("nvx is up to date (%s).", nvxVersion())
		return 0
	}
	LogInfo("A newer nvx is available: %s (current: %s)", tag, nvxVersion())
	if checkOnly {
		LogInfo("Run 'nvx upgrade' to install it.")
		return 0
	}

	self, err := os.Executable()
	if err != nil {
		LogError("Cannot locate current executable: %v", err)
		return 1
	}
	// Resolve symlinks so we replace the real binary, but keep the original path
	// if resolution fails (EvalSymlinks returns "" on error, which would break
	// the replacement).
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil && resolved != "" {
		self = resolved
	}

	asset := nvxAssetName()
	base := fmt.Sprintf("https://github.com/fstubner/nvx/releases/download/%s", tag)
	binURL := base + "/" + asset
	tmp := filepath.Join(GetDownloadsDir(), asset+".new")

	LogInfo("Downloading %s ...", binURL)
	if err := DownloadFile(binURL, tmp); err != nil {
		LogError("Download failed: %v", err)
		return 1
	}
	defer os.Remove(tmp)

	// Verify checksum (fail-closed — never install an unverified binary).
	if err := verifyNvxChecksum(base, asset, tmp); err != nil {
		LogError("Checksum verification failed: %v", err)
		return 1
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		LogError("chmod failed: %v", err)
		return 1
	}

	if err := replaceExecutable(self, tmp); err != nil {
		LogError("Failed to replace executable: %v", err)
		return 1
	}
	LogSuccess("nvx upgraded to %s.", tag)
	return 0
}

// verifyNvxChecksum downloads <asset>.sha256 and checks it against the file.
func verifyNvxChecksum(base, asset, filePath string) error {
	resp, err := githubGet(base + "/" + asset + ".sha256")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksum file unavailable: HTTP %s", resp.Status)
	}
	// Read the whole (small) body; a single Read can truncate the hex on a
	// chunked response and cause a spurious mismatch.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return fmt.Errorf("empty checksum file")
	}
	want := strings.ToLower(fields[0])
	got, err := ComputeSHA256(filePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	LogSuccess("Release checksum verified.")
	return nil
}

// replaceExecutable swaps the running binary with newPath. On Windows the
// running image can't be overwritten, so the old binary is moved aside first.
func replaceExecutable(self, newPath string) error {
	if runtime.GOOS == "windows" {
		old := self + ".old"
		_ = os.Remove(old)
		if err := os.Rename(self, old); err != nil {
			return err
		}
		if err := copyFileMode(newPath, self, 0o755); err != nil {
			// best-effort rollback
			_ = os.Rename(old, self)
			return err
		}
		return nil
	}
	// Unix: rename within the same dir is atomic and works on a running binary.
	staged := self + ".new"
	if err := copyFileMode(newPath, staged, 0o755); err != nil {
		return err
	}
	return os.Rename(staged, self)
}

// --- daily update notification (non-blocking, cached) ---

type updateCheckCache struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
}

// maybeNotifyUpdate prints a one-line hint if a newer release exists, checking
// the network at most once per day and caching the result. Best-effort: any
// error is silently ignored so it never disrupts a command.
func maybeNotifyUpdate(nvxHome string) {
	cachePath := filepath.Join(nvxHome, "update-check.json")
	var cache updateCheckCache
	if data, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	if time.Since(cache.LastCheck) < 24*time.Hour {
		if cache.Latest != "" && isNewerTag(cache.Latest) {
			LogInfo("Update available: %s (run 'nvx upgrade'). Current: %s", cache.Latest, nvxVersion())
		}
		return
	}
	tag, err := latestNvxTag()
	if err != nil {
		return
	}
	cache = updateCheckCache{LastCheck: time.Now(), Latest: tag}
	if data, err := json.Marshal(cache); err == nil {
		_ = os.MkdirAll(nvxHome, 0o755)
		_ = os.WriteFile(cachePath, data, 0o644)
	}
	if isNewerTag(tag) {
		LogInfo("Update available: %s (run 'nvx upgrade'). Current: %s", tag, nvxVersion())
	}
}
