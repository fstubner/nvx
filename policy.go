package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Policy defines corporate rules for package manager operations and sandboxing.
type Policy struct {
	BlockedPackages      []string            `json:"blocked_packages"`
	EnforceIgnoreScripts bool                `json:"enforce_ignore_scripts"`
	Typosquatting        TyposquattingPolicy `json:"typosquatting"`
	ReleaseAge           ReleaseAgePolicy    `json:"release_age"`
	Runtime              RuntimeConfig       `json:"runtime"`
	Isolation            IsolationPolicy     `json:"isolation"`
	// A pointer so an unset Prompts serializes away entirely rather than as
	// "prompts": {}, which pointed readers at three keys that were removed for
	// doing nothing. Kept on the struct so existing policy files still parse.
	Prompts     *PromptsPolicy    `json:"prompts,omitempty"`
	Environment EnvironmentPolicy `json:"environment"`

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
	Mode       string   `json:"mode,omitempty"`
	AllowWrite []string `json:"allow_write"`
	// AllowReadExec are extra directories a contained process may READ and
	// EXECUTE from. Never writable, whatever else the policy says.
	//
	// It exists because some tools keep the program they run outside anything nvx
	// grants. Playwright is the case that forced it: its browsers live in
	// %LOCALAPPDATA%\ms-playwright (~/.cache/ms-playwright on Linux), and a
	// contained process could not even list that directory, let alone launch a
	// browser from it -- measured 2026-08-28, EPERM contained against 27 entries
	// uncontained. Nothing about ports was involved, which is what the MCP
	// containment design assumed the blocker was.
	//
	// Deliberately not defaulted to the Playwright path. nvx manages JavaScript
	// runtimes; baking one browser vendor's cache directory into the sandbox
	// would be a guess about what a project runs, and every entry here widens
	// what contained code may execute. Naming it is the point.
	AllowReadExec []string `json:"allow_read_exec,omitempty"`
}

type NetworkPolicy struct {
	Mode          string   `json:"mode"`
	DefaultAllow  []string `json:"default_allow"`
	AllowHosts    []string `json:"allow_hosts"`
	PromptUnknown bool     `json:"prompt_unknown"`
	// ExposePorts publishes a port a server inside the sandbox listens on, so the
	// host can reach it: ["5173"] or ["5173:8080"] as container[:host]. Windows
	// refuses connections into an AppContainer, so without this a contained dev
	// server binds, reports itself listening, and serves nobody.
	//
	// Strings rather than numbers because the host half is optional; with it
	// omitted nvx picks a free port and prints the URL. The two cannot be the same
	// number -- an AppContainer shares the host's network stack, so one port
	// cannot hold both ends (see exposeMapping).
	//
	// This does NOT relax containment: the port is published by a tunnel the
	// contained side dials outward, and no network capability is granted for it.
	// It is also not an egress permission -- what the sandbox may reach is still
	// AllowHosts alone.
	ExposePorts []string `json:"expose_ports,omitempty"`
	// ConnectPorts are services already running on the host that a contained
	// process may reach: ["9222"] or ["9222:19222"] as host[:in-sandbox].
	//
	// The sandbox otherwise cannot reach anything on your machine -- Windows
	// refuses an AppContainer's loopback connections, and a Linux netns has its
	// own loopback entirely. That is deliberate, and this is the narrow, named
	// way through it: one port, for one run, reached over a tunnel nvx owns.
	//
	// It is NOT the pre-0.5.0 loopback exemption, which opened every service on
	// 127.0.0.1 to every sandbox on the machine, permanently, and had to be
	// removed. This grants one port to one run, and nvx dials the host end
	// itself, so the sandbox never gets a general route out.
	ConnectPorts []string `json:"connect_ports,omitempty"`

	DefaultAllowSet  bool `json:"-"`
	PromptUnknownSet bool `json:"-"`
}

type RuntimePolicy struct {
	Command string `json:"command"`
	Version string `json:"version"`
}

type PromptsPolicy struct {
	Interactive    string `json:"interactive,omitempty"`
	NonInteractive string `json:"non_interactive,omitempty"`
	NetworkUnknown string `json:"network_unknown,omitempty"`
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
			// Mode is deliberately unset. It is parsed and merged but read for no
			// decision, so scaffolding it into every new policy shipped a
			// security-looking key -- value literally "strict" -- that did
			// nothing. The other three inert keys were dropped in 0.5.0; this one
			// was missed, which an acceptance pass caught.
			Filesystem: FilesystemPolicy{
				Provider: "native",
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
		// Prompts is parsed and merged but nothing reads it, so it is left empty
		// rather than defaulted: `nvx policy init` used to scaffold three keys
		// that look like settings and do nothing, including when tightened.
		// Prompt behaviour is fixed -- interactive asks, non-interactive denies,
		// and trust-boundary decisions ignore -y entirely.
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
	// isolation.filesystem.mode is parsed and merged but never read, so it is
	// no longer defaulted into every written policy. See PromptsPolicy above.
	if p.Isolation.Network.Mode == "" {
		p.Isolation.Network.Mode = "proxy"
	} else if mode, ok := parseNetworkMode(p.Isolation.Network.Mode); !ok {
		// A typo in this field used to pass silently and bucket as proxy, so a
		// policy asking for "offlin" got a live egress proxy while the user
		// believed they had asked for no network at all. The neighbouring
		// isolation.level already warns on an unrecognised value; this is the same
		// treatment for the field where getting it wrong hands out more access.
		//
		// Normalised rather than refused, matching isolation.level, and because
		// proxy is the restrictive default rather than an open one -- the user
		// asked for stricter than the default and gets the default, loudly. The
		// value is rewritten so every downstream reader sees something valid
		// instead of falling into its own default arm.
		warnUnknownNetworkModeOnce(p.Isolation.Network.Mode)
		p.Isolation.Network.Mode = mode
	}
	if len(p.Isolation.Network.DefaultAllow) == 0 && !p.Isolation.Network.DefaultAllowSet {
		p.Isolation.Network.DefaultAllow = DefaultPolicy().Isolation.Network.DefaultAllow
	}
	// prompts.* is not defaulted either, for the same reason and with a sharper
	// edge: defaulting it wrote a value that reads as a security decision
	// ("non_interactive": "deny") into every policy while nothing consulted it.
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

// withoutUTF8BOM drops a leading UTF-8 byte-order mark.
//
// Windows produces one by default: Notepad writes it, and so does PowerShell's
// `Set-Content -Encoding utf8` on Windows PowerShell 5.1. Go's JSON parser does
// not skip it, so a policy file written either of those ways failed with
// "invalid character 'ï' looking for beginning of value" -- and because nvx
// refuses to run when it cannot read its own policy, the whole command was
// refused. Found when the Windows enforcement gate was repaired and reached its
// assertions for the first time: the gate wrote its own policy with Set-Content
// and nvx would not read it.
//
// The stripped bytes are what everything downstream sees, including the hash
// that pins a trusted project policy. That is deliberate: the mark carries no
// content, so adding or removing one should not read as a policy change the
// user has to re-approve.
func withoutUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
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
	data = withoutUTF8BOM(data)
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
	data = withoutUTF8BOM(data)
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
	// Publishing a port is a widening, even though it grants the sandbox no new
	// access. It puts something the contained process serves onto the host's
	// loopback, where a browser will treat it with the trust localhost carries --
	// so a project file that adds one is asking for something a developer should
	// approve, exactly like an allowlist entry.
	if hostsAdded(before.Isolation.Network.ExposePorts, after.Isolation.Network.ExposePorts) {
		return true
	}
	// Reaching a service on the host is the largest widening in this file: it is
	// a deliberate hole in the containment boundary, of exactly the kind the
	// loopback exemption was removed for being. Narrow and named is what makes it
	// defensible, and approval is part of that.
	if hostsAdded(before.Isolation.Network.ConnectPorts, after.Isolation.Network.ConnectPorts) {
		return true
	}
	// Extra read/execute roots widen what contained code can run. A project file
	// that adds one is asking to execute something from outside everything nvx
	// grants, which is a decision for whoever owns the machine.
	if hostsAdded(before.Isolation.Filesystem.AllowReadExec, after.Isolation.Filesystem.AllowReadExec) {
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
	// Appended, like AllowWrite and the host allowlists: a project adds the roots
	// its own toolchain needs on top of anything global policy already grants,
	// rather than replacing it. policyLoosens treats an addition here as widening,
	// so a checked-in file still has to be trusted before it takes effect.
	if len(local.Isolation.Network.ConnectPorts) > 0 {
		merged.Isolation.Network.ConnectPorts = append(merged.Isolation.Network.ConnectPorts, local.Isolation.Network.ConnectPorts...)
	}
	if len(local.Isolation.Filesystem.AllowReadExec) > 0 {
		merged.Isolation.Filesystem.AllowReadExec = append(merged.Isolation.Filesystem.AllowReadExec, local.Isolation.Filesystem.AllowReadExec...)
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
	// prompts.* is intentionally not merged: nothing reads it, so merging it was
	// work that produced a value no decision consulted.

	if local.Environment.IsolatedTools {
		merged.Environment.IsolatedTools = true
	}

	return merged
}

// parseNetworkMode canonicalises isolation.network.mode, reporting whether the
// value was recognised. An unrecognised mode normalises to "proxy" -- the
// restrictive default -- so callers never have to guess at a typo's intent.
func parseNetworkMode(mode string) (string, bool) {
	switch m := strings.ToLower(strings.TrimSpace(mode)); m {
	case "proxy", "offline", "loopback", "open":
		return m, true
	case "":
		return "proxy", true
	default:
		return "proxy", false
	}
}

// warnUnknownNetworkModeOnce reports a bad mode a single time per value.
// normalizePolicy runs more than once per invocation, and repeating a security
// warning verbatim trains people to skim past it.
var (
	warnedNetworkModesMu sync.Mutex
	warnedNetworkModes   = map[string]bool{}
)

func warnUnknownNetworkModeOnce(mode string) {
	warnedNetworkModesMu.Lock()
	seen := warnedNetworkModes[mode]
	warnedNetworkModes[mode] = true
	warnedNetworkModesMu.Unlock()
	if seen {
		return
	}
	LogWarn("Unrecognized isolation.network.mode %q in policy; using proxy (allowlisted egress).", mode)
	LogInfo("Valid modes: proxy, offline, loopback, open.")
}
