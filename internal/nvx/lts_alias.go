package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LTS aliases, as nvm spells them and .nvmrc files in the wild carry them.
//
// `lts` meant "the newest LTS"; `lts/*` means the same and `lts/<codename>`
// names one LTS line. nvx resolved only the first: `lts/*` was read as a
// version expression and rejected as "not a version number", and `nvx install
// lts/hydrogen` reported no matching release. A project whose .nvmrc said
// `lts/*` was told it required "Node.js lts/*" and offered an install command
// that failed the same way.

// parseLTSQuery reports whether query is an LTS alias and, for `lts/<name>`,
// which codename it names. The codename is compared case-insensitively.
func parseLTSQuery(query string) (isLTS bool, codename string) {
	q := strings.ToLower(strings.TrimSpace(query))
	switch {
	// lts-latest is fnm's spelling of the same thing, and .node-version files
	// written for fnm carry it.
	case q == "lts", q == "lts/*", q == "lts-latest":
		return true, ""
	case strings.HasPrefix(q, "lts/"):
		return true, strings.TrimPrefix(q, "lts/")
	}
	return false, ""
}

// ltsOffsetError is what a query like `lts/-1` gets. nvm reads it as "the LTS
// line before the newest", which needs the release list to place the lines, so
// nvx does not follow it. The message used to come out of the codename lookup as
// "install it with 'nvx install lts/-1'", advice that failed the same way.
// Returns nil for any other query.
func ltsOffsetError(query string) error {
	_, codename := parseLTSQuery(query)
	if len(codename) < 2 || codename[0] != '-' {
		return nil
	}
	for _, r := range codename[1:] {
		if r < '0' || r > '9' {
			return nil
		}
	}
	return fmt.Errorf("nvx does not read %q, which counts back from the newest LTS line. Name the version (for example 22) or the line (for example lts/iron)", strings.TrimSpace(query))
}

// isVersionAlias reports whether query names a version by alias (`lts`,
// `lts/*`, `lts/<codename>`, `latest`, and nvm's `node` and `stable`) rather
// than by number, so it can only be answered by looking at what is installed.
func isVersionAlias(query string) bool {
	if isLTS, _ := parseLTSQuery(query); isLTS {
		return true
	}
	return isLatestAlias(query)
}

// isLatestAlias reports whether query asks for the newest version. `node` and
// `stable` are nvm's spellings, which .nvmrc files in the wild carry.
func isLatestAlias(query string) bool {
	switch strings.ToLower(strings.TrimSpace(query)) {
	case "latest", "current", "node", "stable":
		return true
	}
	return false
}

// ltsCodenameOf returns the LTS codename an install recorded for a Node
// version, or "" for a release that is not LTS. The marker's presence is the
// LTS fact; its contents are the codename.
func ltsCodenameOf(nvxHome, version string) string {
	data, err := os.ReadFile(filepath.Join(nvxHome, "versions", "node", version, ltsMarkerName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// filterByLTSCodename keeps the versions whose recorded codename is codename.
func filterByLTSCodename(nvxHome string, versions []string, codename string) []string {
	var out []string
	for _, v := range versions {
		if strings.EqualFold(ltsCodenameOf(nvxHome, v), codename) {
			out = append(out, v)
		}
	}
	return out
}
