package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// projectGrants records per-project state that must live outside the project
// tree, so that code running inside the sandbox (which can write the working
// directory) cannot edit the settings that govern it.
//
//   - AllowHosts:    egress hosts the user approved interactively.
//   - TrustedTools:  ad-hoc tool names (e.g. "wrangler") approved to receive
//     the real user home instead of the ephemeral sandbox guest home, so
//     credentials they save (e.g. `wrangler login`) persist.
//   - PolicyPins:    sha256 of each project policy file the user has trusted,
//     keyed by cleaned absolute path.
type projectGrants struct {
	ProjectPath  string            `json:"project_path"`
	AllowHosts   []string          `json:"allow_hosts,omitempty"`
	TrustedTools []string          `json:"trusted_tools,omitempty"`
	PolicyPins   map[string]string `json:"policy_pins,omitempty"`
}

// hasTrustedTool reports whether tool (case-insensitive) is in the granted
// trusted-tools list for this project.
func (g projectGrants) hasTrustedTool(tool string) bool {
	for _, t := range g.TrustedTools {
		if strings.EqualFold(t, tool) {
			return true
		}
	}
	return false
}

// projectScopeDir identifies the current project for grant/pin storage: the
// nearest package.json root, else the current working directory.
func projectScopeDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if root := findProjectRoot(cwd); root != "" {
		return root
	}
	return cwd
}

func grantsDir(nvxHome string) string {
	return filepath.Join(nvxHome, "grants")
}

func grantKey(scopeDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(scopeDir)))
	return hex.EncodeToString(sum[:])[:16]
}

func grantsPath(nvxHome, scopeDir string) string {
	return filepath.Join(grantsDir(nvxHome), grantKey(scopeDir)+".json")
}

func loadProjectGrants(nvxHome, scopeDir string) projectGrants {
	g := projectGrants{ProjectPath: scopeDir, PolicyPins: map[string]string{}}
	if nvxHome == "" || scopeDir == "" {
		return g
	}
	data, err := os.ReadFile(grantsPath(nvxHome, scopeDir))
	if err != nil {
		return g
	}
	_ = json.Unmarshal(data, &g)
	if g.PolicyPins == nil {
		g.PolicyPins = map[string]string{}
	}
	return g
}

func saveProjectGrants(nvxHome string, g projectGrants) error {
	if err := os.MkdirAll(grantsDir(nvxHome), 0700); err != nil {
		return err
	}
	out, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(grantsPath(nvxHome, g.ProjectPath), out, 0600)
}

// hashPolicyFile returns the hex sha256 of a policy file's contents.
func hashPolicyFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

// persistNetworkAllowHost records a user-approved egress host in the project's
// grant file under nvxHome. It never writes into the project tree, and it can
// only add an allow host — it cannot alter any other policy setting.
func persistNetworkAllowHost(nvxHome, hostPort string) {
	hostPort = strings.TrimSpace(strings.ToLower(hostPort))
	if hostPort == "" || nvxHome == "" {
		return
	}
	scope := projectScopeDir()
	if scope == "" {
		return
	}

	g := loadProjectGrants(nvxHome, scope)
	for _, existing := range g.AllowHosts {
		if strings.EqualFold(strings.TrimSpace(existing), hostPort) {
			return
		}
	}
	g.AllowHosts = append(g.AllowHosts, hostPort)
	g.ProjectPath = scope

	if err := saveProjectGrants(nvxHome, g); err != nil {
		LogWarn("Failed to persist network allow host to grants: %v", err)
		return
	}
	auditLog(nvxHome, "grant_added", map[string]string{"host": hostPort, "project": scope})
}
