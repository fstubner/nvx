package nvx

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Recording how each pre-install check was answered.
//
// -y, --agent-mode and NVX_YES approve the typosquat, release-age,
// install-script and advisory prompts without a word. Measured:
// `NVX_AGENT_MODE=1 nvx npm install reakt lodash@4.17.15` installed both, printed
// the advisory list and nothing about the typosquat, and `nvx audit` afterwards
// said "No audit records yet". The log is documented as showing prompts and how
// they were answered, and run_trace.go says security decisions are written
// unconditionally. For a check that was waved through, neither held.
//
// So every one of these checks now writes an event however it was answered, and
// an approval nobody was asked about also prints one line to stderr, which -q
// does not hide. Events are written whatever NVX_TRACE says: they are decisions,
// not run records.
//
//	check_approved  the check would have stopped the install and it went ahead
//	check_refused   the check stopped the install
//
// `check` names the check, `by` says who answered: yes_flag, agent_mode,
// nvx_yes (approved without asking), prompt (a person answered),
// non_interactive (nobody was there to answer) or policy (no prompt exists).
const (
	checkTyposquat          = "typosquat"
	checkReleaseAge         = "release_age"
	checkInstallScripts     = "install_scripts"
	checkVulnerability      = "vulnerability"
	checkRegistryLookup     = "registry_unreachable"
	checkOSVLookup          = "osv_unreachable"
	checkBlockedPackage     = "blocked_package"
	checkEnforceNoScripts   = "enforce_ignore_scripts"
	checkGlobalInstall      = "global_install"
	checkLockfileSource     = "lockfile_mismatch"
	checkResolution         = "resolution_failed"
	checkLockfileUnreadable = "lockfile_unreadable"
	checkPublicRegistryOnly = "public_registry_checks"
	answeredByPolicy        = "policy"
	answeredByRegistry      = "registry"
	answeredByPrompt        = "prompt"
	answeredNonInteractive  = "non_interactive"
)

// checkInfo describes one check outcome for the log and for the stderr line.
type checkInfo struct {
	check   string
	pkg     string
	version string
	detail  string // short, for the audit record
	what    string // for the stderr line when nobody was asked
}

// autoApprovalSource reports which switch answers prompts without asking, or ""
// when none is set. It mirrors the first two tests in PromptYesNo, in the same
// order, so the two cannot disagree about whether a prompt was asked.
func autoApprovalSource() (token, label string) {
	switch {
	case agentModeFlag && yesFlag:
		return "agent_mode", "--agent-mode"
	case yesFlag:
		return "yes_flag", "-y"
	case os.Getenv("NVX_YES") == "true" || os.Getenv("NVX_YES") == "1":
		return "nvx_yes", "NVX_YES"
	}
	return "", ""
}

// nobodyIsHere mirrors the non-interactive tests in PromptYesNo.
func nobodyIsHere() bool {
	return os.Getenv("NVX_NONINTERACTIVE") == "true" || os.Getenv("NVX_NONINTERACTIVE") == "1" || !stdinIsInteractive()
}

func recordCheck(nvxHome, event string, c checkInfo, by string) {
	fields := map[string]string{"check": c.check, "by": by}
	if c.pkg != "" {
		fields["package"] = c.pkg
	}
	if c.version != "" {
		fields["version"] = c.version
	}
	if c.detail != "" {
		fields["detail"] = c.detail
	}
	auditLog(nvxHome, event, fields)
}

// recordCheckRefused is for a refusal that has no prompt: a blocklist entry, or
// install scripts under enforce_ignore_scripts.
func recordCheckRefused(nvxHome string, c checkInfo) {
	recordCheck(nvxHome, "check_refused", c, answeredByPolicy)
}

// askCheck puts a pre-install question and records the answer.
//
// remedy is the advice printed when nobody is there to answer: the narrowest
// policy line that settles this one check, with the blanket switches last.
func askCheck(nvxHome string, c checkInfo, message, remedy string) bool {
	if token, label := autoApprovalSource(); token != "" {
		recordCheck(nvxHome, "check_approved", c, token)
		LogWarn("Approved without asking (%s): %s", label, c.what)
		return true
	}
	hint := remedy
	if hint == "" {
		hint = genericDenialHint
	}
	approved := promptYesNoWithHint(message, hint)
	switch {
	case approved:
		recordCheck(nvxHome, "check_approved", c, answeredByPrompt)
	case nobodyIsHere():
		recordCheck(nvxHome, "check_refused", c, answeredNonInteractive)
	default:
		recordCheck(nvxHome, "check_refused", c, answeredByPrompt)
	}
	return approved
}

// blanketNote ends every remedy. NVX_YES and -y answer every check in the run,
// not the one that stopped, so they come after the setting that names it.
const blanketNote = " To approve every check instead, not only this one, pass -y or set NVX_YES=true."

// jsonList renders names as a JSON array body, `"a","b"`.
func jsonList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		b, _ := json.Marshal(n)
		quoted[i] = string(b)
	}
	return strings.Join(quoted, ",")
}

// policyEntryRemedy names the one policy line that settles a check. A project
// file naming it is a loosening and needs approval, which nobody can give a
// non-interactive run, so the line goes in the global file.
func policyEntryRemedy(section, key string, names []string, what string) string {
	return "To allow " + what + ", add it to " + section + "." + key + " in ~/.nvx/policy.json: " +
		`{"` + section + `":{"` + key + `":[` + jsonList(names) + `]}}.` + blanketNote
}

func typosquatRemedy(pkg string) string {
	return policyEntryRemedy("typosquatting", "trusted_packages", []string{pkg}, "this name")
}

func releaseAgeRemedy(pkg string) string {
	return policyEntryRemedy("release_age", "trusted_packages", []string{pkg}, "this package inside the cooling-off window")
}

func installScriptsRemedy(pkg string) string {
	return policyEntryRemedy("install_scripts", "trusted_packages", []string{pkg}, "this package's install scripts")
}

// advisoryRemedy lists the advisory IDs that stopped the install, capped so a
// lockfile with many findings does not print a page.
func advisoryRemedy(ids []string) string {
	const maxListed = 10
	shown := ids
	more := ""
	if len(ids) > maxListed {
		shown = ids[:maxListed]
		more = " The other advisories need their own entries."
	}
	return "To accept advisories you have assessed, add their IDs to vulnerabilities.allowed_advisories in ~/.nvx/policy.json: " +
		`{"vulnerabilities":{"allowed_advisories":[` + jsonList(shown) + `]}}.` + more +
		` To accept everything below a severity, set vulnerabilities.min_severity, for example {"vulnerabilities":{"min_severity":"high"}}.` + blanketNote
}

// reportPublicOnlyChecksSkipped says once per run, and records, that packages
// from a registry other than the public one get no typosquat or advisory check.
// Both ask a public service about a package by name: the download counts and
// OSV describe the public registry's package of that name, which is another
// package, and a private name should not leave the network it lives on.
func reportPublicOnlyChecksSkipped(nvxHome string, targets []verifyTarget, regs npmRegistryConfig) {
	hosts := map[string]bool{}
	seen := map[string]bool{}
	count := 0
	for _, t := range targets {
		if targetSourceKind(t) != "" {
			continue
		}
		name, _ := parsePackageQuery(t.spec)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if reg := regs.registryFor(name); !isPublicNpmRegistry(reg) {
			hosts[registryHost(reg)] = true
			count++
		}
	}
	if count == 0 {
		return
	}
	list := make([]string, 0, len(hosts))
	for h := range hosts {
		list = append(list, h)
	}
	sort.Strings(list)
	joined := strings.Join(list, ", ")
	LogWarn("%d package(s) come from %s, not the public npm registry. nvx checked them against that registry. The typosquat and known-vulnerability checks did not run for them, because those ask public services about a package by name.", count, joined)
	recordCheck(nvxHome, "check_skipped", checkInfo{check: checkPublicRegistryOnly,
		detail: strconv.Itoa(count) + " from " + joined}, answeredByRegistry)
}

// recordScriptCheckSkipped records, once per run, that the install-script check
// did not run because the command turns lifecycle scripts off. `by` says where.
func recordScriptCheckSkipped(nvxHome, source string) {
	auditLog(nvxHome, "check_skipped", map[string]string{"check": checkInstallScripts,
		"by": scriptsOffBy(source), "detail": "scripts are ignored by " + source})
}

// scriptsOffBy is the audit `by` token for a source of ignore-scripts.
func scriptsOffBy(source string) string {
	switch source {
	case scriptsOffByFlag:
		return "ignore_scripts_flag"
	case scriptsOffByEnv:
		return "ignore_scripts_env"
	}
	return "ignore_scripts_npmrc"
}

// unreachableRemedy is honest that no setting waives a lookup that failed.
func unreachableRemedy(what string) string {
	return "No policy setting waives a failed " + what + " lookup. Retry once it is reachable." +
		" To proceed without it, pass -y or set NVX_YES=true, which approves every check in the run."
}

// advisoryIDs returns the distinct advisory IDs in a scan result, sorted so the
// record and the remedy read the same on every run.
func advisoryIDs(found map[string][]OSVVuln) []string {
	seen := map[string]bool{}
	var ids []string
	for _, list := range found {
		for _, v := range list {
			if !seen[v.ID] {
				seen[v.ID] = true
				ids = append(ids, v.ID)
			}
		}
	}
	sort.Strings(ids)
	return ids
}
