package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EmbeddedPopularPackages serves as a fallback typosquatting dictionary if the dynamic sync fails or is offline.
var EmbeddedPopularPackages = []string{
	"lodash", "react", "react-dom", "express", "chalk", "commander", "tslib", "axios",
	"moment", "uuid", "dotenv", "webpack", "typescript", "eslint", "jest", "prettier",
	"debug", "request", "prop-types", "semver", "fs-extra", "bluebird", "async", "redis",
	"minimist", "mkdirp", "glob", "rimraf", "inquirer", "rxjs", "postcss", "vite", "next",
}

// Policy types and LoadPolicy live in policy.go.

// IsBlocked checks if a package name matches any blocked package patterns
func (p Policy) IsBlocked(pkgName string) bool {
	pkgName = strings.ToLower(strings.TrimSpace(pkgName))
	for _, pattern := range p.BlockedPackages {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if pattern == pkgName {
			return true
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(pkgName, prefix) {
				return true
			}
		}
	}
	return false
}

// LevenshteinDistance calculates the edit distance between two strings
func LevenshteinDistance(s, t string) int {
	d := make([][]int, len(s)+1)
	for i := range d {
		d[i] = make([]int, len(t)+1)
	}
	for i := range d {
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for j := 1; j <= len(t); j++ {
		for i := 1; i <= len(s); i++ {
			if s[i-1] == t[j-1] {
				d[i][j] = d[i-1][j-1]
			} else {
				d[i][j] = min(
					d[i-1][j]+1,
					min(
						d[i][j-1]+1,
						d[i-1][j-1]+1,
					),
				)
			}
		}
	}
	return d[len(s)][len(t)]
}

// LoadPopularPackages returns the typosquatting checklist, syncing from a remote source if outdated
func LoadPopularPackages(nvxHome string) []string {
	cachePath := filepath.Join(nvxHome, "popular_packages.json")

	// Check if local cache is fresh (less than 7 days old)
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < 7*24*time.Hour {
		data, err := os.ReadFile(cachePath)
		if err == nil {
			var list []string
			if err := json.Unmarshal(data, &list); err == nil && len(list) > 0 {
				return list
			}
		}
	}

	// Dynamic update in the background (or fallback synchronously if file missing)
	// We will attempt to fetch a curated list of top 100 packages.
	// For reliable fallback, if it doesn't exist, we download it.
	syncNeeded := false
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		syncNeeded = true
	}

	if syncNeeded {
		// Sync synchronously on first run to populate the cache
		list, err := syncPopularPackages(cachePath)
		if err == nil {
			return list
		}
	} else {
		// Sync asynchronously in the background if we have an old cache, so we don't block the developer
		go func() {
			_, _ = syncPopularPackages(cachePath)
		}()
	}

	return EmbeddedPopularPackages
}

// popularPackagesURL points at the npm-high-impact dataset (top npm packages
// by downloads and dependents, refreshed quarterly), served from the npm
// registry via the jsDelivr CDN. The file is an ES module exporting an array
// of package name string literals.
const popularPackagesURL = "https://cdn.jsdelivr.net/npm/npm-high-impact/lib/top.js"

// maxPopularPackages caps the typosquatting dictionary size to keep the
// Levenshtein comparison per install fast.
const maxPopularPackages = 2000

func syncPopularPackages(cachePath string) ([]string, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(popularPackagesURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	list := extractQuotedStrings(string(body), maxPopularPackages)
	if len(list) == 0 {
		return nil, fmt.Errorf("parsed list is empty")
	}

	// Write cache file
	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
		return list, fmt.Errorf("create popular-package cache directory: %w", err)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return list, fmt.Errorf("encode popular-package cache: %w", err)
	}
	if err := os.WriteFile(cachePath, data, 0600); err != nil {
		return list, fmt.Errorf("write popular-package cache: %w", err)
	}
	return list, nil
}

// extractQuotedStrings pulls single- or double-quoted string literals out of a
// JS module source. Only strings that look like npm package names are kept,
// which makes the parser resilient to formatting changes in the upstream file.
func extractQuotedStrings(src string, limit int) []string {
	var list []string
	i := 0
	for i < len(src) && len(list) < limit {
		c := src[i]
		if c == '\'' || c == '"' {
			end := strings.IndexByte(src[i+1:], c)
			if end == -1 {
				break
			}
			candidate := src[i+1 : i+1+end]
			if isValidPackageName(candidate) {
				list = append(list, candidate)
			}
			i += end + 2
		} else {
			i++
		}
	}
	return list
}

// isValidPackageName reports whether s is a plausible npm package name.
func isValidPackageName(s string) bool {
	if s == "" || len(s) > 214 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '@' || r == '/' {
			continue
		}
		return false
	}
	return s[0] != '.' && s[0] != '_'
}

// CheckTyposquatting returns the name of a popular package if the query is suspiciously close (edit distance 1 or 2)
func CheckTyposquatting(pkgName string, popularList []string) string {
	return CheckTyposquattingAuthority(pkgName, popularList, 2)
}

// weeklyDownloads is the seam the typosquat check goes through, so a test can
// exercise the authority comparison without reaching api.npmjs.org. Following the
// resolveNpmPackageDetailsForVerify pattern below.
//
// Without it, CheckTyposquattingAuthority made two live HTTPS requests per
// near-match name, which meant the test suite silently took different branches
// depending on whether the machine had network: the download-threshold logic ran
// only when a request happened to succeed, and the offline fallback ran otherwise.
// Neither was ever asserted deliberately.
var weeklyDownloads = GetWeeklyDownloads

// CheckTyposquattingAuthority dynamically compares weekly downloads to detect typosquatting threats
func CheckTyposquattingAuthority(pkgName string, popularList []string, maxDist int) string {
	pkgName = strings.ToLower(strings.TrimSpace(pkgName))
	for _, popular := range popularList {
		if pkgName == popular {
			return "" // exact match is always authoritative
		}
	}

	for _, popular := range popularList {
		dist := LevenshteinDistance(pkgName, popular)
		if dist >= 1 && dist <= maxDist {
			// Query downloads to verify authority
			pkgDownloads, errPkg := weeklyDownloads(pkgName)
			suspectDownloads, errSus := weeklyDownloads(popular)

			if errPkg == nil && errSus == nil {
				// Authority threshold: if the target is high-popularity (>50k/week)
				// AND it has more than 100x the weekly downloads of the installed package, it's a typosquat
				if suspectDownloads > 50000 && suspectDownloads > 100*pkgDownloads {
					return popular
				}
			} else {
				// Fallback if offline/API fails: flag on name similarity
				return popular
			}
		}
	}
	return ""
}

// NpmDownloadsResponse represents the structure returned by api.npmjs.org
type NpmDownloadsResponse struct {
	Downloads int    `json:"downloads"`
	Package   string `json:"package"`
}

// EscapeScopedPackage makes a package name safe to interpolate into a registry
// URL path.
//
// It used to hand-roll the escaping: replace the single "/" in a scoped name with
// %2F and return everything else untouched. That covers the case it was written
// for and nothing else, so a name containing "../", a space, "?" or "#" went into
// the URL path verbatim. A name is not always something the user typed -- it can
// come from a project policy file or a package.json in a cloned repository -- and
// the responses feed the typosquat and release-age gates, so steering a lookup at
// a different path than the one being installed is a way to influence what those
// gates see.
//
// url.PathEscape produces byte-identical output for real package names
// (@types/node -> @types%2Fnode, lodash -> lodash) and neutralises the rest.
func EscapeScopedPackage(pkg string) string {
	return url.PathEscape(pkg)
}

// GetWeeklyDownloads queries the public npm downloads point API
func GetWeeklyDownloads(pkgName string) (int, error) {
	escapedPkg := EscapeScopedPackage(pkgName)
	url := fmt.Sprintf("https://api.npmjs.org/downloads/point/last-week/%s", escapedPkg)

	client := &http.Client{Timeout: 5 * time.Second}
	// #nosec G704 -- same as ResolveNpmPackageDetails: hardcoded host, path segment
	// escaped via url.PathEscape.
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %s", resp.Status)
	}

	var data NpmDownloadsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}
	return data.Downloads, nil
}

// OSVQueryBatch structure for batch vulnerability scanning
type OSVQueryBatch struct {
	Queries []OSVQuery `json:"queries"`
}

type OSVQuery struct {
	Package OSVPackage `json:"package"`
	Version string     `json:"version"`
}

type OSVPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

type OSVResponseBatch struct {
	Results []OSVResult `json:"results"`
}

type OSVResult struct {
	Vulns []OSVVuln `json:"vulns"`
}

type OSVVuln struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// ScanVulnerabilitiesBatch queries the OSV API for multiple packages in a single batch request
func ScanVulnerabilitiesBatch(packages []OSVQuery) (map[string][]OSVVuln, error) {
	if len(packages) == 0 {
		return nil, nil
	}

	payload := OSVQueryBatch{Queries: packages}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post("https://api.osv.dev/v1/querybatch", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("OSV API connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OSV API returned HTTP %s", resp.Status)
	}

	var batchResp OSVResponseBatch
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		return nil, err
	}

	results := make(map[string][]OSVVuln)
	for i, query := range payload.Queries {
		if i < len(batchResp.Results) {
			vulns := batchResp.Results[i].Vulns
			if len(vulns) > 0 {
				key := fmt.Sprintf("%s@%s", query.Package.Name, query.Version)
				results[key] = vulns
			}
		}
	}
	return results, nil
}

// NpmRegistryMetadata represents minimal package info from registry
type NpmRegistryMetadata struct {
	DistTags struct {
		Latest string `json:"latest"`
	} `json:"dist-tags"`
	Time     map[string]string            `json:"time"`
	Versions map[string]NpmVersionDetails `json:"versions"`
}

type NpmVersionDetails struct {
	Scripts map[string]string `json:"scripts"`
}

var resolveNpmPackageDetailsForVerify = ResolveNpmPackageDetails
var scanVulnerabilitiesBatchForVerify = ScanVulnerabilitiesBatch

// ResolveNpmPackageDetails queries npm registry for latest version, publish age, and installation script status
func ResolveNpmPackageDetails(pkgName, versionQuery string) (version string, publishTime time.Time, hasScripts bool, err error) {
	client := &http.Client{Timeout: 8 * time.Second}
	// #nosec G704 -- the host is a hardcoded literal, so this cannot be pointed at
	// another server; only the path segment varies, and EscapeScopedPackage runs it
	// through url.PathEscape first. gosec's taint analysis does not model the
	// hardcoded-host case.
	resp, err := client.Get(fmt.Sprintf("https://registry.npmjs.org/%s", EscapeScopedPackage(pkgName)))
	if err != nil {
		return "", time.Time{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, false, fmt.Errorf("registry returned HTTP %s", resp.Status)
	}

	var meta NpmRegistryMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", time.Time{}, false, err
	}

	resolvedVer := versionQuery
	if resolvedVer == "" {
		resolvedVer = meta.DistTags.Latest
	}

	if resolvedVer == "" {
		return "", time.Time{}, false, fmt.Errorf("could not determine latest version")
	}

	// Check for installation scripts
	hasInstallScripts := false
	if verDetails, ok := meta.Versions[resolvedVer]; ok {
		if verDetails.Scripts != nil {
			for _, hook := range []string{"preinstall", "install", "postinstall"} {
				if _, ok := verDetails.Scripts[hook]; ok {
					hasInstallScripts = true
					break
				}
			}
		}
	}

	pubStr, ok := meta.Time[resolvedVer]
	if !ok {
		return resolvedVer, time.Time{}, hasInstallScripts, nil
	}

	pubTime, err := time.Parse(time.RFC3339, pubStr)
	if err != nil {
		return resolvedVer, time.Time{}, hasInstallScripts, nil
	}

	return resolvedVer, pubTime, hasInstallScripts, nil
}

// publishAgeShouldWarn reports whether pubTime is younger than the configured window.
func publishAgeShouldWarn(pubTime time.Time, minAgeHours int, now time.Time) bool {
	if pubTime.IsZero() || minAgeHours <= 0 {
		return false
	}
	return now.Sub(pubTime) < time.Duration(minAgeHours)*time.Hour
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
