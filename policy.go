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
	// Environment names variables a contained process may keep. See
	// IsolationEnvironmentPolicy.
	Environment IsolationEnvironmentPolicy `json:"environment,omitempty"`
	// Level selects standard vs strict containment (see isolationLevel in
	// containment.go). Empty/unrecognized values normalize to "standard".
	Level string `json:"level,omitempty"`
	// Legacy top-level provider from older policy files.
	Provider string        `json:"provider,omitempty"`
	Runtime  RuntimePolicy `json:"runtime,omitempty"`

	EnabledSet bool `json:"-"`
}

type FilesystemPolicy struct {
	Provider string `json:"provider"`
	Mode     string `json:"mode,omitempty"`
	// allow_write was here, declared and merged, and read by nothing. Removed
	// 2026-09-03 rather than implemented.
	//
	// It arrived on 2026-07-02 in a schema refactor, as part of a shape nobody
	// had asked for yet, and no code ever consulted it. Setting it did nothing,
	// which at least failed closed -- but because it was a KNOWN key,
	// policy_unknown_keys.go stayed quiet, so a policy naming it got no warning
	// either. A security tool that silently ignores a permission someone wrote
	// down is worse than one that refuses to understand it.
	//
	// Deleting rather than wiring up, because there is no caller: a writable root
	// beyond the project and the guest home is a write-containment escape, and
	// the one real request for reaching outside the project -- a tool whose
	// program lives elsewhere -- is served by AllowReadExec, which is never
	// writable. If this is ever wanted, it needs a policyLoosens clause too, or a
	// project-local file could add writable roots with no trust prompt.
	// Unknown-key warnings will now name it, which is the correct answer for
	// anyone who had it in a file.
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

// IsolationEnvironmentPolicy names environment variables a contained process
// should keep.
//
// Named for isolation rather than taking the plain EnvironmentPolicy name, which
// was already taken by an unrelated struct (Policy.Environment.isolated_tools).
//
// Containment allows 11 environment variables through on Windows (7 on Unix) and drops the rest, so an install script
// cannot read the secrets in the shell that launched it. That is correct and
// stays. The cost is that a variable a build genuinely needs goes with them:
// measured on Windows on 2026-09-03, 107 variables outside a contained run and
// 48 inside, with CI and NODE_ENV among the casualties. A tool that reads CI to
// suppress prompts starts prompting; nothing errors. Before this existed the
// only way to get one through was --no-sandbox, which answers "I need one
// variable" by switching the sandbox off.
//
// Names only, matched case-insensitively, no patterns. A glob would be the
// obvious next step and there is no second caller asking for one yet.
//
// A name matching a sensitive prefix (AWS_, GITHUB_, ...) is refused and warned
// about rather than honoured -- see refusedPassEnv. Adding an entry counts as
// loosening, so a project-local file naming one needs the same approval an
// egress host does.
type IsolationEnvironmentPolicy struct {
	Allow []string `json:"allow,omitempty"`
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
	} else {
		mode, ok := parseNetworkMode(p.Isolation.Network.Mode)
		if !ok {
			// A typo in this field used to pass silently and bucket as proxy, so a
			// policy asking for "offlin" got a live egress proxy while the user
			// believed they had asked for no network at all. The neighbouring
			// isolation.level already warns on an unrecognised value; this is the same
			// treatment for the field where getting it wrong hands out more access.
			//
			// Normalised rather than refused, matching isolation.level, and because
			// proxy is the restrictive default rather than an open one -- the user
			// asked for stricter than the default and gets the default, loudly.
			warnUnknownNetworkModeOnce(p.Isolation.Network.Mode)
		}
		// Written back on BOTH branches, which is the fix for a fail-open.
		//
		// parseNetworkMode trims and lowercases before it validates, so "offline "
		// is a VALID mode and returns ok -- and the write-back used to live only on
		// the !ok arm, so the padded string survived into the policy. Downstream
		// readers are not uniform about that: networkModeRank trims, so the policy
		// ranked as stricter than the default and merged with no trust prompt,
		// while networkModeRequiresNamespace and applyLinuxNetworkSeccomp lowercase
		// without trimming and fell through to their default arms -- no network
		// namespace, and a seccomp filter reported as installed that was never
		// built.
		//
		// Net effect on Linux: a checked-in .nvx-policy.json asking for something
		// apparently STRICTER than the default silently got unrestricted host
		// network, and the trust prompt that exists to catch a policy widening
		// access never fired, because by its own measure nothing had widened.
		// Windows and macOS were unaffected -- neither keys containment off this
		// string -- which is why nothing caught it.
		//
		// Normalising here rather than trimming at each reader: there are six
		// consumers of this field and two of them were wrong, so the value that
		// leaves normalizePolicy is now canonical and no reader has to remember.
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
// The stripped bytes are what everything downstream PARSES, and what
// field-presence detection re-reads. They are NOT what pins a trusted project
// policy: that hash is taken over the raw bytes, before this runs, so adding or
// removing a mark does change the pin and does cost one re-approval.
//
// This comment claimed the opposite until an acceptance pass checked it. The
// behaviour it described is arguably the better one -- a mark carries no content
// -- but every pin on disk was computed from raw bytes, and quietly changing the
// basis would invalidate them all and re-prompt users who had already decided.
// Stating what is true beats implementing what was written.
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
	warnAboutUnknownPolicyKeys(globalPolicyPath, data)
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
	lp, body, _, err := readAndHashProjectPolicyFile(path)
	return lp, body, err
}

// readAndHashProjectPolicyFile reads the file ONCE and returns both what was
// parsed and the hash that pins it.
//
// The two used to come from two reads: the caller parsed via
// readProjectPolicyFile, discarded the bytes, and then hashPolicyFile opened the
// same path again. Anything able to write the project directory between them --
// which contained code is, since the working directory is writable in every
// sandbox -- could have the loosened version parsed while the pinned version was
// hashed, and a policy the user never trusted would be accepted. Policy
// tampering is named in SECURITY.md's scope, so this is in the threat model
// rather than adjacent to it.
//
// The hash is taken over the RAW bytes, before the byte-order mark is stripped,
// because that is what hashPolicyFile did and what every pin already on disk was
// computed from. Hashing the parsed bytes instead would be tidier and would
// silently invalidate every existing pin on a file Windows wrote with a BOM,
// re-prompting users who had already decided. The comment above
// markPolicyFieldPresence claimed the stripped bytes were "what pins a trusted
// project policy"; they never were, and it now says so.
func readAndHashProjectPolicyFile(path string) (Policy, []byte, string, error) {
	var lp Policy
	lp.Typosquatting.Enabled = true
	raw, err := os.ReadFile(path)
	if err != nil {
		return lp, nil, "", fmt.Errorf("read local policy %s: %w", path, err)
	}
	hash := hashPolicyBytes(raw)

	data := withoutUTF8BOM(raw)
	if err := json.Unmarshal(data, &lp); err != nil {
		return lp, nil, "", fmt.Errorf("parse local policy %s: %w", path, err)
	}
	warnAboutUnknownPolicyKeys(path, data)
	markPolicyFieldPresence(data, &lp)
	return lp, data, hash, nil
}

// LoadPolicy builds the effective policy from the global policy, the project
// policy files, and the project's grant file. A project policy file that would
// loosen the accumulated settings is applied only when its current contents
// have been trusted for this project (see ensureProjectPolicyTrust); otherwise
// it is ignored so that untrusted, in-tree settings cannot weaken protection.
// ignoredPolicyWarned remembers which untrusted policy files this process has
// already complained about.
//
// A CONTAINED command loads the policy twice -- once on the shim path to decide
// whether to sandbox, and again inside runSandbox -- so one command produced two
// identical warnings, which reads as two separate problems with two separate
// files. Keyed by path, so two genuinely different untrusted files still produce
// two warnings; only the repeat of the same one is dropped.
//
// Measured wrong once, and worth saying how: counting the line on an UNCONTAINED
// run shows one, because at the default level `node` is your own code, runSandbox
// never runs, and the policy is loaded once. The duplicate only appears when the
// command is actually contained. An acceptance pass reported it, I measured the
// uncontained case, and concluded it did not exist.
var ignoredPolicyWarned sync.Map

func warnIgnoredPolicyOnce(path string) {
	if _, seen := ignoredPolicyWarned.LoadOrStore(filepath.Clean(path), true); seen {
		return
	}
	LogWarn("Ignoring project policy %s: it loosens nvx security settings and has not been trusted for this project.", path)
}

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
			// One read: the bytes hashed are the bytes parsed. See
			// readAndHashProjectPolicyFile.
			localPolicy, _, hash, err := readAndHashProjectPolicyFile(localPath)
			if err != nil {
				return policy, err
			}
			candidate := MergePolicies(policy, localPolicy)
			if policyLoosens(policy, candidate) {
				if grants.PolicyPins[filepath.Clean(localPath)] != hash {
					warnIgnoredPolicyOnce(localPath)
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
		// One read here too, and for a second reason: this is where the pin is
		// WRITTEN. Hashing a different read than the one the user was shown and
		// approved would record a pin for bytes nobody agreed to.
		localPolicy, _, hash, err := readAndHashProjectPolicyFile(localPath)
		if err != nil {
			return err
		}
		candidate := MergePolicies(baseline, localPolicy)
		if policyLoosens(baseline, candidate) {
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
//
// Two fields an audit flagged as missing are deliberately absent, and stay that
// way because MergePolicies makes them unreachable rather than because they do
// not matter:
//
//   - BlockedPackages is unioned by MergePolicies, so a project file can only ADD
//     to the blocklist. There is no merge that removes an entry, so there is no
//     loosening to detect.
//   - Runtime pins which runtime version is used. It grants no access and
//     restricts none; a project asking for Node 20 is not asking for permission.
//
// Both were checked against MergePolicies rather than assumed. If either ever
// starts replacing instead of unioning, they belong here.
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
	// Compared as a set, not by length.
	//
	// Length is correct today only because MergePolicies unions these lists and
	// never removes, so after is always a superset of before. That makes the
	// length test right by accident of a rule stated somewhere else, and silently
	// wrong the day merging changes. hostsAdded is what every other list on this
	// function uses and costs nothing.
	if hostsAdded(before.Typosquatting.TrustedPackages, after.Typosquatting.TrustedPackages) {
		return true
	}
	// Lowering the typosquat edit distance finds fewer typosquats. The default is
	// 2; a project file setting 1 halves what the check catches, and MergePolicies
	// takes any positive local value, so it applies. Nothing here noticed.
	if after.Typosquatting.MaxDistance > 0 && before.Typosquatting.MaxDistance > 0 &&
		after.Typosquatting.MaxDistance < before.Typosquatting.MaxDistance {
		return true
	}
	// Same shape for the release-age cooling-off window. The default is 24 hours
	// and the whole point of it is that a compromise is usually caught inside it,
	// so a project file quietly setting min_age_hours to 1 is asking to install
	// things published minutes ago.
	if after.ReleaseAgeMinHours() < before.ReleaseAgeMinHours() {
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
	// A passed-through variable carries whatever the shell holds into code the
	// project did not write. Naming one is a deliberate hole in the scrub, so it
	// gets the same approval an egress host does.
	if hostsAdded(before.Isolation.Environment.Allow, after.Isolation.Environment.Allow) {
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
	// Appended, like the host allowlists: a project adds the roots
	// its own toolchain needs on top of anything global policy already grants,
	// rather than replacing it. policyLoosens treats an addition here as widening,
	// so a checked-in file still has to be trusted before it takes effect.
	if len(local.Isolation.Network.ConnectPorts) > 0 {
		merged.Isolation.Network.ConnectPorts = append(merged.Isolation.Network.ConnectPorts, local.Isolation.Network.ConnectPorts...)
	}
	if len(local.Isolation.Filesystem.AllowReadExec) > 0 {
		merged.Isolation.Filesystem.AllowReadExec = append(merged.Isolation.Filesystem.AllowReadExec, local.Isolation.Filesystem.AllowReadExec...)
	}
	if len(local.Isolation.Environment.Allow) > 0 {
		merged.Isolation.Environment.Allow = append(merged.Isolation.Environment.Allow, local.Isolation.Environment.Allow...)
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
