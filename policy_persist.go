package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
//   - ReadExecGrants: filesystem ACEs nvx granted for allow_read_exec, recorded
//     so they can be withdrawn when the policy stops asking for them.
type projectGrants struct {
	ProjectPath  string            `json:"project_path"`
	AllowHosts   []string          `json:"allow_hosts,omitempty"`
	TrustedTools []string          `json:"trusted_tools,omitempty"`
	PolicyPins   map[string]string `json:"policy_pins,omitempty"`
	// ReadExecGrants are the access-control entries nvx wrote for
	// isolation.filesystem.allow_read_exec, so it can take them back. See
	// sandbox_read_exec_grants.go.
	ReadExecGrants []readExecGrant `json:"read_exec_grants,omitempty"`
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
	path := grantsPath(nvxHome, scopeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return g
	}
	if uerr := json.Unmarshal(data, &g); uerr != nil {
		// A ledger that does not parse was treated as an empty one and then silently
		// overwritten. That is the worst outcome available: it records filesystem
		// permissions nvx granted, so discarding it strands every permission it named
		// -- invisible to reconciliation and to `grants reset`, removable only with
		// icacls by hand.
		//
		// Keep the file under a name that says what happened, and say so. The
		// permissions still need removing by hand, but there is at least something on
		// disk that names them.
		quarantine := path + ".unreadable"
		if rerr := os.Rename(path, quarantine); rerr == nil {
			LogWarn("This project's grant record could not be read; it has been kept as %s.", quarantine)
			LogWarn("Directory permissions it listed are no longer tracked and must be removed with icacls.")
		} else {
			LogWarn("This project's grant record could not be read: %v", uerr)
		}
		return projectGrants{ProjectPath: scopeDir, PolicyPins: map[string]string{}}
	}
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

	// Written to a temporary file and renamed, so an interrupted write or a full
	// disk cannot leave a half-written file behind. That matters more since this
	// file started tracking filesystem permissions: a truncated ledger does not
	// parse, and a permission nothing has a record of is one nothing can withdraw
	// -- not reconciliation, not `nvx grants reset`, only icacls by hand.
	final := grantsPath(nvxHome, g.ProjectPath)
	tmp := fmt.Sprintf("%s.%d.tmp", final, os.Getpid())
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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

// readGrantsFile loads one grants file by path, for callers walking the grants
// directory rather than resolving a project. Returns nothing if it cannot be
// read: a reset must not abort on one unreadable file.
func readGrantsFile(path string) []readExecGrant {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var g projectGrants
	if err := json.Unmarshal(data, &g); err != nil {
		LogWarn("Skipping an unreadable grant record at %s; any directory permissions it listed are not withdrawn.", path)
		return nil
	}
	return g.ReadExecGrants
}
