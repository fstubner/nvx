package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// `nvx import` scans nvm, fnm and volta for the Node versions they have, and
// installs those versions into nvx's own store.
//
// It reads version NUMBERS, not files: each one is downloaded from nodejs.org
// and checksum-verified like any other `nvx install`. Nothing is copied out of
// the other manager and nothing about it is changed. The messages used to say
// "Importing Node.js v22.11.0 from nvm-windows", which reads as a copy from a
// source nvx never opened.

// normalizeImportSource makes the name comparable: case and surrounding spaces
// must not turn a source nvx supports into one it rejects, now that rejecting is
// an error rather than an empty result.
func normalizeImportSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return "all"
	}
	return source
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// importSources are the version managers nvx can read versions from. Named in
// one place so an unknown one can be rejected rather than quietly matching
// nothing.
var importSources = []string{"nvm", "fnm", "volta"}

// runImport adopts the Node versions another version manager has, and returns an
// exit code.
//
// It returns one at all because `nvx import bogus` used to print "No previous
// Node.js installations found for source 'bogus'." and exit 0 -- a typo read as
// success, and as evidence that the named manager had nothing, which nvx had not
// checked. Every other unknown-argument path in the CLI exits 1.
func runImport(source string, nvxHome string) int {
	source = normalizeImportSource(source)
	if source != "all" && !containsFold(importSources, source) {
		LogError("Unknown import source %q. Known sources: %s, or 'all'.", source, strings.Join(importSources, ", "))
		return 1
	}

	discovered := make(map[string]string) // version -> source

	if source == "all" || source == "nvm" {
		importNvm(discovered)
	}
	if source == "all" || source == "fnm" {
		importFnm(discovered)
	}
	if source == "all" || source == "volta" {
		importVolta(discovered)
	}

	if len(discovered) == 0 {
		LogInfo("No previous Node.js installations found for source '%s'.", source)
		return 0
	}

	// Said once, up front, because the per-version lines below cannot help
	// sounding like a copy and this is the only place to correct that. nvx reads
	// which VERSIONS another manager has and installs those versions itself,
	// downloading and checksum-verifying each from nodejs.org. Nothing is copied
	// out of the other manager, and nothing about it is changed.
	LogInfo("Reading which Node.js versions your other version managers have; nvx downloads its own verified copy of each.")

	// Ask before downloading. `nvx import` with no arguments went straight to
	// installing every version it found across nvm, fnm and volta -- on an
	// acceptance pass that was three full Node runtimes, hundreds of megabytes,
	// from a command whose name suggests reading rather than fetching. Naming them
	// first is also the only chance to notice it found something unexpected.
	//
	// -y / NVX_YES approves it, as everywhere else. This is a convenience prompt,
	// not a trust-boundary one: it downloads checksum-verified runtimes from the
	// same place `nvx install` does, so it is not in the class that deliberately
	// refuses -y.
	pending := make([]string, 0, len(discovered))
	for ver, src := range discovered {
		pending = append(pending, fmt.Sprintf("v%s (found in %s)", strings.TrimPrefix(strings.ToLower(ver), "v"), src))
	}
	sort.Strings(pending)
	for _, p := range pending {
		LogInfo("  %s", p)
	}
	if !PromptYesNo(fmt.Sprintf("Download and install %d Node.js version(s) into nvx?", len(pending))) {
		LogInfo("Nothing was installed.")
		return 0
	}

	provider := Providers["node"]
	installedCount := 0
	alreadyInstalled := 0

	for ver, src := range discovered {
		cleanVer := strings.TrimPrefix(strings.ToLower(ver), "v")
		cleanVer = strings.TrimPrefix(cleanVer, "node-")
		if cleanVer == "" {
			continue
		}
		if err := safeVersionComponent("v" + cleanVer); err != nil {
			LogWarn("Skipping unusable version %q discovered in %s: %v", ver, src, err)
			continue
		}

		targetDir := filepath.Join(nvxHome, "versions", provider.Name(), "v"+cleanVer)
		if _, err := os.Stat(targetDir); err == nil {
			alreadyInstalled++
			LogInfo("Node.js v%s (from %s) is already installed in nvx.", cleanVer, src)
			continue
		}

		LogInfo("Installing Node.js v%s, found in %s...", cleanVer, src)
		err := provider.Install(cleanVer, nvxHome)
		if err != nil {
			LogError("Failed to import Node.js v%s: %v", cleanVer, err)
		} else {
			LogSuccess("Installed Node.js v%s (%s has it too).", cleanVer, src)
			installedCount++
		}
	}

	LogSuccess("Import complete: %d installed, %d already present.", installedCount, alreadyInstalled)
	return 0
}

func importNvm(discovered map[string]string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// 1. Standard Unix nvm: ~/.nvm/versions/node/
	nvmUnixDir := filepath.Join(home, ".nvm", "versions", "node")
	scanVersionDirs(nvmUnixDir, "nvm", discovered)

	// 2. Windows nvm-windows: %NVM_HOME% or %APPDATA%\nvm
	if runtime.GOOS == "windows" {
		nvmWin := os.Getenv("NVM_HOME")
		if nvmWin == "" {
			if appData := os.Getenv("APPDATA"); appData != "" {
				nvmWin = filepath.Join(appData, "nvm")
			}
		}
		if nvmWin != "" {
			scanVersionDirs(nvmWin, "nvm-windows", discovered)
		}
	}
}

func importFnm(discovered map[string]string) {
	for _, p := range fnmInstallationDirs() {
		scanVersionDirs(p, "fnm", discovered)
	}
}

// fnmInstallationDirs lists where fnm keeps installed Node versions: a
// node-versions directory under its base directory, one vX.Y.Z per version.
//
// This scanned ~/.fnm, ~/.local/share/fnm/current and %LOCALAPPDATA%nm
// directly, so on fnm's standard layout it read the base directory's own
// entries (node-versions, aliases) and found no version at all. The bases below
// are fnm's own, from src/directories.rs and src/config.rs at 86adc96: FNM_DIR
// when set, then the platform data directory's fnm (XDG_DATA_HOME or
// ~/.local/share on Linux, %APPDATA% on Windows), then the legacy ~/.fnm, and on
// macOS ~/Library/Application Support/fnm. %LOCALAPPDATA%nm is kept because
// the scan looked there before; a directory that does not exist costs nothing.
func fnmInstallationDirs() []string {
	var bases []string
	if dir := os.Getenv("FNM_DIR"); dir != "" {
		bases = append(bases, dir)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			bases = append(bases, filepath.Join(xdg, "fnm"))
		}
		bases = append(bases,
			filepath.Join(home, ".local", "share", "fnm"),
			filepath.Join(home, ".fnm"),
		)
		if runtime.GOOS == "darwin" {
			bases = append(bases, filepath.Join(home, "Library", "Application Support", "fnm"))
		}
	}
	if runtime.GOOS == "windows" {
		for _, env := range []string{"APPDATA", "LOCALAPPDATA"} {
			if dir := os.Getenv(env); dir != "" {
				bases = append(bases, filepath.Join(dir, "fnm"))
			}
		}
	}
	dirs := make([]string, 0, len(bases))
	for _, b := range bases {
		dirs = append(dirs, filepath.Join(b, "node-versions"))
	}
	return dirs
}

func importVolta(discovered map[string]string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	voltaDir := filepath.Join(home, ".volta", "tools", "image", "node")
	scanVersionDirs(voltaDir, "volta", discovered)
}

func scanVersionDirs(dir string, sourceName string, discovered map[string]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Filter valid version directory names (e.g. v20.11.0, 20.11.0, node-v18.16.0)
		clean := strings.ToLower(name)
		clean = strings.TrimPrefix(clean, "node-")
		clean = strings.TrimPrefix(clean, "v")

		if len(clean) > 0 && (clean[0] >= '0' && clean[0] <= '9') {
			if _, exists := discovered[clean]; !exists {
				discovered[clean] = sourceName
			}
		}
	}
}
