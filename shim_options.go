package main

import (
	"os"
	"strings"
)

var noSandboxFlag bool

type shimOptions struct {
	filesystemProvider string
	// payloadNoSandbox records a --no-sandbox smuggled through the wrapped
	// command (e.g. `npx --no-sandbox`). It is stripped but NOT honored: only an
	// explicit `nvx --no-sandbox <cmd>` disables isolation, so nothing can bypass
	// the sandbox by tacking a flag onto a package manager.
	payloadNoSandbox bool
	args             []string
}

func parseShimOptions(args []string) shimOptions {
	opts := shimOptions{args: args}
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-sandbox":
			opts.payloadNoSandbox = true
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

func shouldSandbox(cmdName string, policy Policy, opts shimOptions) bool {
	// Only a leading `nvx --no-sandbox ...` (noSandboxFlag) disables isolation;
	// a --no-sandbox smuggled into the wrapped command's args does not.
	if noSandboxFlag {
		return false
	}
	if inSandboxSession() {
		return false
	}
	if os.Getenv("NVX_SANDBOX") == "1" || os.Getenv("NVX_SANDBOX") == "true" {
		return false
	}
	if !policy.Isolation.Enabled {
		return false
	}
	provider := runtimeForShim(cmdName)
	for _, c := range provider.ShimCommands() {
		if strings.EqualFold(c, cmdName) {
			return true
		}
	}
	return isProjectBinCommand(cmdName)
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
