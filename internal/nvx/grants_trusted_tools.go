package nvx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// authLikeSubcommands are the ad-hoc-tool subcommands that plausibly need to
// persist credentials/config across runs — exactly the spec's own examples
// (wrangler login, gh auth, aws configure). Intentionally narrow: the goal is
// prompting rarely, not on every never-before-seen npx invocation.
var authLikeSubcommands = map[string]bool{
	"login": true, "auth": true, "configure": true,
}

// nonFlagTokens returns args' tokens that are not flags (or flag values), in
// order. E.g. ["-y", "wrangler", "login"] -> ["wrangler", "login"].
func nonFlagTokens(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if flagTakesValue(arg) && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

// stripVersionSuffix removes a trailing "@version" from a package spec,
// correctly handling scoped packages ("@scope/pkg@1.0" -> "@scope/pkg").
func stripVersionSuffix(spec string) string {
	if spec == "" {
		return spec
	}
	prefix := ""
	rest := spec
	if strings.HasPrefix(rest, "@") {
		prefix = "@"
		rest = rest[1:]
	}
	if idx := strings.Index(rest, "@"); idx != -1 {
		rest = rest[:idx]
	}
	return prefix + rest
}

// trustedToolCandidate inspects an ad-hoc-tool invocation (npx/bunx)
// and returns the bare tool name and whether its subcommand looks like it
// needs to persist credentials/config across runs (an auth-shaped subcommand).
// Returns ("", false) for any command that is not an ad-hoc-tool executor.
func trustedToolCandidate(cmd string, args []string) (tool string, wantsPersistence bool) {
	if !executorCommands[strings.ToLower(cmd)] {
		return "", false
	}
	toks := nonFlagTokens(args)
	if len(toks) == 0 {
		return "", false
	}
	tool = strings.ToLower(stripVersionSuffix(toks[0]))
	if len(toks) < 2 {
		return tool, false
	}
	return tool, authLikeSubcommands[strings.ToLower(toks[1])]
}

// ensureTrustedToolGrant decides whether toolName gets a persistent per-project
// profile for this and future sandboxed runs, so logins and config persist.
// It returns true when the tool is already granted, or when NVX_TRUST_YES
// approves it, even if that approval could not be recorded. It returns false
// when nvx refuses, or toolName or nvxHome is empty, and the caller then stops
// the run. It never asks. The decision is recorded by `nvx trust --tool`, in the
// project's grant file under nvxHome, never in the project tree. The profile is
// always contained under nvxHome and never touches the real home, so this works
// uniformly on all platforms.
func ensureTrustedToolGrant(nvxHome, toolName string) bool {
	if toolName == "" || nvxHome == "" {
		return false
	}
	scope := projectScopeDir()
	if scope == "" {
		return false
	}

	if toolTrusted(nvxHome, scope, toolName) {
		return true
	}

	// A trust decision, so -y and NVX_YES do not approve it. The grant persists
	// for every later run.
	command := "nvx trust --tool <name>"
	if label := safePackageLabel(toolName); label != "" {
		command = "nvx trust --tool " + label
	}
	if !approveWidening(wideningRequest{
		what:    fmt.Sprintf("a persistent profile for %s in this project", toolName),
		refusal: fmt.Sprintf("nvx refused to let %q keep a persistent profile in this project, which would keep its logins and settings between runs. That has not been trusted here.", toolName),
		command: command,
	}) {
		auditLog(nvxHome, "trusted_tool_denied", map[string]string{"tool": toolName, "project": scope})
		return false
	}

	if err := recordTrustedTool(nvxHome, scope, toolName); err != nil {
		LogWarn("Failed to persist trusted-tool grant: %v", err)
		auditLog(nvxHome, "trusted_tool_grant_persist_failed", map[string]string{"tool": toolName, "project": scope})
		// The approval stands for this run even though it could not be
		// recorded. A future run decides again.
		return true
	}
	auditLog(nvxHome, "trusted_tool_granted", map[string]string{"tool": toolName, "project": scope, "by": "nvx_trust_yes"})
	return true
}

// toolTrusted reports whether tool may keep a persistent profile in the project
// at scope. A grant is recorded under the project's real path, so one recorded
// from a terminal that spells the folder differently, in letter case or through
// a symlink, still counts. One an earlier version recorded under the path as
// spelled counts too.
func toolTrusted(nvxHome, scope, tool string) bool {
	if loadProjectGrants(nvxHome, scope).hasTrustedTool(tool) {
		return true
	}
	real := canonicalPath(scope)
	return real != filepath.Clean(scope) && loadProjectGrants(nvxHome, real).hasTrustedTool(tool)
}

// recordTrustedTool grants tool a persistent profile in the project at scope,
// under the project's real path. The ledger is re-read under its lock and only
// this entry added, so a concurrent nvx's additions survive.
func recordTrustedTool(nvxHome, scope, tool string) error {
	return updateProjectGrants(nvxHome, canonicalPath(scope), func(g *projectGrants) error {
		if !g.hasTrustedTool(tool) {
			g.TrustedTools = append(g.TrustedTools, tool)
		}
		return nil
	})
}
