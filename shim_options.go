package main

import "strings"

var noSandboxFlag bool

type shimOptions struct {
	noSandbox          bool
	filesystemProvider string
	args               []string
}

func parseShimOptions(args []string) shimOptions {
	opts := shimOptions{args: args}
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-sandbox":
			opts.noSandbox = true
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
	if opts.noSandbox || noSandboxFlag {
		return false
	}
	if inSandboxSession() {
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
