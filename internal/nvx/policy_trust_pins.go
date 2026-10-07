package nvx

import (
	"path/filepath"
)

// Where trust in a project policy file is recorded.
//
// A pin lived only in the ledger of the project a command ran in, keyed by the
// path as that shell spelled it. Once trust became a command a person runs in
// their own terminal, two things went wrong with that.
//
// A monorepo's root .nvx-policy.json applies to every workspace package below
// it, and each package is a project of its own. Trusted at the root, the file was
// still refused in packages/a, so every command there stopped with exit 77.
//
// The ledger and the pin are keyed by the path as Getwd returns it, which on
// Windows keeps the case it was typed in and on Unix goes through any symlink in
// $PWD. Measured on Windows: `nvx trust` run from c:\...\proj said it trusted
// the file, and a run from C:\...\proj was still refused.
//
// So a pin is now recorded in the ledger of the folder that holds the file,
// under the file's real path, and it counts wherever the file applies. A pin an
// earlier version recorded in the project's own ledger still counts.

// canonicalPath is path with symlinks resolved and, on Windows, each part
// in the case it has on disk.
func canonicalPath(path string) string {
	clean := filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return real
	}
	return clean
}

// policyPinned reports whether the policy file at path is trusted at hash.
// scopeGrants is the ledger of the project the command runs in.
func policyPinned(nvxHome string, scopeGrants projectGrants, path, hash string) bool {
	if hash == "" {
		return false
	}
	if scopeGrants.PolicyPins[filepath.Clean(path)] == hash {
		return true
	}
	canonical := canonicalPath(path)
	if scopeGrants.PolicyPins[canonical] == hash {
		return true
	}
	ledger := filepath.Dir(canonical)
	if ledger == filepath.Clean(scopeGrants.ProjectPath) {
		return false
	}
	return loadProjectGrants(nvxHome, ledger).PolicyPins[canonical] == hash
}

// recordPolicyPins trusts each policy file at its hash, in the ledger of the
// folder that holds it.
func recordPolicyPins(nvxHome string, pins map[string]string) error {
	byLedger := map[string]map[string]string{}
	for path, hash := range pins {
		canonical := canonicalPath(path)
		ledger := filepath.Dir(canonical)
		if byLedger[ledger] == nil {
			byLedger[ledger] = map[string]string{}
		}
		byLedger[ledger][canonical] = hash
	}
	for ledger, entries := range byLedger {
		// Re-read under the ledger's lock and add only these pins, so a
		// concurrent nvx's entries survive.
		if err := updateProjectGrants(nvxHome, ledger, func(g *projectGrants) error {
			for path, hash := range entries {
				g.PolicyPins[path] = hash
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
