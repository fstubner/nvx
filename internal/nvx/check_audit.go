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
// -y and NVX_YES approve the typosquat, release-age, install-script and
// advisory prompts without a word, and --agent-mode did too until it was made
// to refuse instead. Measured:
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
// `check` names the check, `by` says who answered: yes_flag or nvx_yes
// (approved without asking), prompt (a person answered), non_interactive
// (nobody was there to answer), agent_mode (--agent-mode refused without
// asking) or policy (no prompt exists).
const (
	checkTyposquat          = "typosquat"
	checkReleaseAge         = "release_age"
	checkInstallScripts     = "install_scripts"
	checkVulnerability      = "vulnerability"
	checkMaliciousPackage   = "malicious_package"
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
	answeredByAgentMode     = "agent_mode"
)

// checkInfo describes one check outcome for the log and for the stderr line.
type checkInfo struct {
	check   string
	pkg     string
	version string
	detail  string // short, for the audit record
	what    string // for the stderr line when nobody was asked
	// aborted is the error line for a refusal, "Installation aborted: ...".
	// askCheck prints it, so the paragraph for an agent can come after it and
	// end the refusal.
	aborted string
}

// autoApprovalSource reports which switch answers the pre-install checks
// without asking, or "" when none does. Outside agent mode it mirrors the first
// two tests in PromptYesNo, in the same order, so the two cannot disagree about
// whether a prompt was asked.
//
// --agent-mode is not one of them. It approved every check until it was made to
// refuse whatever would ask. A person who set NVX_AGENT_MODE in an agent's
// environment to stop it hanging had turned the typosquat, release-age,
// install-script and advisory checks into log lines.
//
// In agent mode -y and NVX_YES answer nothing either. Agents pass -y by habit,
// and with -y beside --agent-mode a release inside the cooling-off window went
// ahead with "Approved without asking (-y)". TestAgentModeIgnoresYesAndNvxYes
// pins it. askCheck says which of them it ignored.
func autoApprovalSource() (token, label string) {
	switch {
	case agentModeFlag:
		return "", ""
	case yesFlag:
		return "yes_flag", "-y"
	case nvxYesSet():
		return "nvx_yes", "NVX_YES"
	}
	return "", ""
}

// noteApprovalsIgnored says, in agent mode, that -y and NVX_YES did not
// approve the check that was refused.
func noteApprovalsIgnored() {
	var set []string
	if yesFlag {
		set = append(set, "-y")
	}
	if nvxYesSet() {
		set = append(set, "NVX_YES")
	}
	switch len(set) {
	case 1:
		LogWarn("%s was ignored, because agent mode is on and nothing approves a check in agent mode.", set[0])
	case 2:
		LogWarn("-y and NVX_YES were ignored, because agent mode is on and nothing approves a check in agent mode.")
	}
}

// nobodyIsHere mirrors the non-interactive tests in PromptYesNo.
func nobodyIsHere() bool {
	return os.Getenv("NVX_NONINTERACTIVE") == "true" || os.Getenv("NVX_NONINTERACTIVE") == "1" || !stdinInteractive()
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
// remedy is the advice printed when nvx refuses without asking: the narrowest
// policy line that settles this one check, with the blanket switches last, and
// then the paragraph for an agent. A person who answered no has decided, so
// they are told only that the install stopped.
func askCheck(nvxHome string, c checkInfo, message string, remedy checkRemedy) bool {
	if token, label := autoApprovalSource(); token != "" {
		recordCheck(nvxHome, "check_approved", c, token)
		LogWarn("Approved without asking (%s): %s", label, c.what)
		return true
	}
	noteTrailingApprovalFlag()
	switch {
	case agentModeFlag:
		// Even with a terminal on stdin. An agent that drives a pseudo-terminal
		// looks like a person there, and --agent-mode says it is not one.
		LogWarn("--agent-mode is set, so nvx refuses instead of asking: %s", message)
		noteApprovalsIgnored()
		recordCheck(nvxHome, "check_refused", c, answeredByAgentMode)
	case nobodyIsHere():
		LogWarn("Non-interactive environment: denying prompt. Prompt was: %s", message)
		recordCheck(nvxHome, "check_refused", c, answeredNonInteractive)
	default:
		if consoleYesNo(message, remedy.text) {
			recordCheck(nvxHome, "check_approved", c, answeredByPrompt)
			return true
		}
		recordCheck(nvxHome, "check_refused", c, answeredByPrompt)
		if c.aborted != "" {
			LogError("%s", c.aborted)
		}
		return false
	}
	if c.aborted != "" {
		LogError("%s", c.aborted)
	}
	explainCheckRefusal(remedy)
	return false
}

// checkRemedy is what a refusal tells the reader to do about it.
type checkRemedy struct {
	// text is for a person: the narrowest setting that allows this, then the
	// switches that approve every check.
	text string
	// line is that setting as the JSON to add to ~/.nvx/policy.json, repeated in
	// the paragraph for an agent, or "" when no setting allows it.
	line string
}

// explainCheckRefusal ends a check refusal: the remedy, then the paragraph for
// an agent, which is always last.
func explainCheckRefusal(r checkRemedy) {
	if r.text != "" {
		LogRefusalDetail("%s", r.text)
	}
	LogRefusalDetail("%s", agentRefusalNote(r.line))
}

// agentRefusalNote is the paragraph every check refusal ends with, in one
// wording.
//
// The refusals named -y, NVX_YES and ~/.nvx/policy.json as the way past them,
// and an agent outside the sandbox can use all three, so the text coached it to
// approve itself. Only the global-install refusal spoke to an agent. This says
// what not to do, and gives the line for the person to add, since the person is
// who decides.
func agentRefusalNote(line string) string {
	const head = "If you are an automated agent: do not retry with -y or NVX_YES, and do not edit the policy yourself. Tell the person you work for"
	if line == "" {
		return head + ", and let them decide."
	}
	return head + ", who can add this line to ~/.nvx/policy.json: " + line
}

// blanketNote ends every remedy. NVX_YES and -y answer every check in the run,
// not the one that stopped, so they come after the setting that names it.
// NVX_YES comes first because it works through the shims, and -y only before
// the command.
const blanketNote = " To approve every check in the run instead, not only this one, set NVX_YES=true, or put -y before the command (nvx -y ...)."

// jsonList renders names as a JSON array body, `"a","b"`.
func jsonList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		b, _ := json.Marshal(n)
		quoted[i] = string(b)
	}
	return strings.Join(quoted, ",")
}

// policyEntryLine is the JSON a policy file needs to name names in section.key.
func policyEntryLine(section, key string, names []string) string {
	return `{"` + section + `":{"` + key + `":[` + jsonList(names) + `]}}`
}

// policyEntryRemedy names the one policy line that settles a check. A project
// file naming it is a loosening that has to be trusted, so the line goes in the
// global file.
func policyEntryRemedy(section, key string, names []string, what string) checkRemedy {
	line := policyEntryLine(section, key, names)
	return checkRemedy{
		text: "To allow " + what + ", add it to " + section + "." + key + " in ~/.nvx/policy.json: " + line + "." + blanketNote,
		line: line,
	}
}

func typosquatRemedy(pkg string) checkRemedy {
	return policyEntryRemedy("typosquatting", "trusted_packages", []string{pkg}, "this name")
}

// releaseAgeRemedy offers an older version first. Naming one published before
// the window is the way past this check that an agent may take itself, since
// every check still runs on the version it names.
//
// The name is in commands someone may paste into a shell, and it can come from
// a lockfile, so one that is not a plain package name is left as a placeholder.
func releaseAgeRemedy(pkg string) checkRemedy {
	r := policyEntryRemedy("release_age", "trusted_packages", []string{pkg}, "this package inside the cooling-off window")
	shown := "<package>"
	if isValidPackageName(pkg) {
		shown = pkg
	}
	r.text = "To install a version published before the window instead, which an automated agent may do itself, name it, as " +
		shown + "@<version>. `npm view " + shown + " time` lists when each version was published. " + r.text
	return r
}

// releaseAgeUnknownRemedy is for a version with no publish time, which a
// registry that never sends one gives for every package.
func releaseAgeUnknownRemedy(pkg string) checkRemedy {
	line := policyEntryLine("release_age", "trusted_packages", []string{pkg})
	return checkRemedy{
		text: "To allow this package without a publish time, add it to release_age.trusted_packages in ~/.nvx/policy.json: " + line +
			`. For a registry that sends no publish times, list its packages there by scope, such as "@your-scope/*", or set release_age.enabled to false.` +
			blanketNote,
		line: line,
	}
}

func installScriptsRemedy(pkg string) checkRemedy {
	return policyEntryRemedy("install_scripts", "trusted_packages", []string{pkg}, "this package's install scripts")
}

// advisoryRemedy lists the advisory IDs that stopped the install, capped so a
// lockfile with many findings does not print a page.
func advisoryRemedy(ids []string) checkRemedy {
	const maxListed = 10
	shown := ids
	more := ""
	if len(ids) > maxListed {
		shown = ids[:maxListed]
		more = " The other advisories need their own entries."
	}
	line := policyEntryLine("vulnerabilities", "allowed_advisories", shown)
	return checkRemedy{
		text: "To accept advisories you have assessed, add their IDs to vulnerabilities.allowed_advisories in ~/.nvx/policy.json: " +
			line + "." + more +
			` To accept everything below a severity, set vulnerabilities.min_severity, for example {"vulnerabilities":{"min_severity":"high"}}.` + blanketNote,
		line: line,
	}
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
	case scriptsOffByYarnMode:
		return "yarn_mode_skip_build"
	case scriptsOffByYarnrc:
		return "yarnrc_enable_scripts"
	}
	return "ignore_scripts_npmrc"
}

// unreachableRemedy is honest that no setting waives a lookup that failed.
func unreachableRemedy(what string) checkRemedy {
	return checkRemedy{text: "No policy setting waives a failed " + what + " lookup. Retry once it is reachable." +
		" To proceed without it, set NVX_YES=true or put -y before the command, which approves every check in the run."}
}

// maliciousAdvisories returns the MAL- advisories in a scan result and the
// packages they name, each sorted.
func maliciousAdvisories(found map[string][]OSVVuln) (ids, pkgs []string) {
	seen := map[string]bool{}
	for pkgKey, list := range found {
		named := false
		for _, v := range list {
			if !isMaliciousAdvisory(v.ID) {
				continue
			}
			named = true
			if !seen[v.ID] {
				seen[v.ID] = true
				ids = append(ids, v.ID)
			}
		}
		if named {
			pkgs = append(pkgs, pkgKey)
		}
	}
	sort.Strings(ids)
	sort.Strings(pkgs)
	return ids, pkgs
}

// maliciousRemedy says that nothing but an entry naming the advisory allows a
// malicious package.
func maliciousRemedy(ids []string) checkRemedy {
	line := policyEntryLine("vulnerabilities", "allowed_advisories", ids)
	return checkRemedy{
		text: "-y, NVX_YES, NVX_TRUST_YES and vulnerabilities.min_severity do not allow a package known to be malicious. " +
			"If you have checked that the advisory does not apply, add its ID to vulnerabilities.allowed_advisories in ~/.nvx/policy.json: " +
			line + `. A pattern such as "MAL-*" does not allow it.`,
		line: line,
	}
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
