package main

import (
	"os"
	"strings"
)

var noSandboxFlag bool
var strictFlag bool
var standardFlag bool

type shimOptions struct {
	filesystemProvider string
	// payloadNoSandbox records a --no-sandbox smuggled through the wrapped
	// command (e.g. `npx --no-sandbox`). It is stripped but NOT honored: only an
	// explicit `nvx --no-sandbox <cmd>` disables isolation, so nothing can bypass
	// the sandbox by tacking a flag onto a package manager.
	payloadNoSandbox bool
	// payloadStrict / payloadStandard record --strict/--standard smuggled
	// through the wrapped command's own args.
	//
	// They are treated differently, and this comment said otherwise for long
	// enough to mislead a reviewer: payloadStrict IS honoured (shouldContain),
	// because --strict only ever adds containment and there is nothing to gain
	// by sneaking in a flag that sandboxes you harder. payloadStandard is
	// stripped and NOT honoured, for the anti-bypass reason payloadNoSandbox
	// has: it reduces containment, so a dependency's own arguments must not be
	// able to apply it.
	payloadStrict   bool
	payloadStandard bool
	// strictFlag / standardFlag record a leading `nvx --strict`/`nvx --standard`
	// override for this invocation.
	strictFlag   bool
	standardFlag bool
	args         []string
}

func parseShimOptions(args []string) shimOptions {
	opts := shimOptions{args: args}
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-sandbox":
			opts.payloadNoSandbox = true
		case arg == "--strict":
			opts.payloadStrict = true
		case arg == "--standard":
			opts.payloadStandard = true
		case strings.HasPrefix(arg, "--filesystem-provider="):
			opts.filesystemProvider = strings.TrimPrefix(arg, "--filesystem-provider=")
		case arg == "--filesystem-provider" && i+1 < len(args):
			opts.filesystemProvider = args[i+1]
			i++
		default:
			filtered = append(filtered, arg)
		}
	}
	opts.args = filtered
	return opts
}

func shouldSandbox(cmdName string, args []string, policy Policy, opts shimOptions) bool {
	// Only a leading `nvx --no-sandbox ...` (noSandboxFlag) disables isolation;
	// a --no-sandbox smuggled into the wrapped command's args does not.
	if noSandboxFlag {
		return false
	}
	if inSandboxSession() {
		return false
	}
	if os.Getenv("NVX_SANDBOX") == "1" || os.Getenv("NVX_SANDBOX") == "true" {
		// The marker is an inherited environment variable, so an ambient
		// NVX_SANDBOX=1 -- exported in a shell profile, left in a CI config, written
		// by a malicious postinstall -- would otherwise disable containment for every
		// later run, silently and permanently. Honour it only when we cannot prove it
		// is false.
		if containmentDisproved() {
			LogWarn("NVX_SANDBOX is set, but this process is not inside a sandbox; ignoring it and containing anyway.")
			LogInfo("Something set NVX_SANDBOX outside a sandbox. If that was not deliberate, check your shell profile and CI environment.")
		} else {
			return false
		}
	}
	if !policy.Isolation.Enabled {
		return false
	}
	if !isWrappedCommand(cmdName) {
		return false
	}

	class := classifyInvocation(cmdName, args)
	level := policy.IsolationLevel()
	return shouldContain(class, level, opts)
}

// isWrappedCommand reports whether nvx wraps this command name -- a runtime's
// own shim (npm, npx, node, bun) or a project-bin command routed through one.
//
// Split out of shouldSandbox so a run trace can report "not a wrapped command"
// as the reason an invocation was not contained, without restating the test.
func isWrappedCommand(cmdName string) bool {
	if isProjectBinCommand(cmdName) {
		return true
	}
	for _, c := range runtimeForShim(cmdName).ShimCommands() {
		if strings.EqualFold(c, cmdName) {
			return true
		}
	}
	return false
}

func allShimCommands() []string {
	if len(shimToRuntime) == 0 {
		initRuntimeRegistry()
	}
	seen := map[string]bool{}
	var cmds []string
	for _, p := range Providers {
		for _, c := range p.ShimCommands() {
			if !seen[c] {
				seen[c] = true
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}
