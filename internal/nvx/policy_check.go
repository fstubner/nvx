package nvx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// `nvx policy check` is the gate a CI job runs.
//
// It answers one question -- does this project satisfy the policy in force --
// and answers it in a way a pipeline can act on: a distinct exit code per
// failure class (see exit_codes.go) and, with --format json, the same verdict as
// data. Everything else nvx does exits 0 or 1, so gating on it means gating on
// "something went wrong", which is as true of a DNS failure as of a blocked
// package.
//
// Two rules it must not break:
//
//   - It never prompts. A prompt in CI is a hang, and a hang is worse than a
//     failure because nothing reports it until the job times out. Nothing here
//     calls a Prompt function, and ensureProjectPolicyTrust -- the one part of
//     policy loading that asks anything -- is deliberately not on this path. An
//     untrusted project policy is reported as a finding instead.
//   - It makes no network request unless asked. Reaching the registry and OSV
//     turns a check into something that fails when a third party is down, which
//     in a pipeline is indistinguishable from a real violation. --online opts in,
//     and the checks that need it say they were skipped rather than passing
//     silently.

type policyCheckFinding struct {
	Class   string `json:"class"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type policyCheckResult struct {
	OK       bool                 `json:"ok"`
	ExitCode int                  `json:"exit_code"`
	Findings []policyCheckFinding `json:"findings"`
	// Skipped names the checks that did not run, so a green result cannot be
	// read as "everything was checked".
	Skipped []string `json:"skipped,omitempty"`
	Checked []string `json:"checked"`
}

func runPolicyCheck(args []string, nvxHome string) int {
	asJSON := false
	online := false
	for _, arg := range args {
		switch arg {
		case "--format=json":
			asJSON = true
		case "--format=text":
			asJSON = false
		case "--online":
			online = true
		default:
			LogError("Unknown option for nvx policy check: %s", arg)
			LogInfo("Valid options: --format=json, --format=text, --online")
			return exitPolicyInternalError
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		LogError("Failed to resolve working directory: %v", err)
		return exitPolicyInternalError
	}

	result := evaluatePolicyCheck(nvxHome, cwd, online)
	if asJSON {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			LogError("Failed to encode the result: %v", err)
			return exitPolicyInternalError
		}
		fmt.Println(string(data))
		return result.ExitCode
	}
	printPolicyCheckResult(result)
	return result.ExitCode
}

func printPolicyCheckResult(result policyCheckResult) {
	for _, name := range result.Checked {
		fmt.Printf("  checked  %s\n", name)
	}
	for _, name := range result.Skipped {
		fmt.Printf("  skipped  %s\n", name)
	}
	if len(result.Findings) == 0 {
		fmt.Println("\nPass: the project satisfies the policy in force.")
		return
	}
	fmt.Println()
	for _, f := range result.Findings {
		fmt.Printf("  %s (%d): %s\n", f.Class, f.Code, f.Message)
	}
	fmt.Printf("\nFailed: exit %d (%s). See docs/exit-codes.md.\n", result.ExitCode, classForCode(result.ExitCode))
}

func classForCode(code int) string {
	for _, c := range policyCheckClasses {
		if c.Code == code {
			return c.Name
		}
	}
	return "unknown"
}

// evaluatePolicyCheck runs every check and collects the findings.
//
// Every check runs even once one has failed, because a pipeline's output is read
// once: stopping at the first finding turns one fix into one run per finding.
// The exit code is chosen afterwards, from the most severe class present.
func evaluatePolicyCheck(nvxHome, cwd string, online bool) policyCheckResult {
	var result policyCheckResult
	add := func(class, format string, a ...interface{}) {
		for _, c := range policyCheckClasses {
			if c.Name == class {
				result.Findings = append(result.Findings, policyCheckFinding{
					Class: class, Code: c.Code, Message: fmt.Sprintf(format, a...),
				})
				return
			}
		}
	}

	exp, err := explainPolicy(nvxHome, cwd)
	if err != nil {
		result.Findings = append(result.Findings, policyCheckFinding{
			Class: "policy_file_invalid", Code: exitPolicyFileInvalid, Message: err.Error(),
		})
		result.ExitCode = exitPolicyFileInvalid
		result.Checked = append(result.Checked, "policy files parse")
		return result
	}
	result.Checked = append(result.Checked, "policy files parse")
	policy := exp.Effective

	result.Checked = append(result.Checked, "project policy files are in force")
	for _, issue := range exp.Issues {
		switch issue.Kind {
		case "refused":
			add("policy_violation", "%s", issue.Detail)
		default:
			add("policy_violation", "%s %s", issue.Path, issue.Detail)
		}
	}

	result.Checked = append(result.Checked, "containment is available on this platform")
	if policy.Isolation.Enabled && !sandboxAvailableHere() {
		add("sandbox_unavailable", "isolation.enabled is true and nvx has no OS-native sandbox on %s; every contained command will refuse to run", runtime.GOOS)
	}

	deps, source := projectDependencies(cwd)
	if source == "" {
		result.Skipped = append(result.Skipped, "dependency checks (no package.json or package-lock.json here)")
		result.ExitCode = worstPolicyCheckCode(result.Findings)
		result.OK = len(result.Findings) == 0
		return result
	}

	result.Checked = append(result.Checked, "dependencies against blocked_packages (from "+source+")")
	// One finding per NAME. A lockfile can carry the same package at several
	// versions, and repeating the same line per version is noise in front of the
	// fix, which is the same fix every time.
	reported := map[string]bool{}
	for _, dep := range deps {
		if reported[dep.Name] || !policy.IsBlocked(dep.Name) {
			continue
		}
		reported[dep.Name] = true
		add("blocked_package", "%s is named in blocked_packages and is a dependency of this project", dep.Name)
	}

	if !online {
		result.Skipped = append(result.Skipped, "known vulnerabilities and release age (both need the network; pass --online)")
		result.ExitCode = worstPolicyCheckCode(result.Findings)
		result.OK = len(result.Findings) == 0
		return result
	}

	checkProjectVulnerabilities(policy, deps, &result, add)
	checkProjectReleaseAge(policy, deps, &result, add)

	result.ExitCode = worstPolicyCheckCode(result.Findings)
	result.OK = len(result.Findings) == 0
	return result
}

// checkProjectVulnerabilities asks OSV about the dependencies whose exact
// version is known.
//
// A range is not a version, and guessing which one a lockfile-less project would
// resolve to would report an advisory against something nobody installs. Those
// are reported as skipped instead.
func checkProjectVulnerabilities(policy Policy, deps []projectDependency, result *policyCheckResult, add func(string, string, ...interface{})) {
	var queries []OSVQuery
	for _, dep := range deps {
		if dep.Version == "" {
			continue
		}
		queries = append(queries, OSVQuery{Package: OSVPackage{Name: dep.Name, Ecosystem: "npm"}, Version: dep.Version})
	}
	if len(queries) == 0 {
		result.Skipped = append(result.Skipped, "known vulnerabilities (no exact versions here; a lockfile carries them)")
		return
	}
	found, err := scanVulnerabilitiesBatchForVerify(queries)
	if err != nil {
		// Reported as a finding rather than swallowed: "could not check" and
		// "checked, nothing found" are the two answers a security tool must never
		// confuse. It is an internal error rather than a vulnerability, because the
		// project has not been shown to violate anything.
		add("internal_error", "the vulnerability scan did not complete: %v", err)
		return
	}
	result.Checked = append(result.Checked, fmt.Sprintf("%d dependencies against OSV", len(queries)))
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, v := range found[key] {
			if policy.BlocksInstall(v) {
				add("vulnerability", "%s: %s (%s)", key, v.ID, severityLabel(v.Severity))
			}
		}
	}
}

// severityLabel renders an advisory's rating, saying so when there is none.
func severityLabel(severity string) string {
	if strings.TrimSpace(severity) == "" {
		return "severity unknown, which counts as above every floor"
	}
	return strings.ToLower(severity)
}

// checkProjectReleaseAge looks for dependencies published inside the cooling-off
// window.
//
// Direct dependencies only, and one registry request each. The window exists
// because a compromised publish is usually caught within a day, and the packages
// a project can actually choose to hold back are the ones it names itself; a
// transitive tree turns one check into several hundred requests for versions the
// project does not control.
func checkProjectReleaseAge(policy Policy, deps []projectDependency, result *policyCheckResult, add func(string, string, ...interface{})) {
	if !policy.ReleaseAgeEnabled() {
		result.Skipped = append(result.Skipped, "release age (release_age.enabled is false)")
		return
	}
	window := policy.ReleaseAgeMinHours()
	now := time.Now()
	checked := 0
	// One registry request per name, for the same reason the blocklist reports one
	// finding per name.
	asked := map[string]bool{}
	for _, dep := range deps {
		if !dep.Direct || asked[dep.Name] || policy.IsReleaseAgeTrusted(dep.Name) {
			continue
		}
		asked[dep.Name] = true
		query := dep.Version
		if query == "" {
			query = dep.Range
		}
		_, pubTime, _, err := resolveNpmPackageDetailsForVerify(dep.Name, query)
		if err != nil {
			add("internal_error", "could not resolve %s from the registry: %v", dep.Name, err)
			continue
		}
		checked++
		if publishAgeShouldWarn(pubTime, window, now) {
			add("release_age", "%s was published within the %d hour release_age window", dep.Name, window)
		}
	}
	result.Checked = append(result.Checked, fmt.Sprintf("%d direct dependencies against the %d hour release_age window", checked, window))
}

// worstPolicyCheckCode picks the exit code: the most severe class present, in
// the order policyCheckClasses declares. An internal error outranks a verdict,
// since a run that could not finish has not produced one.
func worstPolicyCheckCode(findings []policyCheckFinding) int {
	if len(findings) == 0 {
		return exitPolicyPass
	}
	for _, c := range policyCheckClasses {
		for _, f := range findings {
			if f.Code == c.Code {
				return c.Code
			}
		}
	}
	return exitPolicyInternalError
}

// sandboxAvailableHere reports whether this platform has an OS-native sandbox.
//
// The same three platforms platformLaunchNative is implemented for. A policy
// that requires containment on anything else does not fail at check time today;
// it fails at the first contained command, having already been committed.
func sandboxAvailableHere() bool {
	switch runtime.GOOS {
	case "windows", "linux", "darwin":
		return true
	}
	return false
}

// projectDependency is one package the project depends on, as the manifest or
// the lockfile describes it.
type projectDependency struct {
	Name string
	// Version is the exact resolved version, from a lockfile. Empty when only a
	// range is known.
	Version string
	// Range is what package.json asked for, e.g. "^4.17.1".
	Range string
	// Direct means package.json names it, rather than it arriving through
	// something else.
	Direct bool
}

// projectDependencies reads the project's dependencies, preferring the lockfile
// for its exact versions and falling back to the manifest. The second return
// names the file they came from, or is empty when there is nothing to read.
func projectDependencies(cwd string) ([]projectDependency, string) {
	direct, ranges := directDependencies(cwd)
	locked := lockedDependencies(cwd)

	switch {
	case len(locked) > 0:
		byName := make(map[string]bool, len(locked))
		out := make([]projectDependency, 0, len(locked))
		for _, dep := range locked {
			dep.Direct = direct[dep.Name]
			dep.Range = ranges[dep.Name]
			byName[dep.Name] = true
			out = append(out, dep)
		}
		// A direct dependency the lockfile does not carry is still a dependency,
		// and leaving it out would quietly narrow the blocklist check to whatever
		// happens to be installed.
		for name := range direct {
			if !byName[name] {
				out = append(out, projectDependency{Name: name, Range: ranges[name], Direct: true})
			}
		}
		sortDependencies(out)
		return out, "package-lock.json"
	case len(direct) > 0:
		out := make([]projectDependency, 0, len(direct))
		for name := range direct {
			out = append(out, projectDependency{Name: name, Range: ranges[name], Direct: true})
		}
		sortDependencies(out)
		return out, "package.json"
	}
	return nil, ""
}

func sortDependencies(deps []projectDependency) {
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].Name != deps[j].Name {
			return deps[i].Name < deps[j].Name
		}
		return deps[i].Version < deps[j].Version
	})
}

// directDependencies reads the four dependency maps package.json can carry.
//
// Its own reader rather than packagesFromPackageJSON, which returns names only
// and reads from the process working directory. The version RANGE is what makes
// a release-age check possible without a lockfile, and a check command should
// not depend on where it was launched from.
func directDependencies(cwd string) (map[string]bool, map[string]string) {
	names := map[string]bool{}
	ranges := map[string]string{}
	data, err := os.ReadFile(filepath.Join(cwd, "package.json"))
	if err != nil {
		return names, ranges
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(withoutUTF8BOM(data), &pkg); err != nil {
		LogWarn("Failed to parse package.json: %v", err)
		return names, ranges
	}
	for _, deps := range []map[string]string{pkg.Dependencies, pkg.DevDependencies, pkg.OptionalDependencies, pkg.PeerDependencies} {
		for name, spec := range deps {
			names[name] = true
			if ranges[name] == "" {
				ranges[name] = spec
			}
		}
	}
	return names, ranges
}

// lockedDependencies reads the resolved versions out of package-lock.json.
func lockedDependencies(cwd string) []projectDependency {
	data, err := os.ReadFile(filepath.Join(cwd, "package-lock.json"))
	if err != nil {
		return nil
	}
	var lock packageLockFile
	if err := json.Unmarshal(withoutUTF8BOM(data), &lock); err != nil {
		LogWarn("Failed to parse package-lock.json: %v", err)
		return nil
	}
	seen := map[string]bool{}
	var out []projectDependency
	addDep := func(name, version string) {
		if name == "" || version == "" || seen[name+"@"+version] {
			return
		}
		seen[name+"@"+version] = true
		out = append(out, projectDependency{Name: name, Version: version})
	}
	for path, pkg := range lock.Packages {
		addDep(packageNameFromLockPath(path), pkg.Version)
	}
	var walk func(deps map[string]packageLockPackage)
	walk = func(deps map[string]packageLockPackage) {
		for name, dep := range deps {
			addDep(name, dep.Version)
			walk(dep.Dependencies)
		}
	}
	walk(lock.Dependencies)
	return out
}
