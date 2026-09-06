package main

import (
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
	case q == "lts", q == "lts/*":
		return true, ""
	case strings.HasPrefix(q, "lts/"):
		return true, strings.TrimPrefix(q, "lts/")
	}
	return false, ""
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
