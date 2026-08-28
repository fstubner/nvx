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
	// command (e.g. `npx --no-sandbox`). It is NOT honoured: only an explicit
	// `nvx --no-sandbox <cmd>` disables isolation, so nothing can bypass the
	// sandbox by tacking a flag onto a package manager.
	//
	// It is passed on to the command unchanged. Until 2026-08-24 it was also
	// removed, which is a different and much larger claim than refusing to honour
	// it -- see parseShimOptions.
	payloadNoSandbox bool
	// payloadStrict / payloadStandard record --strict/--standard smuggled
	// through the wrapped command's own args.
	//
	// NEITHER is honoured, and both are recorded only so the caller can say why
	// nothing happened. Neither is removed from what the command receives.
	//
	// payloadStandard has never been honoured, for the anti-bypass reason
	// payloadNoSandbox has: it reduces containment, so a dependency's own
	// arguments must not be able to apply it.
	//
	// payloadStrict WAS honoured until 0.5.6, because it only ever adds
	// containment and smuggling it gains an attacker nothing. That was the wrong
	// question: --strict is TypeScript's and ESLint's flag, so `nvx tsc --strict`
	// was silently sandboxed. See shouldContain.
	//
	// This comment has now been wrong in both directions at different times.
	// Check containment.go before trusting it.
	payloadStrict   bool
	payloadStandard bool
	// payloadBareProvider records a `--filesystem-provider` with no "=", which nvx
	// no longer reads a value for. Recorded so the caller can say why it was
	// ignored rather than leaving the developer to wonder which provider ran.
	payloadBareProvider bool
	// strictFlag / standardFlag record a leading `nvx --strict`/`nvx --standard`
	// override for this invocation.
	strictFlag   bool
	standardFlag bool
	args         []string
}

// parseShimOptions reads nvx's own flags out of a wrapped command's arguments.
//
// It NOTICES them and does not remove them. That distinction is the whole of
// this function and it was the other way round until 2026-08-24, which made nvx
// silently change what programs it wraps were asked to do:
//
//	nvx npx tsc --strict          -> tsc ran WITHOUT --strict
//	nvx npx electron --no-sandbox -> electron never saw --no-sandbox
//	nvx node app.js -- --strict   -> stripped past the end-of-options separator
//	nvx node app.js --filesystem-provider x keep -> "x" eaten as well
//
// Those names are not nvx's to take. `--strict` belongs to TypeScript, ESLint and
// a dozen others; `--no-sandbox` belongs to Chromium and everything embedding it.
// Removing them produced a wrong answer with no error -- a non-strict typecheck
// reported as a strict one -- and it happened even for uncontained runs, where
// nvx has no security interest at all.
//
// Noticing is still necessary, and is what the anti-bypass rule rests on: a
// weakening flag smuggled through a package manager's arguments must not take
// effect (see shouldContain and payloadNoSandbox). Passing it through does not
// weaken that -- nvx decides what to honour from these fields, not from the
// child's argv.
//
// Scanning stops at "--". After the end-of-options separator every argument
// belongs to the program, by a convention older than any of these flags.
func parseShimOptions(args []string) shimOptions {
	opts := shimOptions{args: args}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		switch {
		case arg == "--no-sandbox":
			opts.payloadNoSandbox = true
		case arg == "--strict":
			opts.payloadStrict = true
		case arg == "--standard":
			opts.payloadStandard = true
		case strings.HasPrefix(arg, "--filesystem-provider="):
			opts.filesystemProvider = strings.TrimPrefix(arg, "--filesystem-provider=")
			// Only the "=" spelling, which is the one README documents. The
			// separated form had to consume the following argument to find its
			// value, and consuming an argument that belongs to the program is the
			// bug above in its most damaging form: it removed a filename.
			opts.payloadBareProvider = false
		case arg == "--filesystem-provider":
			opts.payloadBareProvider = true
		}
	}
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
