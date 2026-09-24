package nvx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// loadProjectGrants reads a project's ledger without its lock, for callers that
// only read it. See loadProjectGrantsWithLock.
func loadProjectGrants(nvxHome, scopeDir string) projectGrants {
	return loadProjectGrantsWithLock(nvxHome, scopeDir, false)
}

// loadProjectGrantsWithLock reads a project's ledger. lockHeld says whether the
// caller already holds the project's ledger lock, as updateProjectGrants does;
// the lock is not re-entrant, so taking it again there would deadlock.
func loadProjectGrantsWithLock(nvxHome, scopeDir string, lockHeld bool) projectGrants {
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
		quarantineUnreadableGrants(nvxHome, scopeDir, path, data, uerr, lockHeld)
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
	// A unique temp name, not one built from the PID: two saves in one process
	// -- measured, sixteen goroutines through updateProjectGrants before it took
	// a lock -- collided on the same temp file and failed with "being used by
	// another process". The lock makes that sequence impossible now; this makes
	// the save safe on its own as well.
	f, err := os.CreateTemp(filepath.Dir(final), filepath.Base(final)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(out); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// hashPolicyBytes is the pin: the hex sha256 of a policy file's raw bytes,
// exactly as read, before any byte-order mark is stripped.
//
// One function so the production path and the tests that seed a pin cannot
// disagree about what a pin is made of. Every pin already written to disk was
// computed this way, so the input must stay the raw bytes.
func hashPolicyBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// hashPolicyFile hashes a policy file by path.
//
// Production reads the file and hashes what it parsed in one step -- see
// readAndHashProjectPolicyFile -- so this remains for tests that need to compute
// the expected pin for a file they just wrote. Keeping it delegating to
// hashPolicyBytes is what stops those tests drifting into their own definition of
// a pin and passing against a product that computes a different one.
func hashPolicyFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return hashPolicyBytes(data), true
}

// Nothing writes AllowHosts into the grants store any more.
//
// persistNetworkAllowHost lived here and was called from the egress prompt, so
// answering one "allow outbound connection to X?" granted that host for ever.
// Approving is now for the run in progress only; the durable form is an entry in
// isolation.network.allow_hosts, which is a decision someone wrote down rather
// than one the contained process asked for.
//
// The READ side is deliberately untouched: loadProjectGrants still returns
// AllowHosts and LoadPolicy still merges them, so hosts persisted by an earlier
// version keep working and stay visible to `nvx grants list` and removable by
// `nvx grants reset`. Dropping the reader would have stranded them — granted, in
// force, and no longer listed.

// readGrantsFile loads one grants file by path, for callers walking the grants
// directory rather than resolving a project. Returns nothing if it cannot be
// read: a reset must not abort on one unreadable file.
// Returns ok=false when the file cannot be read or parsed, which is NOT the same
// as holding no grants. Conflating them let `grants reset --all` delete a record
// it had just reported it could not act on, destroying the only trace of
// permissions still on disk -- and report success while doing it.
func readGrantsFile(path string) (grants []readExecGrant, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var g projectGrants
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, false
	}
	return g.ReadExecGrants, true
}

// quarantinePath returns a free name to preserve an unreadable record under.
//
// Numbered rather than fixed: os.Rename replaces an existing target on Windows,
// so a second unreadable record silently destroyed the first -- along with the
// only record of whatever permissions that one named.
// quarantineUnreadableGrants moves an unparseable ledger aside, under the
// project's lock, and only if the file is still the one that failed to parse.
//
// It used to rename whatever sat at path, from any reader, unlocked. A reader
// that read a damaged ledger, then lost the CPU while updateProjectGrants (which
// holds the lock) moved that ledger aside and saved a good one, would then move
// the good one aside too: the permissions just recorded became untracked, which
// is the loss quarantining exists to prevent. Re-reading under the lock and
// comparing bytes means only the file that was actually unreadable moves.
func quarantineUnreadableGrants(nvxHome, scopeDir, path string, seen []byte, parseErr error, lockHeld bool) {
	if !lockHeld {
		unlock, err := lockProjectGrants(nvxHome, scopeDir)
		if err != nil {
			LogWarn("This project's grant record could not be read: %v", parseErr)
			return
		}
		defer unlock()
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, seen) {
			return // already moved aside, or replaced by a good one
		}
	}
	quarantine := quarantinePath(path)
	if rerr := os.Rename(path, quarantine); rerr == nil {
		LogWarn("This project's grant record could not be read; it has been kept as %s.", quarantine)
		LogWarn("Directory permissions it listed are no longer tracked and must be removed with icacls.")
	} else {
		LogWarn("This project's grant record could not be read: %v", parseErr)
	}
}

// quarantinePath names the place an unreadable ledger is kept. Unique by time,
// not by probing for a free name: the probe and the rename were two steps, so
// two processes could pick the same name, and os.Rename replaces an existing
// file on Windows -- one kept copy would silently overwrite the other. Every
// name still contains ".unreadable", which `nvx grants reset` counts.
func quarantinePath(path string) string {
	return fmt.Sprintf("%s.unreadable.%s", path, time.Now().UTC().Format("20060102T150405.000000000"))
}
