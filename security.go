package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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

// Policy defines corporate rules for npm installations
type Policy struct {
	BlockedPackages      []string            `json:"blocked_packages"`
	EnforceIgnoreScripts bool                `json:"enforce_ignore_scripts"`
	Typosquatting        TyposquattingPolicy `json:"typosquatting"`
	Isolation            IsolationPolicy     `json:"isolation"`
}

type TyposquattingPolicy struct {
	Enabled         bool     `json:"enabled"`
	MaxDistance     int      `json:"max_distance"`
	TrustedPackages []string `json:"trusted_packages"`
}

type IsolationPolicy struct {
	Enabled  bool          `json:"enabled"`
	Provider string        `json:"provider"`
	Runtime  RuntimePolicy `json:"runtime"`
}

type RuntimePolicy struct {
	Command string `json:"command"`
	Version string `json:"version"`
}

// DefaultPolicy returns a Policy object with standard defaults
func DefaultPolicy() Policy {
	return Policy{
		BlockedPackages:      []string{},
		EnforceIgnoreScripts: false,
		Typosquatting: TyposquattingPolicy{
			Enabled:         true,
			MaxDistance:     2,
			TrustedPackages: []string{},
		},
		Isolation: IsolationPolicy{
			Enabled:  false,
			Provider: "native",
			Runtime: RuntimePolicy{
				Command: "",
				Version: "",
			},
		},
	}
}


// LoadPolicy reads and merges global and cascading local policies
func LoadPolicy(nvxHome string) (Policy, error) {
	policy := DefaultPolicy()

	// 1. Load global policy
	globalPolicyPath := filepath.Join(nvxHome, "policy.json")
	if _, err := os.Stat(globalPolicyPath); err == nil {
		data, err := os.ReadFile(globalPolicyPath)
		if err == nil {
			_ = json.Unmarshal(data, &policy)
		}
	}

	// 2. Walk up directory tree to find local policy files (.nvx-policy.json or policy.json)
	cwd, err := os.Getwd()
	if err == nil {
		dir := cwd
		for {
			var localPath string
			localPolicy1 := filepath.Join(dir, ".nvx-policy.json")
			localPolicy2 := filepath.Join(dir, "policy.json")

			if _, err := os.Stat(localPolicy1); err == nil {
				localPath = localPolicy1
			} else if _, err := os.Stat(localPolicy2); err == nil {
				if filepath.Clean(filepath.Dir(localPolicy2)) != filepath.Clean(nvxHome) {
					localPath = localPolicy2
				}
			}

			if localPath != "" {
				var localPolicy Policy
				// Initialize with default typosquatting enabled so it doesn't overwrite global unless explicitly set
				localPolicy.Typosquatting.Enabled = true
				data, err := os.ReadFile(localPath)
				if err == nil {
					if err := json.Unmarshal(data, &localPolicy); err == nil {
						policy = MergePolicies(policy, localPolicy)
					}
				}
			}

			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return policy, nil
}

// MergePolicies combines global and local policies according to priority rules
func MergePolicies(global, local Policy) Policy {
	merged := global

	// 1. Blocked packages: Union
	blockedMap := make(map[string]bool)
	for _, p := range global.BlockedPackages {
		blockedMap[strings.ToLower(p)] = true
	}
	for _, p := range local.BlockedPackages {
		pLower := strings.ToLower(p)
		if !blockedMap[pLower] {
			blockedMap[pLower] = true
			merged.BlockedPackages = append(merged.BlockedPackages, p)
		}
	}

	// 2. Ignore scripts: Logical OR
	if local.EnforceIgnoreScripts {
		merged.EnforceIgnoreScripts = true
	}

	// 3. Typosquatting: trusted packages union, local enabled overrides global
	// Since boolean defaults to false, we inspect the local values
	// We want local policy to be able to disable typosquatting:
	// If typosquatting block is defined in local, we honor its Enabled setting.
	// But in standard unmarshalling, if "enabled" is absent, it is false.
	// To prevent accidental disabling, we initialized localPolicy.Typosquatting.Enabled = true
	// before unmarshalling. So if it is false, the user explicitly set `"enabled": false`.
	if !local.Typosquatting.Enabled {
		merged.Typosquatting.Enabled = false
	}
	if local.Typosquatting.MaxDistance > 0 {
		merged.Typosquatting.MaxDistance = local.Typosquatting.MaxDistance
	}
	trustedMap := make(map[string]bool)
	for _, t := range global.Typosquatting.TrustedPackages {
		trustedMap[strings.ToLower(t)] = true
	}
	for _, t := range local.Typosquatting.TrustedPackages {
		tLower := strings.ToLower(t)
		if !trustedMap[tLower] {
			trustedMap[tLower] = true
			merged.Typosquatting.TrustedPackages = append(merged.Typosquatting.TrustedPackages, t)
		}
	}

	// 4. Isolation: Logical OR for Enabled, Local overrides Provider and Runtime details
	if local.Isolation.Enabled {
		merged.Isolation.Enabled = true
	}
	if local.Isolation.Provider != "" {
		merged.Isolation.Provider = local.Isolation.Provider
	}
	if local.Isolation.Runtime.Command != "" {
		merged.Isolation.Runtime.Command = local.Isolation.Runtime.Command
	}
	if local.Isolation.Runtime.Version != "" {
		merged.Isolation.Runtime.Version = local.Isolation.Runtime.Version
	}

	return merged
}



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

func syncPopularPackages(cachePath string) ([]string, error) {
	// Querying a reliable raw json list of popular npm packages
	client := &http.Client{Timeout: 4 * time.Second}
	// Using a public CDN mirror containing a list of top packages
	resp, err := client.Get("https://raw.githubusercontent.com/npm/dep-graph/master/top-1000.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()


	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	// Some lists are arrays of strings, others are arrays of objects. We parse standard array of strings
	var rawData interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rawData); err != nil {
		return nil, err
	}

	var list []string
	switch typed := rawData.(type) {
	case []interface{}:
		for _, item := range typed {
			if str, ok := item.(string); ok {
				list = append(list, str)
			} else if obj, ok := item.(map[string]interface{}); ok {
				// if object, try to parse name key
				if name, ok := obj["name"].(string); ok {
					list = append(list, name)
				}
			}
		}
	case map[string]interface{}:
		// if object map, extract keys
		for k := range typed {
			list = append(list, k)
		}
	}

	if len(list) == 0 {
		return nil, fmt.Errorf("parsed list is empty")
	}

	// Write cache file
	os.MkdirAll(filepath.Dir(cachePath), 0755)
	data, err := json.Marshal(list)
	if err == nil {
		_ = os.WriteFile(cachePath, data, 0644)
	}
	return list, nil
}

// CheckTyposquatting returns the name of a popular package if the query is suspiciously close (edit distance 1 or 2)
func CheckTyposquatting(pkgName string, popularList []string) string {
	return CheckTyposquattingAuthority(pkgName, popularList, 2)
}

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
			pkgDownloads, errPkg := GetWeeklyDownloads(pkgName)
			suspectDownloads, errSus := GetWeeklyDownloads(popular)

			if errPkg == nil && errSus == nil {
				// Authority threshold: if the target is high-popularity (>50k/week)
				// AND it has more than 100x the weekly downloads of the installed package, it's a typosquat
				if suspectDownloads > 50000 && suspectDownloads > 100 * pkgDownloads {
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

// EscapeScopedPackage replaces "/" with "%2F" for scoped package names
func EscapeScopedPackage(pkg string) string {
	if strings.HasPrefix(pkg, "@") && strings.Contains(pkg, "/") {
		parts := strings.SplitN(pkg, "/", 2)
		return parts[0] + "%2F" + parts[1]
	}
	return pkg
}

// GetWeeklyDownloads queries the public npm downloads point API
func GetWeeklyDownloads(pkgName string) (int, error) {
	escapedPkg := EscapeScopedPackage(pkgName)
	url := fmt.Sprintf("https://api.npmjs.org/downloads/point/last-week/%s", escapedPkg)

	client := &http.Client{Timeout: 5 * time.Second}
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

// ResolveNpmPackageDetails queries npm registry for latest version, publish age, and installation script status
func ResolveNpmPackageDetails(pkgName, versionQuery string) (version string, publishTime time.Time, hasScripts bool, err error) {
	client := &http.Client{Timeout: 8 * time.Second}
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

