package main

import (
	"fmt"
	"strings"
)

// authLikeSubcommands are the ad-hoc-tool subcommands that plausibly need to
// persist credentials/config to the user's real home — exactly the spec's own
// examples (wrangler login, gh auth, aws configure). Intentionally narrow: the
// goal is prompting rarely, not on every never-before-seen npx invocation.
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

// trustedToolCandidate inspects an ad-hoc-tool invocation (npx/bunx/uvx/pyx)
// and returns the bare tool name and whether its subcommand looks like it
// needs to persist credentials to the real home. Returns ("", false) for any
// command that is not an ad-hoc-tool executor.
func trustedToolCandidate(cmd string, args []string) (tool string, wantsRealHome bool) {
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

// ensureTrustedToolGrant decides whether toolName should receive the real
// user home directory for this sandboxed run. Returns true if it should
// (either already granted, or the user just approved it now); false if
// denied, unsupported on this platform, or toolName is empty. Prompts at
// most once per project per tool; the decision persists in the project's
// grant file under nvxHome, never in the project tree.
func ensureTrustedToolGrant(nvxHome, toolName string) bool {
	if toolName == "" {
		return false
	}
	scope := projectScopeDir()
	if scope == "" {
		return false
	}

	g := loadProjectGrants(nvxHome, scope)
	if g.hasTrustedTool(toolName) {
		return true
	}

	if !realHomeSwapSupported() {
		LogInfo("%q could persist credentials to your real home on Linux/macOS; the Windows sandbox can't grant that safely yet. Run without isolation for this command: nvx --no-sandbox <cmd> ...", toolName)
		return false
	}

	msg := fmt.Sprintf("%q wants access to your real home directory to save credentials/config (e.g. login tokens). Allow?", toolName)
	if !PromptYesNo(msg) {
		auditLog(nvxHome, "trusted_tool_denied", map[string]string{"tool": toolName, "project": scope})
		return false
	}

	g.TrustedTools = append(g.TrustedTools, toolName)
	g.ProjectPath = scope
	if err := saveProjectGrants(nvxHome, g); err != nil {
		LogWarn("Failed to persist trusted-tool grant: %v", err)
		return false
	}
	auditLog(nvxHome, "trusted_tool_granted", map[string]string{"tool": toolName, "project": scope})
	return true
}