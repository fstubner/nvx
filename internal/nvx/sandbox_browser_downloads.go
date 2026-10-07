package nvx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// A browser a contained install downloads goes with the guest home.
//
// puppeteer's postinstall and `playwright install` keep their browsers in a
// cache under the home directory, and inside the sandbox that is the guest
// home, which nvx deletes when the command ends. The install still exits 0, so
// the first sign was puppeteer or Playwright failing to find the browser later.
// Measured 2026-10-07 in a Debian 13 container with storage.googleapis.com
// allowed, the cache in the guest home reached 856 MB during a contained `npm
// install puppeteer`. npm exited 0, nvx said nothing, and no browser was left
// afterwards.

// browserCache is a folder a browser installer writes under the home, and the
// command that installs the browser where the tool looks for it.
type browserCache struct {
	tool string
	// dir is relative to the home and slash-separated. One element may be *,
	// for every folder at that level.
	dir     string
	install string
}

// browserCaches are the folders for goos. puppeteer uses ~/.cache/puppeteer
// everywhere. Playwright uses the platform's cache folder, which the sandbox
// points into the guest home through XDG_CACHE_HOME on Linux. Windows points an
// AppContainer's LOCALAPPDATA at the package's own folder. Measured 2026-10-07,
// a contained node saw it as AppData\Local\Packages\nvx.sandbox.<id>\AC under
// the guest home.
func browserCaches(goos string) []browserCache {
	playwright := ".cache/ms-playwright"
	switch goos {
	case "windows":
		playwright = "AppData/Local/Packages/*/AC/ms-playwright"
	case "darwin":
		playwright = "Library/Caches/ms-playwright"
	}
	return []browserCache{
		{tool: "puppeteer", dir: ".cache/puppeteer", install: "npx puppeteer browsers install chrome"},
		{tool: "Playwright", dir: playwright, install: "npx playwright install"},
	}
}

// dirsUnder returns the folders under home that c.dir names. Not
// filepath.Glob, which would read a * or [ in home itself as a pattern.
func (c browserCache) dirsUnder(home string) []string {
	parent, rest, wild := strings.Cut(c.dir, "/*/")
	if !wild {
		return []string{filepath.Join(home, filepath.FromSlash(c.dir))}
	}
	parentDir := filepath.Join(home, filepath.FromSlash(parent))
	entries, _ := os.ReadDir(parentDir)
	var dirs []string
	for _, e := range entries {
		dirs = append(dirs, filepath.Join(parentDir, e.Name(), filepath.FromSlash(rest)))
	}
	return dirs
}

// browserDownloadFloor is how much a cache folder must hold to count as a
// download. A blocked download leaves folders and small records behind, such as
// Playwright's note of the project in .links. A browser is hundreds of MB.
const browserDownloadFloor = 1 << 20

// warnLostBrowserDownloads says when a browser landed in a guest home that nvx
// is about to delete, and how to install it where the tool looks for it.
func warnLostBrowserDownloads(guestHome string) {
	for _, c := range browserCaches(runtime.GOOS) {
		for _, dir := range c.dirsUnder(guestHome) {
			if !holdsAtLeast(dir, browserDownloadFloor) {
				continue
			}
			LogWarn("%s downloaded a browser into the sandbox's home, which nvx deletes when the command ends.", c.tool)
			LogInfo("To install it where %s looks for it, run: %s --no-sandbox %s", c.tool, installedNvxHint(), c.install)
			break
		}
	}
}

// errEnough stops the walk in holdsAtLeast.
var errEnough = errors.New("enough")

// holdsAtLeast reports whether the regular files under dir add up to at least
// n bytes. It stops as soon as they do, and does not follow links.
func holdsAtLeast(dir string, n int64) bool {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		if total >= n {
			return errEnough
		}
		return nil
	})
	return errors.Is(err, errEnough)
}
