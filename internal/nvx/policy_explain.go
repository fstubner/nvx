package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// `nvx policy explain` answers "why is this setting what it is".
//
// The effective policy is assembled from four places -- nvx's built-in defaults,
// ~/.nvx/policy.json, every .nvx-policy.json between the current directory and
// the root, and the project's grant file -- and until now the only way to see
// the result was to read all of them and apply the merge rules in your head. The
// question that actually gets asked is narrower and is not answerable that way:
// "I set this in my project file, why is it not applying?" There are three
// separate reasons it might not be (the file was never trusted, the enforced
// baseline refused it, or something nearer overrode it) and they look identical
// from the outside.
//
// One row per setting, with where its value came from. Values, not policy files:
// the reader has the files, what they are missing is the outcome.

type policySettingSource struct {
	Setting string
	Value   string
	Source  string
}

// policySettingValues renders the settings worth explaining, in a fixed order.
//
// A hand-written list rather than reflection over the struct. Reflection would
// cover Policy exactly and would also print the fields that are parsed and read
// by nothing (prompts.*, the legacy isolation.runtime pair), which is how a
// reader concludes a setting is in effect when it is inert.
func policySettingValues(p Policy) []policySettingSource {
	list := func(v []string) string {
		if len(v) == 0 {
			return "(none)"
		}
		return strings.Join(v, ", ")
	}
	return []policySettingSource{
		{Setting: "enforced", Value: strconv.FormatBool(p.Enforced)},
		{Setting: "blocked_packages", Value: list(p.BlockedPackages)},
		{Setting: "enforce_ignore_scripts", Value: strconv.FormatBool(p.EnforceIgnoreScripts)},
		{Setting: "typosquatting.enabled", Value: strconv.FormatBool(p.Typosquatting.Enabled)},
		{Setting: "typosquatting.max_distance", Value: strconv.Itoa(p.Typosquatting.MaxDistance)},
		{Setting: "typosquatting.trusted_packages", Value: list(p.Typosquatting.TrustedPackages)},
		{Setting: "release_age.enabled", Value: strconv.FormatBool(p.ReleaseAgeEnabled())},
		{Setting: "release_age.min_age_hours", Value: strconv.Itoa(p.ReleaseAgeMinHours())},
		{Setting: "release_age.trusted_packages", Value: list(p.ReleaseAge.TrustedPackages)},
		{Setting: "install_scripts.trusted_packages", Value: list(p.InstallScripts.TrustedPackages)},
		{Setting: "vulnerabilities.min_severity", Value: severityFloorLabel(p.Vulnerabilities.MinSeverity)},
		{Setting: "vulnerabilities.allowed_advisories", Value: list(p.Vulnerabilities.AllowedAdvisories)},
		{Setting: "isolation.enabled", Value: strconv.FormatBool(p.Isolation.Enabled)},
		// parseIsolationLevel rather than Policy.IsolationLevel, which warns on an
		// unrecognised value. This renders the same policy several times over to
		// work out where each setting came from, and a warning per stage would say
		// the same thing four times.
		{Setting: "isolation.level", Value: isolationLevelLabel(p.Isolation.Level)},
		{Setting: "isolation.filesystem.provider", Value: p.FilesystemProvider()},
		{Setting: "isolation.filesystem.allow_read_exec", Value: list(p.Isolation.Filesystem.AllowReadExec)},
		{Setting: "isolation.network.mode", Value: p.Isolation.Network.Mode},
		{Setting: "isolation.network.default_allow", Value: list(p.Isolation.Network.DefaultAllow)},
		{Setting: "isolation.network.allow_hosts", Value: list(p.Isolation.Network.AllowHosts)},
		{Setting: "isolation.network.prompt_unknown", Value: strconv.FormatBool(p.Isolation.Network.PromptUnknown)},
		{Setting: "isolation.network.expose_ports", Value: list(p.Isolation.Network.ExposePorts)},
		{Setting: "isolation.network.connect_ports", Value: list(p.Isolation.Network.ConnectPorts)},
		{Setting: "isolation.environment.allow", Value: list(p.Isolation.Environment.Allow)},
		{Setting: "environment.isolated_tools", Value: strconv.FormatBool(p.Environment.IsolatedTools)},
		{Setting: "runtime.default", Value: p.Runtime.Default},
	}
}

// policyExplanation is what the command prints: the settings with their origins,
// plus the things that happened on the way to them.
//
// Effective is the policy the rows describe, so `nvx policy check` can assemble
// the policy and learn what was refused on the way in one pass rather than
// walking the project files a second time with its own copy of the rules.
type policyExplanation struct {
	Rows      []policySettingSource
	Notes     []string
	Issues    []projectPolicyIssue
	Effective Policy
}

// projectPolicyIssue is a project policy file that did not apply, and why.
//
// Two reasons, and they are not the same thing to a reader or to a pipeline: a
// refused file broke an enforced baseline and nvx will not run at all, while an
// ignored one merely loosens settings nobody has approved yet and can be trusted
// on the spot.
type projectPolicyIssue struct {
	Path   string
	Kind   string // "refused" or "ignored"
	Detail string
}

// explainPolicy replays the same load order LoadPolicy uses, recording which
// stage last changed each setting.
//
// Replayed rather than instrumented in place. LoadPolicy is on the path of every
// command nvx runs and this is a reporting command, so the cost of being wrong
// here is a confusing table, while the cost of threading provenance through
// LoadPolicy is carrying it in the code that decides whether to sandbox. The
// stages are read from the same functions in the same order, so a change to the
// merge rules changes both.
func explainPolicy(nvxHome, cwd string) (policyExplanation, error) {
	var exp policyExplanation

	base := DefaultPolicy()
	normalizePolicy(&base)
	current := policySettingValues(base)
	for i := range current {
		current[i].Source = "built-in default"
	}

	global, err := loadGlobalPolicy(nvxHome)
	if err != nil {
		return exp, err
	}
	globalSource := "global policy (" + filepath.Join(nvxHome, "policy.json") + ")"
	if global.Enforced {
		globalSource = "global policy, enforced (" + filepath.Join(nvxHome, "policy.json") + ")"
		exp.Notes = append(exp.Notes, "The global policy is enforced: a project file may make a setting stricter and is refused if it loosens one.")
	}
	current = attributeChanges(current, policySettingValues(withPolicyDefaults(global)), globalSource)

	policy := global
	localPaths := collectProjectPolicyPaths(cwd, nvxHome)
	grants := loadProjectGrants(nvxHome, projectScopeDir())
	// Farthest first, nearest last, exactly as LoadPolicy applies them.
	for i := len(localPaths) - 1; i >= 0; i-- {
		localPath := localPaths[i]
		localPolicy, _, hash, err := readAndHashProjectPolicyFile(localPath)
		if err != nil {
			return exp, err
		}
		candidate, mergeErr := MergeUnderBaseline(policy, localPolicy, localPath)
		if mergeErr != nil {
			exp.Issues = append(exp.Issues, projectPolicyIssue{Path: localPath, Kind: "refused", Detail: mergeErr.Error()})
			// LoadPolicy fails outright here, so nothing after this file applies
			// either. Reporting the rest would describe a policy no command runs
			// under.
			exp.Rows = current
			exp.Effective = policy
			return exp, nil
		}
		if policyLoosens(policy, candidate) && grants.PolicyPins[filepath.Clean(localPath)] != hash {
			exp.Issues = append(exp.Issues, projectPolicyIssue{
				Path:   localPath,
				Kind:   "ignored",
				Detail: "it loosens settings and has not been trusted for this project, so what it says is not what is in force",
			})
			continue
		}
		policy = candidate
		current = attributeChanges(current, policySettingValues(withPolicyDefaults(policy)), "project policy ("+localPath+")")
	}

	if len(grants.AllowHosts) > 0 {
		policy.Isolation.Network.AllowHosts = append(append([]string{}, policy.Isolation.Network.AllowHosts...), grants.AllowHosts...)
		current = attributeChanges(current, policySettingValues(withPolicyDefaults(policy)), "approved for this project (nvx grants list)")
	}

	exp.Rows = current
	exp.Effective = withPolicyDefaults(policy)
	return exp, nil
}

// withPolicyDefaults returns the policy as the rest of nvx sees it, since
// defaulting happens after merging and a table of pre-default values would not
// match what any command actually enforces.
func withPolicyDefaults(p Policy) Policy {
	normalizePolicy(&p)
	return p
}

// attributeChanges stamps source onto every setting whose value this stage
// changed, leaving the others with whatever stamped them before.
func attributeChanges(before, after []policySettingSource, source string) []policySettingSource {
	out := make([]policySettingSource, len(after))
	for i := range after {
		out[i] = after[i]
		out[i].Source = before[i].Source
		if before[i].Value != after[i].Value {
			out[i].Source = source
		}
	}
	return out
}

func runPolicyExplain(args []string, nvxHome string) int {
	for _, arg := range args {
		LogError("Unknown option for nvx policy explain: %s", arg)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		LogError("Failed to resolve working directory: %v", err)
		return 1
	}
	exp, err := explainPolicy(nvxHome, cwd)
	if err != nil {
		LogError("Could not work out the effective policy: %v", err)
		return 1
	}

	fmt.Printf("Effective nvx policy in %s\n\n", cwd)
	width := 0
	for _, row := range exp.Rows {
		if len(row.Setting) > width {
			width = len(row.Setting)
		}
	}
	valueWidth := 0
	for _, row := range exp.Rows {
		if len(row.Value) > valueWidth {
			valueWidth = len(row.Value)
		}
	}
	// A long list would push the source column off the terminal, and the source
	// is the column this command exists for.
	if valueWidth > 40 {
		valueWidth = 40
	}
	for _, row := range exp.Rows {
		value := row.Value
		if len(value) > valueWidth {
			value = value[:valueWidth-1] + "…"
		}
		fmt.Printf("  %-*s  %-*s  %s\n", width, row.Setting, valueWidth, value, row.Source)
	}
	if len(exp.Notes) > 0 {
		fmt.Println()
		for _, note := range exp.Notes {
			fmt.Println(note)
		}
	}
	for _, issue := range exp.Issues {
		fmt.Println()
		switch issue.Kind {
		case "refused":
			fmt.Printf("Refused: %s\n", issue.Detail)
			fmt.Println("No command runs in this directory until that file agrees with the enforced global policy.")
		default:
			fmt.Printf("Ignored: %s\n  %s\n", issue.Path, issue.Detail)
			fmt.Println("  Run a command here to be asked about it, or see nvx grants list.")
		}
	}
	return 0
}

// isolationLevelLabel renders isolation.level without warning about a value it
// does not recognise, which the caller above explains.
func isolationLevelLabel(value string) string {
	level, ok := parseIsolationLevel(value)
	if !ok {
		return level.String() + " (unrecognised " + strconv.Quote(value) + ")"
	}
	return level.String()
}
