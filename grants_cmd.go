package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// formatProjectGrants renders a projectGrants as a human-readable summary.
func formatProjectGrants(g projectGrants) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Grants for %s\n", g.ProjectPath)
	if len(g.AllowHosts) == 0 {
		b.WriteString("  egress hosts: (none)\n")
	} else {
		b.WriteString("  egress hosts:\n")
		for _, h := range g.AllowHosts {
			fmt.Fprintf(&b, "    - %s\n", h)
		}
	}
	if len(g.TrustedTools) == 0 {
		b.WriteString("  trusted tools: (none)\n")
	} else {
		b.WriteString("  trusted tools (persistent profile):\n")
		for _, t := range g.TrustedTools {
			fmt.Fprintf(&b, "    - %s\n", t)
		}
	}
	if len(g.PolicyPins) == 0 {
		b.WriteString("  trusted project policy files: (none)\n")
	} else {
		b.WriteString("  trusted project policy files:\n")
		for path := range g.PolicyPins {
			fmt.Fprintf(&b, "    - %s\n", path)
		}
	}
	// Listed because these are the only grants that write something outside nvx's
	// own storage. `nvx grants reset` withdraws them.
	if len(g.ReadExecGrants) == 0 {
		b.WriteString("  read/execute directories: (none)\n")
	} else {
		b.WriteString("  read/execute directories (filesystem permissions nvx granted):\n")
		for _, r := range g.ReadExecGrants {
			fmt.Fprintf(&b, "    - %s\n", r.Path)
		}
	}
	return b.String()
}

// runGrants implements `nvx grants list` and `nvx grants reset [--all]`.
func runGrants(args []string, nvxHome string) int {
	if len(args) == 0 {
		LogError("Usage: nvx grants list | nvx grants reset [--all]")
		return 1
	}

	switch args[0] {
	case "list":
		scope := projectScopeDir()
		if scope == "" {
			LogError("Could not determine the current project.")
			return 1
		}
		// Read without repairing. loadProjectGrants quarantines a record it cannot
		// parse, which is right on a path that is about to write one back -- but
		// `grants list` is a question, and answering it should not rename a file on
		// disk. Reported by an acceptance pass after `list` renamed a corrupt record.
		g := projectGrants{ProjectPath: scope, PolicyPins: map[string]string{}}
		if data, rerr := os.ReadFile(grantsPath(nvxHome, scope)); rerr == nil {
			if uerr := json.Unmarshal(data, &g); uerr != nil {
				LogWarn("This project's grant record could not be read, so this list may be incomplete.")
				g = projectGrants{ProjectPath: scope, PolicyPins: map[string]string{}}
			}
		}
		fmt.Print(formatProjectGrants(g))
		return 0

	case "reset":
		all := false
		for _, a := range args[1:] {
			if a == "--all" {
				all = true
			}
		}
		if all {
			dir := grantsDir(nvxHome)
			entries, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					LogSuccess("No grants to reset.")
					return 0
				}
				LogError("Failed to read grants directory: %v", err)
				return 1
			}
			// Withdraw the filesystem permissions before dropping the record of
			// them. Deleting the ledger first would leave the access-control entries
			// on disk with nothing left that knows they exist -- which is what
			// "reset" was quietly doing before.
			revoked, failed, unreadable := 0, 0, 0
			for _, e := range entries {
				path := filepath.Join(dir, e.Name())
				// Preserved records of permissions nvx can no longer account for.
				// Deleting one would destroy the only trace that they exist.
				if strings.Contains(e.Name(), ".unreadable") {
					unreadable++
					continue
				}
				grants, ok := readGrantsFile(path)
				if !ok {
					// Could not be read, so its permissions cannot be withdrawn and
					// its contents are unknown. Keep the file: removing it is exactly
					// the loss this whole ledger exists to prevent.
					LogWarn("Could not read %s; it was left in place, and any directory permissions it lists are not withdrawn.", e.Name())
					unreadable++
					continue
				}
				r, f := revokeAllReadExecGrants(grants, revokeSandboxReadExec)
				revoked += r
				failed += f
				if f > 0 {
					// Keep the record of whatever could not be withdrawn; removing it
					// would strand those entries permanently.
					continue
				}
				if err := os.Remove(path); err != nil {
					LogWarn("Failed to remove %s: %v", e.Name(), err)
				}
			}
			if revoked > 0 {
				LogInfo("Withdrew %d read/execute directory permission(s).", revoked)
			}
			if failed > 0 {
				LogWarn("Could not withdraw %d permission(s); their records were kept so a later reset can retry.", failed)
			}
			if unreadable > 0 {
				LogWarn("%d grant record(s) could not be read and were kept; directory permissions they list must be removed with icacls.", unreadable)
			}
			// "Reset all project grants" has to mean all of them. A record left in
			// place -- unreadable, or naming a permission that could not be
			// withdrawn -- means a filesystem permission nvx granted is still on
			// disk. This printed that success and exited 0 two lines after warning
			// it had skipped a record, so a cleanup script had no way to tell the
			// difference. The single-project form already reported this correctly.
			if failed > 0 || unreadable > 0 {
				LogError("Did not reset all project grants: %d record(s) were left in place, and the permissions they name were not withdrawn.", failed+unreadable)
				return 1
			}
			LogSuccess("Reset all project grants.")
			return 0
		}

		scope := projectScopeDir()
		if scope == "" {
			LogError("Could not determine the current project.")
			return 1
		}
		path := grantsPath(nvxHome, scope)
		grants, readable := readGrantsFile(path)
		if !readable {
			if _, statErr := os.Stat(path); statErr == nil {
				LogError("This project's grant record could not be read; it was left in place rather than deleted.")
				LogInfo("Any directory permissions it lists must be removed with icacls.")
				return 1
			}
		}
		if revoked, failed := revokeAllReadExecGrants(grants, revokeSandboxReadExec); revoked > 0 || failed > 0 {
			if revoked > 0 {
				LogInfo("Withdrew %d read/execute directory permission(s).", revoked)
			}
			if failed > 0 {
				LogWarn("Could not withdraw %d permission(s); the record was kept so a later reset can retry.", failed)
				return 1
			}
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				LogSuccess("No grants recorded for this project.")
				return 0
			}
			LogError("Failed to remove grant file: %v", err)
			return 1
		}
		LogSuccess("Reset grants for this project.")
		return 0

	default:
		LogError("Unknown grants subcommand: %s", args[0])
		return 1
	}
}
