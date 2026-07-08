package main

import "strings"

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
