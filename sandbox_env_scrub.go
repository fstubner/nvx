package main

import (
	"sort"
	"strconv"
	"strings"
)

// What containment does to the environment, and how a project asks for a
// variable back.
//
// scrubEnvironment keeps an allowlist of 11 keys on Windows, 7 on Unix, and drops everything else, so
// that a package's install script cannot read the secrets sitting in the shell
// that invoked it. That is the point of the sandbox and it is not changing.
//
// What was wrong is that it happened in total silence. Measured on a Windows
// machine on 2026-09-03: 107 variables outside, 48 inside, and nothing printed.
// A tool that checks CI to suppress interactive prompts starts prompting; a
// build reading NODE_ENV=production quietly emits a development bundle. Neither
// fails, so there is nothing to search for, and the only workaround available
// was --no-sandbox -- turning containment off to get a variable through.
//
// Two things follow: say when a variable that changes how tools behave has been
// removed, and give a project a way to name one it needs.

// envScrubResult is what filtering the environment did, so the caller can report
// it rather than dropping variables silently.
type envScrubResult struct {
	// Env is the filtered environment, ready to hand to the contained process.
	Env []string
	// Dropped names every variable removed, sorted. Names only: a value here is
	// exactly the secret the scrub exists to withhold.
	Dropped []string
	// Refused names variables the policy asked to pass through that a sensitive
	// prefix blocked anyway. Always reported -- someone wrote it down and it did
	// not happen.
	Refused []string
}

// notableEnvKeys are variables whose absence changes how a build or test run
// behaves rather than merely being absent.
//
// The full dropped list is large and almost entirely Windows furniture --
// COMPUTERNAME, PROCESSOR_LEVEL, ONEDRIVE and fifty more -- which no build reads
// and which would make a line print on every contained run for no purpose. These
// are the ones that alter behaviour when they vanish.
//
// This is a curated list and therefore incomplete by construction: a project's
// own variable (MY_API_URL) is not here and is still dropped without a line on
// screen. The complete list goes to the audit log on every contained run, which
// is where to look when something behaves differently inside the sandbox.
var notableEnvKeys = map[string]bool{
	"CI":          true,
	"NODE_ENV":    true,
	"DEBUG":       true,
	"FORCE_COLOR": true,
	"NO_COLOR":    true,
}

// notableEnvPrefixes catch families rather than single names.
//
// Proxy variables are deliberately absent. nvx sets its own (applyProxyEnv) and
// removes the host's on purpose (stripProxyEnv), so reporting them as casualties
// would be telling the user that something broke when nvx replaced them.
var notableEnvPrefixes = []string{"NPM_CONFIG_"}

// notableDropped returns the dropped variables worth putting on screen.
func notableDropped(dropped []string) []string {
	var out []string
	for _, name := range dropped {
		upper := strings.ToUpper(name)
		if notableEnvKeys[upper] {
			out = append(out, name)
			continue
		}
		for _, prefix := range notableEnvPrefixes {
			if strings.HasPrefix(upper, prefix) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// passEnvSet turns a policy's isolation.environment.allow into the set
// scrubEnvironment consults, keyed the way it compares (upper case).
func passEnvSet(passEnv []string) map[string]bool {
	if len(passEnv) == 0 {
		return nil
	}
	set := make(map[string]bool, len(passEnv))
	for _, name := range passEnv {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		set[strings.ToUpper(name)] = true
	}
	return set
}

// refusedPassEnv returns the names a policy asked to pass through that a
// sensitive prefix blocks.
//
// The prefixes win, and a project-local file cannot overrule them. Letting
// isolation.environment.allow name AWS_SECRET_ACCESS_KEY would turn a checked-in
// file into a way to hand a cloud credential to whatever a package's install
// script runs -- the exact transfer the scrub exists to prevent. Refusing
// loudly, rather than quietly honouring it, is the whole point.
func refusedPassEnv(passEnv []string) []string {
	var refused []string
	for _, name := range passEnv {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		upper := strings.ToUpper(name)
		for _, prefix := range sensitiveEnvPrefixes {
			if strings.HasPrefix(upper, prefix) {
				refused = append(refused, name)
				break
			}
		}
	}
	sort.Strings(refused)
	return refused
}

// reportEnvScrub tells the user what containment removed.
//
// The full list always goes to the audit log; the terminal gets a line only when
// a variable that changes tool behaviour was among them, so an ordinary run stays
// quiet. Names, never values.
func reportEnvScrub(nvxHome string, res envScrubResult) {
	if len(res.Dropped) > 0 {
		auditLog(nvxHome, "env_scrubbed", map[string]string{
			"count":   strconv.Itoa(len(res.Dropped)),
			"dropped": strings.Join(res.Dropped, ","),
		})
	}

	// Asked for and not delivered: always worth saying, however ordinary the run.
	for _, name := range res.Refused {
		LogWarn("isolation.environment.allow names %s, which holds a credential by convention; it was not passed in.", name)
	}

	notable := notableDropped(res.Dropped)
	if len(notable) == 0 {
		return
	}
	LogWarn("Containment removed %d environment variables, including %s.",
		len(res.Dropped), strings.Join(notable, ", "))
	LogInfo("Tools that read them behave differently inside the sandbox. To pass one through, add it to " +
		"isolation.environment.allow in .nvx-policy.json; the full list is in the audit log.")
}
