package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Policy defines corporate rules for package manager operations and sandboxing.
type Policy struct {
	BlockedPackages      []string            `json:"blocked_packages"`
	EnforceIgnoreScripts bool                `json:"enforce_ignore_scripts"`
	Typosquatting        TyposquattingPolicy `json:"typosquatting"`
	ReleaseAge           ReleaseAgePolicy    `json:"release_age"`
	Runtime              RuntimeConfig       `json:"runtime"`
	Isolation            IsolationPolicy     `json:"isolation"`
	Prompts              PromptsPolicy       `json:"prompts"`
	Environment          EnvironmentPolicy   `json:"environment"`

	ProjectDir string `json:"-"`
}

type EnvironmentPolicy struct {
	IsolatedTools bool `json:"isolated_tools"`
}

type TyposquattingPolicy struct {
	Enabled         bool     `json:"enabled"`
	MaxDistance     int      `json:"max_distance"`
	TrustedPackages []string `json:"trusted_packages"`
}

// ReleaseAgePolicy warns when installing npm package versions published within
// min_age_hours. Trusted packages (typosquatting.trusted_packages) are exempt.
type ReleaseAgePolicy struct {
	Enabled     *bool `json:"enabled,omitempty"`
	MinAgeHours int   `json:"min_age_hours,omitempty"`
}

type RuntimeConfig struct {
	Default  string            `json:"default"`
	Versions map[string]string `json:"versions"`
	// Legacy fields from isolation.runtime (migrated on load).
	Command string `json:"command,omitempty"`
	Version string `json:"version,omitempty"`
}

type IsolationPolicy struct {
	Enabled    bool             `json:"enabled"`
	Filesystem FilesystemPolicy `json:"filesystem"`
	Network    NetworkPolicy    `json:"network"`
	// Level selects standard vs strict containment (see isolationLevel in
	// containment.go). Empty/unrecognized values normalize to "standard".
	Level string `json:"level,omitempty"`
	// Legacy top-level provider from older policy files.
	Provider string        `json:"provider,omitempty"`
	Runtime  RuntimePolicy `json:"runtime,omitempty"`

	EnabledSet bool `json:"-"`
}

type FilesystemPolicy struct {
	Provider   string   `json:"provider"`
	Mode       string   `json:"mode"`
	AllowWrite []string `json:"allow_write"`
}

type NetworkPolicy struct {
	Mode          string   `json:"mode"`
	DefaultAllow  []string `json:"default_allow"`
	AllowHosts    []string `json:"allow_hosts"`
	PromptUnknown bool     `json:"prompt_unknown"`

	DefaultAllowSet  bool `json:"-"`
	PromptUnknownSet bool `json:"-"`
}

type RuntimePolicy struct {
	Command string `json:"command"`
	Version string `json:"version"`
}

type PromptsPolicy struct {
	Interactive    string `json:"interactive"`
	NonInteractive string `json:"non_interactive"`
	NetworkUnknown string `json:"network_unknown"`
}

func DefaultPolicy() Policy {
	return Policy{
		BlockedPackages:      []string{},
		EnforceIgnoreScripts: false,
		Typosquatting: TyposquattingPolicy{
			Enabled:         true,
			MaxDistance:     2,
			TrustedPackages: []string{},
		},
		ReleaseAge: ReleaseAgePolicy{
			Enabled:     boolPtr(true),
			MinAgeHours: 24,
		},
		Runtime: RuntimeConfig{
			Default:  "node",
			Versions: map[string]string{},
		},
		Isolation: IsolationPolicy{
			Enabled: true,
			Filesystem: FilesystemPolicy{
				Provider: "native",
				Mode:     "strict",
			},
			Network: NetworkPolicy{
				Mode: "proxy",
				DefaultAllow: []string{
					"registry.npmjs.org:443",
					"api.osv.dev:443",
				},
				PromptUnknown: true,
			},
		},
		Prompts: PromptsPolicy{
			Interactive:    "ask",
			NonInteractive: "deny",
			NetworkUnknown: "ask",
		},
	}
}

func normalizePolicy(p *Policy) {
	if p.Runtime.Default == "" {
		p.Runtime.Default = "node"
	}
	if p.Runtime.Versions == nil {
		p.Runtime.Versions = map[string]string{}
	}

	// Legacy isolation.runtime → runtime.version/command
	if p.Isolation.Runtime.Version != "" && p.Runtime.Version == "" {
		p.Runtime.Version = p.Isolation.Runtime.Version
	}
	if p.Isolation.Runtime.Command != "" && p.Runtime.Command == "" {
		p.Runtime.Command = p.Isolation.Runtime.Command
	}
	if p.Runtime.Version != "" && p.Runtime.Versions["node"] == "" {
		p.Runtime.Versions["node"] = p.Runtime.Version
	}

	// Legacy isolation.provider → filesystem.provider
	if p.Isolation.Filesystem.Provider == "" {
		if p.Isolation.Provider != "" {
			p.Isolation.Filesystem.Provider = p.Isolation.Provider
		} else {
			p.Isolation.Filesystem.Provider = "native"
		}
	}
	if p.Isolation.Filesystem.Mode == "" {
		p.Isolation.Filesystem.Mode = "strict"
	}
	if p.Isolation.Network.Mode == "" {
		p.Isolation.Network.Mode = "proxy"
	}
	if len(p.Isolation.Network.DefaultAllow) == 0 && !p.Isolation.Network.DefaultAllowSet {
		p.Isolation.Network.DefaultAllow = DefaultPolicy().Isolation.Network.DefaultAllow
	}
	if p.Prompts.Interactive == "" {
		p.Prompts.Interactive = "ask"
	}
	if p.Prompts.NonInteractive == "" {
		p.Prompts.NonInteractive = "deny"
	}
	if p.Prompts.NetworkUnknown == "" {
		p.Prompts.NetworkUnknown = "ask"
	}
	normalizeReleaseAgePolicy(&p.ReleaseAge)
}

func boolPtr(b bool) *bool {
	return &b
}

func normalizeReleaseAgePolicy(r *ReleaseAgePolicy) {
	if r.Enabled == nil {
		r.Enabled = boolPtr(true)
	}
	if *r.Enabled && r.MinAgeHours <= 0 {
		r.MinAgeHours = 24
	}
}

// ReleaseAgeEnabled reports whether the release-age install warning is active.
func (p Policy) ReleaseAgeEnabled() bool {
	if p.ReleaseAge.Enabled == nil {
		return true
	}
	return *p.ReleaseAge.Enabled
}

// ReleaseAgeMinHours returns the minimum publish age (in hours) before a version
// is accepted without warning. Defaults to 24 when enabled.
func (p Policy) ReleaseAgeMinHours() int {
	if p.ReleaseAge.MinAgeHours > 0 {
		return p.ReleaseAge.MinAgeHours
	}
	if p.ReleaseAgeEnabled() {
		return 24
	}
	return 0
}

// IsTrustedPackage returns true when pkgName is listed in typosquatting.trusted_packages,
// or matches a wildcard pattern (e.g. "@myorg/*", "internal-*").
func (p Policy) IsTrustedPackage(pkgName string) bool {
	lower := strings.ToLower(pkgName)
	for _, t := range p.Typosquatting.TrustedPackages {
		tLower := strings.ToLower(t)
		if tLower == lower {
			return true
		}
		if strings.Contains(tLower, "*") || strings.Contains(tLower, "?") {
			if matched, err := filepath.Match(tLower, lower); err == nil && matched {
				return true
			}
		}
	}
	return false
}

func (p Policy) PinnedRuntimeVersion(runtimeName string) string {
	if runtimeName == "" {
		runtimeName = p.Runtime.Default
	}
	if v := p.Runtime.Versions[runtimeName]; v != "" {
		return v
	}
	if runtimeName == p.Runtime.Default || runtimeName == "node" {
		return p.Runtime.Version
	}
	return ""
}

// IsolationLevel returns the effective containment level, normalizing an
// empty or unrecognized isolation.level value to standard.
func (p Policy) IsolationLevel() isolationLevel {
	level, ok := parseIsolationLevel(p.Isolation.Level)
	if !ok {
		LogWarn("Unrecognized isolation.level %q in policy; using standard.", p.Isolation.Level)
	}
	return level
}

func (p Policy) FilesystemProvider() string {
	if p.Isolation.Filesystem.Provider != "" {
		return strings.ToLower(p.Isolation.Filesystem.Provider)
	}
	return "native"
}

func (p Policy) NetworkAllowlist(provider RuntimeProvider) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.TrimSpace(strings.ToLower(h))
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range p.Isolation.Network.DefaultAllow {
		add(h)
	}
	for _, h := range p.Isolation.Network.AllowHosts {
		add(h)
	}
	if provider != nil {
		if !p.Isolation.Network.DefaultAllowSet {
			for _, h := range provider.DefaultNetworkAllow() {
				add(h)
			}
		}
	}
	return out
}

func markPolicyFieldPresence(data []byte, p *Policy) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return
	}
	isolationRaw, ok := root["isolation"]
	if !ok {
		return
	}
	var isolation map[string]json.RawMessage
	if err := json.Unmarshal(isolationRaw, &isolation); err != nil {
		return
	}
	if _, ok := isolation["enabled"]; ok {
		p.Isolation.EnabledSet = true
	}
	networkRaw, ok := isolation["network"]
	if !ok {
		return
	}
	var network map[string]json.RawMessage
	if err := json.Unmarshal(networkRaw, &network); err != nil {
		return
	}
	if _, ok := network["default_allow"]; ok {
		p.Isolation.Network.DefaultAllowSet = true
	}
	if _, ok := network["prompt_unknown"]; ok {
		p.Isolation.Network.PromptUnknownSet = true
	}
}

// loadGlobalPolicy returns DefaultPolicy merged with ~/.nvx/policy.json.
func loadGlobalPolicy(nvxHome string) (Policy, error) {
	policy := DefaultPolicy()
	globalPolicyPath := filepath.Join(nvxHome, "policy.json")
	data, err := os.ReadFile(globalPolicyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return policy, nil
		}
		return policy, fmt.Errorf("read global policy %s: %w", globalPolicyPath, err)
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return policy, fmt.Errorf("parse global policy %s: %w", globalPolicyPath, err)
	}
	markPolicyFieldPresence(data, &policy)
	return policy, nil
}

// collectProjectPolicyPaths walks upward from cwd collecting project policy
// files (nearest last is applied last, so nearest wins).
func collectProjectPolicyPaths(cwd, nvxHome string) []string {
	var localPaths []string
	dir := cwd
	for {
		localPolicy1 := filepath.Join(dir, ".nvx-policy.json")
		localPolicy2 := filepath.Join(dir, "policy.json")

		if _, err := os.Stat(localPolicy1); err == nil {
			localPaths = append(localPaths, localPolicy1)
		} else if _, err := os.Stat(localPolicy2); err == nil {
			if filepath.Clean(filepath.Dir(localPolicy2)) != filepath.Clean(nvxHome) {
				localPaths = append(localPaths, localPolicy2)
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return localPaths
}

func readProjectPolicyFile(path string) (Policy, []byte, error) {
	var lp Policy
	lp.Typosquatting.Enabled = true
	data, err := os.ReadFile(path)
	if err != nil {
		return lp, nil, fmt.Errorf("read local policy %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &lp); err != nil {
		return lp, nil, fmt.Errorf("parse local policy %s: %w", path, err)
	}
	markPolicyFieldPresence(data, &lp)
	return lp, data, nil
}

// LoadPolicy builds the effective policy from the global policy, the project
// policy files, and the project's grant file. A project policy file that would
// loosen the accumulated settings is applied only when its current contents
// have been trusted for this project (see ensureProjectPolicyTrust); otherwise
// it is ignored so that untrusted, in-tree settings cannot weaken protection.
func LoadPolicy(nvxHome string) (Policy, error) {
	policy, err := loadGlobalPolicy(nvxHome)
	if err != nil {
		return policy, err
	}

	cwd, cwdErr := os.Getwd()
	if cwdErr == nil {
		localPaths := collectProjectPolicyPaths(cwd, nvxHome)
		grants := loadProjectGrants(nvxHome, projectScopeDir())

		for i := len(localPaths) - 1; i >= 0; i-- {
			localPath := localPaths[i]
			localPolicy, _, err := readProjectPolicyFile(localPath)
			if err != nil {
				return policy, err
			}
			candidate := MergePolicies(policy, localPolicy)
			if policyLoosens(policy, candidate) {
				hash, ok := hashPolicyFile(localPath)
				if !ok || grants.PolicyPins[filepath.Clean(localPath)] != hash {
					LogWarn("Ignoring project policy %s: it loosens nvx security settings and has not been trusted for this project.", localPath)
					continue
				}
			}
			policy = candidate
			if localPolicy.Environment.IsolatedTools {
				policy.ProjectDir = filepath.Dir(localPath)
			}
		}

		for _, h := range grants.AllowHosts {
			policy.Isolation.Network.AllowHosts = append(policy.Isolation.Network.AllowHosts, h)
		}
	}

	normalizePolicy(&policy)
	return policy, nil
}

// ensureProjectPolicyTrust prompts once for any project policy file that would
// loosen security and has not been trusted. Accepting records a pin (keyed by
// file contents) under ~/.nvx; declining, or a non-interactive environment,
// leaves the file untrusted so LoadPolicy will ignore it. This runs on the
// execution path before a sandboxed command starts.
func ensureProjectPolicyTrust(nvxHome string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	localPaths := collectProjectPolicyPaths(cwd, nvxHome)
	if len(localPaths) == 0 {
		return nil
	}

	baseline, err := loadGlobalPolicy(nvxHome)
	if err != nil {
		return err
	}
	scope := projectScopeDir()
	grants := loadProjectGrants(nvxHome, scope)
	changed := false

	for i := len(localPaths) - 1; i >= 0; i-- {
		localPath := localPaths[i]
		localPolicy, _, err := readProjectPolicyFile(localPath)
		if err != nil {
			return err
		}
		candidate := MergePolicies(baseline, localPolicy)
		if policyLoosens(baseline, candidate) {
			hash, _ := hashPolicyFile(localPath)
			cleanPath := filepath.Clean(localPath)
			if grants.PolicyPins[cleanPath] != hash {
				if !PromptTrustBoundary("Project policy " + localPath + " loosens nvx security settings. Trust it for this project?") {
					auditLog(nvxHome, "policy_pin_changed_denied", map[string]string{"path": cleanPath})
					continue
				}
				grants.PolicyPins[cleanPath] = hash
				changed = true
				auditLog(nvxHome, "policy_pin_accepted", map[string]string{"path": cleanPath})
			}
		}
		baseline = candidate
	}

	if changed {
		grants.ProjectPath = scope
		if err := saveProjectGrants(nvxHome, grants); err != nil {
			LogWarn("Failed to record project policy trust: %v", err)
		}
	}
	return nil
}

func networkModeRank(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "open":
		return 3
	case "offline", "loopback":
		return 1
	default: // proxy and unknown
		return 2
	}
}

func isolationLevelRank(level string) int {
	l, _ := parseIsolationLevel(level)
	if l == levelStrict {
		return 2
	}
	return 1
}

func hostsAdded(before, after []string) bool {
	seen := map[string]bool{}
	for _, h := range before {
		seen[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for _, h := range after {
		if !seen[strings.ToLower(strings.TrimSpace(h))] {
			return true
		}
	}
	return false
}

// policyLoosens reports whether the after policy is more permissive than before.
func policyLoosens(before, after Policy) bool {
	if before.Isolation.Enabled && !after.Isolation.Enabled {
		return true
	}
	if networkModeRank(after.Isolation.Network.Mode) > networkModeRank(before.Isolation.Network.Mode) {
		return true
	}
	if before.Typosquatting.Enabled && !after.Typosquatting.Enabled {
		return true
	}
	if before.ReleaseAgeEnabled() && !after.ReleaseAgeEnabled() {
		return true
	}
	if !before.Isolation.Network.PromptUnknown && after.Isolation.Network.PromptUnknown {
		return true
	}
	if len(after.Typosquatting.TrustedPackages) > len(before.Typosquatting.TrustedPackages) {
		return true
	}
	if hostsAdded(before.Isolation.Network.DefaultAllow, after.Isolation.Network.DefaultAllow) {
		return true
	}
	if hostsAdded(before.Isolation.Network.AllowHosts, after.Isolation.Network.AllowHosts) {
		return true
	}
	if !strings.EqualFold(before.Isolation.Filesystem.Provider, after.Isolation.Filesystem.Provider) {
		return true
	}
	if before.EnforceIgnoreScripts && !after.EnforceIgnoreScripts {
		return true
	}
	if isolationLevelRank(after.Isolation.Level) < isolationLevelRank(before.Isolation.Level) {
		return true
	}
	return false
}

func MergePolicies(global, local Policy) Policy {
	merged := global

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

	if local.EnforceIgnoreScripts {
		merged.EnforceIgnoreScripts = true
	}

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

	if local.ReleaseAge.Enabled != nil {
		merged.ReleaseAge.Enabled = local.ReleaseAge.Enabled
	}
	if local.ReleaseAge.MinAgeHours > 0 {
		merged.ReleaseAge.MinAgeHours = local.ReleaseAge.MinAgeHours
	}

	if local.Isolation.EnabledSet {
		merged.Isolation.Enabled = local.Isolation.Enabled
	} else if local.Isolation.Enabled {
		merged.Isolation.Enabled = true
	}
	if local.Isolation.Level != "" {
		merged.Isolation.Level = local.Isolation.Level
	}
	if local.Isolation.Filesystem.Provider != "" {
		merged.Isolation.Filesystem.Provider = local.Isolation.Filesystem.Provider
	}
	if local.Isolation.Filesystem.Mode != "" {
		merged.Isolation.Filesystem.Mode = local.Isolation.Filesystem.Mode
	}
	if len(local.Isolation.Filesystem.AllowWrite) > 0 {
		merged.Isolation.Filesystem.AllowWrite = append(merged.Isolation.Filesystem.AllowWrite, local.Isolation.Filesystem.AllowWrite...)
	}
	if local.Isolation.Network.Mode != "" {
		merged.Isolation.Network.Mode = local.Isolation.Network.Mode
	}
	if local.Isolation.Network.DefaultAllowSet {
		merged.Isolation.Network.DefaultAllow = local.Isolation.Network.DefaultAllow
		merged.Isolation.Network.DefaultAllowSet = true
	} else if len(local.Isolation.Network.DefaultAllow) > 0 {
		merged.Isolation.Network.DefaultAllow = local.Isolation.Network.DefaultAllow
	}
	if len(local.Isolation.Network.AllowHosts) > 0 {
		merged.Isolation.Network.AllowHosts = append(merged.Isolation.Network.AllowHosts, local.Isolation.Network.AllowHosts...)
	}
	if local.Isolation.Network.PromptUnknownSet {
		merged.Isolation.Network.PromptUnknown = local.Isolation.Network.PromptUnknown
		merged.Isolation.Network.PromptUnknownSet = true
	} else if local.Isolation.Network.PromptUnknown {
		merged.Isolation.Network.PromptUnknown = true
	}
	// Legacy isolation.provider
	if local.Isolation.Provider != "" {
		merged.Isolation.Filesystem.Provider = local.Isolation.Provider
	}
	if local.Isolation.Runtime.Version != "" {
		merged.Runtime.Version = local.Isolation.Runtime.Version
	}
	if local.Isolation.Runtime.Command != "" {
		merged.Runtime.Command = local.Isolation.Runtime.Command
	}
	if local.Runtime.Default != "" {
		merged.Runtime.Default = local.Runtime.Default
	}
	for k, v := range local.Runtime.Versions {
		if merged.Runtime.Versions == nil {
			merged.Runtime.Versions = map[string]string{}
		}
		merged.Runtime.Versions[k] = v
	}
	if local.Runtime.Version != "" {
		merged.Runtime.Version = local.Runtime.Version
	}
	if local.Runtime.Command != "" {
		merged.Runtime.Command = local.Runtime.Command
	}
	if local.Prompts.Interactive != "" {
		merged.Prompts.Interactive = local.Prompts.Interactive
	}
	if local.Prompts.NonInteractive != "" {
		merged.Prompts.NonInteractive = local.Prompts.NonInteractive
	}
	if local.Prompts.NetworkUnknown != "" {
		merged.Prompts.NetworkUnknown = local.Prompts.NetworkUnknown
	}

	if local.Environment.IsolatedTools {
		merged.Environment.IsolatedTools = true
	}

	return merged
}
