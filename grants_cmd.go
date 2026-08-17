package main

import (
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
		g := loadProjectGrants(nvxHome, scope)
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
			for _, e := range entries {
				if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
					LogWarn("Failed to remove %s: %v", e.Name(), err)
				}
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
