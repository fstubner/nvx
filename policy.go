package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Policy defines corporate rules for package manager operations and sandboxing.
type Policy struct {
	BlockedPackages      []string            `json:"blocked_packages"`
	EnforceIgnoreScripts bool                `json:"enforce_ignore_scripts"`
	Typosquatting        TyposquattingPolicy `json:"typosquatting"`
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
	// Legacy top-level provider from older policy files.
	Provider string        `json:"provider,omitempty"`
	Runtime  RuntimePolicy `json:"runtime,omitempty"`
}

type FilesystemPolicy struct {
	Provider   string   `json:"provider"`
	Mode       string   `json:"mode"`
	AllowWrite []string `json:"allow_write"`
}

type NetworkPolicy struct {
	Mode         string   `json:"mode"`
	DefaultAllow []string `json:"default_allow"`
	AllowHosts   []string `json:"allow_hosts"`
	PromptUnknown bool    `json:"prompt_unknown"`
}

type RuntimePolicy struct {
	Command string `json:"command"`
	Version string `json:"version"`
}

type PromptsPolicy struct {
	Interactive     string `json:"interactive"`
	NonInteractive  string `json:"non_interactive"`
	NetworkUnknown  string `json:"network_unknown"`
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
	if len(p.Isolation.Network.DefaultAllow) == 0 {
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
		for _, h := range provider.DefaultNetworkAllow() {
			add(h)
		}
	}
	return out
}

func LoadPolicy(nvxHome string) (Policy, error) {
	policy := DefaultPolicy()

	globalPolicyPath := filepath.Join(nvxHome, "policy.json")
	if _, err := os.Stat(globalPolicyPath); err == nil {
		data, err := os.ReadFile(globalPolicyPath)
		if err == nil {
			_ = json.Unmarshal(data, &policy)
		}
	}

	cwd, err := os.Getwd()
	if err == nil {
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

		for i := len(localPaths) - 1; i >= 0; i-- {
			localPath := localPaths[i]
			var localPolicy Policy
			localPolicy.Typosquatting.Enabled = true
			data, err := os.ReadFile(localPath)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(data, &localPolicy); err != nil {
				continue
			}
			policy = MergePolicies(policy, localPolicy)
			if localPolicy.Environment.IsolatedTools {
				policy.ProjectDir = filepath.Dir(localPath)
			}
		}
	}

	normalizePolicy(&policy)
	return policy, nil
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

	if local.Isolation.Enabled {
		merged.Isolation.Enabled = true
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
	if len(local.Isolation.Network.DefaultAllow) > 0 {
		merged.Isolation.Network.DefaultAllow = local.Isolation.Network.DefaultAllow
	}
	if len(local.Isolation.Network.AllowHosts) > 0 {
		merged.Isolation.Network.AllowHosts = append(merged.Isolation.Network.AllowHosts, local.Isolation.Network.AllowHosts...)
	}
	if local.Isolation.Network.PromptUnknown {
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
