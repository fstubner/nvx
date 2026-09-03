package main

import "errors"

// errSandboxDidNotStart means nvx declined to run the command, or could not
// set up the containment it promised — as opposed to the command running
// inside the sandbox and exiting non-zero itself.
//
// The two were the same thing to every caller, because platformLaunchNative
// returned a bare int and used 1 for both. Measured 2026-09-03: a run that
// refused to start because NVX_HOME was too long for an AF_UNIX socket wrote
//
//	{"event":"run","exit":"1","mode":"sandboxed",...}
//
// to the audit log, byte-identical to an `npm install` that ran fully
// contained and failed on its own terms. Someone reading that log to answer
// "was this command actually contained?" — the question the log exists for —
// could not tell.
//
// A sentinel rather than a wrapped reason, on purpose. Every refusal site
// already reports its own cause to the user, in its own words, usually with
// advice attached; re-deriving those strings for the error would either
// duplicate them or quietly replace them. What was missing was never the
// wording, only the machine-readable fact that the command did not run.
var errSandboxDidNotStart = errors.New("the sandbox did not start")

// sandboxDidNotStart records that a command never ran contained, and returns the
// exit code to give for it.
//
// Every way runNativeSandbox can decline goes through here, so `nvx audit` can
// answer the one question it exists for: was this command actually contained?
// Before this, all of them returned a bare 1 and wrote nothing, so a refusal was
// indistinguishable from a contained command that failed on its own terms --
// and the most likely refusal in practice, an NVX_HOME too long for an AF_UNIX
// socket, happens before the sandbox launcher is even reached, so recording it
// only at the launcher would have missed it.
//
// The reason is a fixed string chosen at the call site, never a rendered error,
// for the reason LogWarn records format strings rather than their output: a
// rendered message can carry a package URL with credentials in it, and this log
// goes to disk. The launcher's own reason is the errSandboxDidNotStart sentinel,
// which carries no runtime data either.
//
// Unconditional, unlike the per-run record behind NVX_TRACE. A refusal is a
// containment decision, and those are logged whether or not tracing is on.
func sandboxDidNotStart(config SandboxConfig, reason string, code int) int {
	auditLog(config.NvxHome, "sandbox_not_started", map[string]string{
		"command": config.Command,
		"reason":  reason,
	})
	return code
}
