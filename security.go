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

// popularPackagesTTL is when the cached dictionary is refreshed in the
// background. It is NOT when the cache stops being used -- see
// LoadPopularPackages.
const popularPackagesTTL = 7 * 24 * time.Hour

// readPopularPackagesCache returns the cached dictionary, and whether it is
// usable at all. A file that cannot be read or parsed is not usable; an old one
// is.
func readPopularPackagesCache(cachePath string) ([]string, bool) {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil || len(list) == 0 {
		return nil, false
	}
	return list, true
}

// LoadPopularPackages returns the typosquatting checklist, refreshing it in the
// background once the cache is older than popularPackagesTTL.
//
// A stale cache is still used. It used to be discarded the moment it passed
// seven days, falling back to EmbeddedPopularPackages -- 33 names against the
// 2000 on disk -- while the sync that would have replaced it ran in the
// background for NEXT time and the user still saw "Verifying package". A
// typosquat check comparing against 33 names instead of 2000 is a different
// check, and nothing said so.
//
// Measured on the machine this was found on: the cache held 2000 entries, six
// days old, and would have dropped to 33 the following afternoon.
//
// Seven days was never a correctness boundary either. The source dataset
// (npm-high-impact, see popularPackagesURL) is refreshed quarterly, so a
// week-old copy and a fresh one are almost always the same list. The TTL is
// there to keep the copy current, not to decide whether it can be trusted, and
// the embedded list is for having nothing at all rather than for having
// something slightly old.
func LoadPopularPackages(nvxHome string) []string {
	cachePath := filepath.Join(nvxHome, "popular_packages.json")
	cached, usable := readPopularPackagesCache(cachePath)

	fresh := false
	if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < popularPackagesTTL {
		fresh = true
	}
	if usable && fresh {
		return cached
	}

	if !usable {
		// Nothing to fall back on but the embedded list, so it is worth blocking
		// briefly to get the real one. This is the first run, or a corrupt cache.
		if list, err := syncPopularPackages(cachePath); err == nil {
			return list
		}
		return EmbeddedPopularPackages
	}

	// Stale but usable: refresh for next time, and check against what we have
	// rather than against a twentieth of it.
	go func() {
		_, _ = syncPopularPackages(cachePath)
	}()
	return cached
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
	// NextPageToken is set when OSV has more advisories for this query than it
	// returned. Parsed so its presence can be reported; nvx does not follow it,
	// and silently keeping the first page would understate a package that has
	// enough vulnerabilities to need paging.
	NextPageToken string `json:"next_page_token,omitempty"`
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

	// A short result list is an error, not an absence of vulnerabilities.
	//
	// This was `if i < len(batchResp.Results)`, so any query the API did not answer
	// was skipped and the package came back with nothing against it -- and the
	// caller prints "Vulnerability scan clean. No active CVEs found." for an empty
	// map. "Could not check" and "checked, nothing found" are the two answers that
	// must never be confused in a security tool, and this rendered the first as the
	// second.
	//
	// Returning an error is not a new failure mode: the caller already handles one
	// by asking "Proceed without CVE checks?" and aborting the install if the user
	// declines. That path existed and this simply reaches it.
	if len(batchResp.Results) != len(payload.Queries) {
		return nil, fmt.Errorf("OSV returned %d results for %d queries; some packages were not checked",
			len(batchResp.Results), len(payload.Queries))
	}

	results := make(map[string][]OSVVuln)
	for i, query := range payload.Queries {
		res := batchResp.Results[i]
		// A paged answer means this package has more advisories than one response
		// carries. nvx does not follow the pages, so say so rather than reporting
		// the first page as the whole answer -- the package is already known to
		// have vulnerabilities at that point, so the honest outcome is an error the
		// user is asked about.
		if res.NextPageToken != "" {
			return nil, fmt.Errorf("OSV paginated its answer for %s@%s; nvx cannot yet read past the first page, so the result would be incomplete",
				query.Package.Name, query.Version)
		}
		if len(res.Vulns) > 0 {
			key := fmt.Sprintf("%s@%s", query.Package.Name, query.Version)
			results[key] = fillVulnSummaries(client, res.Vulns)
		}
	}
	return results, nil
}

// fillVulnSummaries fetches the one-line description for each advisory.
//
// /v1/querybatch answers with ids and modification times only -- no summary --
// so every advisory printed as "GHSA-xxxx-xxxx-xxxx: " with nothing after the
// colon. A wall of bare identifiers is not something a developer can act on, and
// it reads like the tool is broken. Reported from real use 2026-08-20.
//
// Best-effort by construction: a failed lookup leaves the summary empty, which is
// exactly today's output, so this can only improve the message and never block an
// install on a second network call. Bounded to a handful of lookups because the
// list is what one install matched, not the whole database.
func fillVulnSummaries(client *http.Client, vulns []OSVVuln) []OSVVuln {
	const maxDetailLookups = 10
	for i := range vulns {
		if i >= maxDetailLookups || vulns[i].Summary != "" || vulns[i].ID == "" {
			continue
		}
		if s := fetchVulnSummary(client, vulns[i].ID); s != "" {
			vulns[i].Summary = s
		}
	}
	return vulns
}

func fetchVulnSummary(client *http.Client, id string) string {
	// url.PathEscape on the id: it comes from a network response, and it is
	// interpolated into a request path.
	resp, err := client.Get("https://api.osv.dev/v1/vulns/" + url.PathEscape(id))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var detail struct {
		Summary string `json:"summary"`
		Details string `json:"details"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&detail); err != nil {
		return ""
	}
	if detail.Summary != "" {
		return detail.Summary
	}
	// Some advisories carry only the long form. One line of it beats nothing.
	if detail.Details != "" {
		first := strings.TrimSpace(strings.SplitN(detail.Details, "\n", 2)[0])
		if len(first) > 160 {
			first = first[:157] + "..."
		}
		return first
	}
	return ""
}

// NpmRegistryMetadata represents minimal package info from registry
type NpmRegistryMetadata struct {
	// Every dist-tag, not just latest. A registry serves `next`, `beta`,
	// `canary` and whatever else a publisher defines, and resolving only
	// `latest` left the rest to fall through as literal strings -- see
	// resolveVersionQuery.
	DistTags map[string]string            `json:"dist-tags"`
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

	resolvedVer, err := resolveVersionQuery(versionQuery, meta)
	if err != nil {
		return "", time.Time{}, false, err
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

// resolveVersionQuery turns whatever followed the "@" into a concrete version
// present in the registry metadata, or reports that it could not.
//
// It used to be `resolvedVer := versionQuery`, replaced by the latest tag only
// when the query was empty. So `npm install npm@latest` carried the literal
// string "latest" forward, and every lookup keyed on it missed:
//
//   - meta.Versions["latest"] misses, so hasInstallScripts stayed false and the
//     install-script prompt never appeared;
//   - meta.Time["latest"] misses, so the release-age check compared against a
//     zero time and never fired;
//   - the OSV query carried version "latest", so the vulnerability scan was not
//     about the version being installed.
//
// Two security checks silently did nothing for the single most common way to
// install a package, and the third reported against a version that does not
// exist. Reported from real use 2026-08-20 as a scary, detail-free advisory list
// for `npm@latest`; the noisy output was the symptom, this is the cause.
//
// Anything that is neither a dist-tag nor an exact published version -- a semver
// range like ^4.17.0, or a typo -- is an error rather than a pass-through. The
// caller turns that into "could not verify registry metadata ... proceed?", which
// is honest: nvx cannot check a version it cannot name. Silently continuing with
// every check disabled is what this replaces.
func resolveVersionQuery(versionQuery string, meta NpmRegistryMetadata) (string, error) {
	q := strings.TrimSpace(versionQuery)

	if q == "" {
		if latest := meta.DistTags["latest"]; latest != "" {
			return latest, nil
		}
		return "", fmt.Errorf("could not determine latest version")
	}

	// An exact published version needs no resolution. Checked before the tag map
	// so a version that happens to share a tag's name cannot be redirected.
	if _, ok := meta.Versions[q]; ok {
		return q, nil
	}

	if tagged, ok := meta.DistTags[q]; ok && tagged != "" {
		if _, known := meta.Versions[tagged]; known {
			return tagged, nil
		}
		// The registry named a version for this tag that it did not publish
		// details for. Better to say so than to check nothing.
		return "", fmt.Errorf("dist-tag %q points at version %q, which the registry did not describe", q, tagged)
	}

	return "", fmt.Errorf("%q is not an exact version or a dist-tag; nvx cannot check a version it cannot resolve", q)
}
