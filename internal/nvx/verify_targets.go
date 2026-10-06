package nvx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

// What the pre-install checks run on.
//
// The checks ran on nvx's guess of what the package manager would install:
// the names on the command line, or the entries of package-lock.json taken at
// their word. Measured 2026-10-01 against a build of main:
//
//   - `npm install tsx` checked tsx. It also installed esbuild, whose
//     postinstall ran with no prompt, while `npm install esbuild` prompted.
//   - A lockfile entry named left-pad@1.3.0 whose `resolved` URL was the
//     is-number tarball was checked as left-pad and installed is-number.
//   - With no lockfile, esbuild pinned at 0.20.0 in package.json was checked as
//     0.28.2, and a `file:` dependency was looked up in the registry by name.
//   - update, dedupe, rebuild, `npm exec`, create, init, dlx and `bun x` ran
//     contained with no checks at all.
//
// For npm, nvx now asks npm what it will install (see npm_resolve.go). Every
// lockfile entry is held to the registry's record of its name and version. A
// whole-project pnpm, Yarn or Bun install reads that package manager's
// lockfile (see lockfile_pm.go). Their named installs and updates read the
// command line and package.json, which the docs say.

// verifyTarget is one package the pre-install checks run on.
type verifyTarget struct {
	// spec is what the checks parse: name@version, or a spec as typed on the
	// command line or declared in package.json.
	spec string
	// name is set for a lockfile entry, whose spec may be a bare name.
	name string
	// From a lockfile entry: where npm fetches the package and the hash it
	// checks the download against. Both empty for anything else.
	resolved, integrity string
	// sourceKind names a lockfile entry's non-registry source: a git URL, a
	// remote tarball someone asked for, a file: path.
	sourceKind string
	// hasInstallScript is the lockfile's own note that the package runs one.
	// It can only add a prompt: npm writes it for a binding.gyp as well, which
	// the registry's scripts field does not show.
	hasInstallScript bool
	// transitive marks a lockfile entry the user did not choose: a dependency
	// of something they did. The typosquat check skips it, because a name a
	// package's author wrote is not a name anyone mistyped.
	transitive bool
	// locked marks an entry read from a lockfile. A Yarn 2+ entry records
	// neither a URL nor a registry hash, so the two fields above cannot say so.
	locked bool
}

func (t verifyTarget) fromLockfile() bool { return t.locked }

// carriesSource reports a lockfile entry that records where its tarball is or
// what it hashes to, which must agree with the registry's record.
func (t verifyTarget) carriesSource() bool { return t.resolved != "" || t.integrity != "" }

func specTargets(specs []string) []verifyTarget {
	out := make([]verifyTarget, 0, len(specs))
	for _, s := range specs {
		out = append(out, verifyTarget{spec: s})
	}
	return out
}

func targetSpecs(targets []verifyTarget) []string {
	if len(targets) == 0 {
		return nil
	}
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.spec)
	}
	return out
}

// verifyRequest is what the pre-install checks need to know about a command.
type verifyRequest struct {
	pmCmd   string
	pmArgs  []string
	nvxHome string
	// contain and launch describe how the command itself will run, so that
	// npm's resolution step runs the same way.
	contain bool
	launch  SandboxConfig
}

// verifyBeforeRun runs the pre-install checks for a package-manager command.
// It returns 0 to proceed, or the exit code, a refusal reason and a label for
// the first package checked.
func verifyBeforeRun(req verifyRequest) (int, string, string) {
	platform := installPlatform(req)
	var targets []verifyTarget
	if strings.EqualFold(req.pmCmd, "npm") {
		resolved, code, reason := npmResolvedTargets(req, platform)
		if code != 0 {
			return code, reason, ""
		}
		targets = resolved
	}
	if targets == nil {
		var err error
		targets, err = detectTargets(req.pmCmd, req.pmArgs, platform)
		if err != nil {
			// The lockfile is what installs, and nvx could not read it. Checking
			// package.json instead checked the newest version of each range in
			// place of the locked one, and flagged versions nobody was installing.
			// For pnpm, Yarn and Bun, package.json is what was checked before
			// their lockfiles were read, and it is offered as that.
			msg := fmt.Sprintf("%v, so nvx cannot tell which versions this command installs and has nothing to check. Proceed without the pre-install checks?", err)
			what := "the install goes ahead without its lockfile checked"
			if len(targets) > 0 {
				msg = fmt.Sprintf("%v, so nvx cannot tell which versions this command installs. It can check what package.json declares, and not the packages those bring in. Proceed with only those checked?", err)
				what = "the install goes ahead with only what package.json declares checked"
			}
			if !askCheck(req.nvxHome, checkInfo{check: checkLockfileUnreadable, detail: err.Error(), what: what}, msg, lockfileUnreadableRemedy) {
				LogError("Installation aborted: the lockfile could not be read and proceeding was not approved.")
				return 1, "its lockfile could not be read", ""
			}
			if len(targets) == 0 {
				return 0, "", ""
			}
			LogWarn("Checking what package.json declares in place of the lockfile, which could not be read. The packages those bring in are not checked.")
		}
	}
	if len(targets) == 0 {
		return 0, "", ""
	}
	// The registries this command's own npm will read: a contained npm sees
	// the project's .npmrc only. See npm_registry.go.
	regs := loadNpmRegistryConfig(projectManifestDir(), req.contain)
	scriptsOff := ignoreScriptsSource(req.pmCmd, req.pmArgs, req.contain, projectManifestDir())
	code, reason := runVerifyTargetsWith(targets, req.nvxHome, regs, scriptsOff)
	return code, reason, targets[0].spec
}

const lockfileUnreadableRemedy = "No policy setting waives an unreadable lockfile. Check that the package manager wrote it, or update nvx if the lockfile is in a newer format than it reads." +
	" To proceed without the checks, pass -y or set NVX_YES=true, which approves every check in the run."

// detectTargets lists what the checks run on without asking the package
// manager: the packages a command names, or the project's lockfile, or its
// package.json. The error is a lockfile that exists and could not be read.
func detectTargets(cmdName string, args []string, platform nodePlatform) ([]verifyTarget, error) {
	cmd := strings.ToLower(cmdName)
	switch cmd {
	case "npm", "yarn", "pnpm":
		if pkgs := detectInstallPackages(args); len(pkgs) > 0 {
			return specTargets(pkgs), nil
		}
		if hasInstallVerb(args, ciVerbs...) || isBareYarnInstall(cmd, args) {
			return projectTargets(cmd, platform)
		}
		if commandVerbIndex(args, refreshVerbs...) >= 0 {
			return refreshTargets(cmd, args, platform)
		}
		if t := runnerTargets(cmd, args); t != nil {
			return t, nil
		}
		// `npm link <name>` installs <name> from the registry into the global
		// prefix before linking it. A path is skipped by the checks, which say so.
		if cmd == "npm" && commandVerbIndex(args, "link", "ln") >= 0 {
			return specTargets(installPackagesArg(args, "link", "ln")), nil
		}
	case "bun":
		// bun add/install/i/a [pkg...]; "a" is Bun's short alias for add.
		if pkgs := installPackagesArg(args, "a"); len(pkgs) > 0 {
			return specTargets(pkgs), nil
		}
		if hasInstallVerb(args, "a") {
			return projectTargets(cmd, platform)
		}
		if commandVerbIndex(args, refreshVerbs...) >= 0 {
			return refreshTargets(cmd, args, platform)
		}
		if t := runnerTargets(cmd, args); t != nil {
			return t, nil
		}
		// `bun pm trust <name>...` runs those packages' blocked scripts. With
		// --all it names none, and there is nothing to check by name.
		if hasCommandPair(args, "pm", "trust") {
			return specTargets(installPackagesArg(args, "trust")), nil
		}
	case "npx", "bunx":
		return specTargets(detectExecutorPackages(args)), nil
	}
	return nil, nil
}

// projectTargets reads what a project installs: the lockfile cmd installs
// from when it has one, otherwise package.json as declared. With an error for
// a pnpm, Yarn or Bun lockfile that is there and cannot be read, it returns
// what it read before it read those lockfiles, for the caller to offer.
func projectTargets(cmd string, platform nodePlatform) ([]verifyTarget, error) {
	dir := projectManifestDir()
	lock, ok, err := readPMLockfile(cmd, dir)
	if err != nil {
		fallback, _ := npmProjectTargets(platform)
		return fallback, err
	}
	if ok {
		return pmLockTargets(lock, platform, dir), nil
	}
	return npmProjectTargets(platform)
}

// npmProjectTargets reads package-lock.json when the project has one,
// otherwise package.json as declared.
func npmProjectTargets(platform nodePlatform) ([]verifyTarget, error) {
	lock, ok, err := readProjectLockfile(projectManifestDir())
	if err != nil {
		return nil, err
	}
	if ok {
		if t := lockTargets(lock, platform, ""); len(t) > 0 {
			return markTransitive(t, directDepSpecs(lock)), nil
		}
	}
	return specTargets(packagesFromPackageJSON()), nil
}

// directDepSpecs lists what the project itself declares, for markTransitive: the
// dependencies in package.json, and those of any lockfile entry that is not
// under node_modules, which is the root and each workspace member.
func directDepSpecs(lock packageLockFile) []string {
	var specs []string
	if m, ok := readManifestDeps(projectManifestDir()); ok {
		specs = manifestSpecs(m)
	}
	for path, e := range lock.Packages {
		if strings.Contains(path, "node_modules/") {
			continue
		}
		for _, deps := range []map[string]string{e.Dependencies, e.DevDependencies, e.OptionalDependencies, e.PeerDependencies} {
			for name, spec := range deps {
				specs = append(specs, manifestDepSpec(name, spec))
			}
		}
	}
	return specs
}

// manifestSpecs is every dependency package.json declares, in all four fields.
func manifestSpecs(m manifestDeps) []string {
	var specs []string
	for _, deps := range m.depMaps() {
		for name, spec := range deps {
			specs = append(specs, manifestDepSpec(name, spec))
		}
	}
	return specs
}

// markTransitive flags the lockfile targets whose package is not among the
// chosen specs: the packages the user named, or the project's own dependencies.
// With nothing chosen it flags nothing, so a project whose manifest could not be
// read keeps every check.
func markTransitive(targets []verifyTarget, chosen []string) []verifyTarget {
	if len(chosen) == 0 {
		return targets
	}
	names := map[string]bool{}
	for _, spec := range chosen {
		if n := targetPackageName(verifyTarget{spec: spec}); n != "" {
			names[n] = true
		}
		// An alias, lp@npm:left-pad, installs left-pad, which the lockfile names.
		if _, v := parsePackageQuery(spec); strings.HasPrefix(v, "npm:") {
			if n, _ := parsePackageQuery(strings.TrimPrefix(v, "npm:")); n != "" {
				names[n] = true
			}
		}
	}
	out := make([]verifyTarget, len(targets))
	for i, t := range targets {
		if t.fromLockfile() && !names[targetPackageName(t)] {
			t.transitive = true
		}
		out[i] = t
	}
	return out
}

// refreshTargets covers update, upgrade, dedupe and rebuild. Named packages
// are checked as named. Without names they act on the whole project.
func refreshTargets(cmd string, args []string, platform nodePlatform) ([]verifyTarget, error) {
	i := commandVerbIndex(args, refreshVerbs...)
	named := installPackagesArg(args, refreshVerbs...)
	verb := strings.ToLower(args[i])
	rebuild := verb == "rebuild" || verb == "rb"
	if len(named) > 0 && !rebuild {
		return specTargets(named), nil
	}
	lockCmd := cmd
	switch verb {
	case "update", "up", "upgrade", "udpate":
		// An update installs newer versions than the lockfile's. Checking the
		// locked ones would pass over what installs, and refuse an update
		// made to leave a vulnerable version behind.
		lockCmd = ""
	}
	project, err := projectTargets(lockCmd, platform)
	if err != nil || len(named) == 0 {
		return project, err
	}
	// rebuild runs the scripts of what is installed, so the installed versions
	// are the ones to check.
	want := map[string]bool{}
	for _, n := range named {
		want[n] = true
	}
	var picked []verifyTarget
	for _, t := range project {
		if want[targetPackageName(t)] {
			t.transitive = false // named on the command line
			picked = append(picked, t)
		}
	}
	if len(picked) > 0 {
		return picked, nil
	}
	return specTargets(named), nil
}

// runnerVerbs fetch a package and run it: `npm exec`, `pnpm dlx`, `bun x`.
var runnerVerbs = map[string][]string{
	"npm":  {"exec", "x"},
	"pnpm": {"dlx"},
	"yarn": {"dlx"},
	"bun":  {"x"},
}

// initializerVerbs fetch a create-* package and run it.
var initializerVerbs = map[string][]string{
	"npm":  {"create", "init"},
	"pnpm": {"create"},
	"yarn": {"create"},
	"bun":  {"create", "c"},
}

func runnerTargets(cmd string, args []string) []verifyTarget {
	if i := commandVerbIndex(args, runnerVerbs[cmd]...); i >= 0 {
		return specTargets(detectExecutorPackages(args[i+1:]))
	}
	if i := commandVerbIndex(args, initializerVerbs[cmd]...); i >= 0 {
		// Bare `npm init` writes a package.json and fetches nothing.
		if spec := nextPositional(args, i); spec != "" {
			return []verifyTarget{{spec: initializerPackage(spec)}}
		}
	}
	return nil
}

// initializerPackage is the package `npm init <x>` and `create <x>` run, by
// npm's rule: foo is create-foo, @scope is @scope/create, and @scope/foo is
// @scope/create-foo, each keeping its version.
func initializerPackage(spec string) string {
	if nonRegistrySpecKind(spec) != "" {
		return spec
	}
	name, version := parsePackageQuery(spec)
	pkg := "create-" + name
	if strings.HasPrefix(name, "@") {
		if scope, rest, ok := strings.Cut(name, "/"); ok {
			pkg = scope + "/create-" + rest
		} else {
			pkg = name + "/create"
		}
	}
	if version != "" {
		pkg += "@" + version
	}
	return pkg
}

// readProjectLockfile reads npm-shrinkwrap.json or package-lock.json from dir,
// in the order npm prefers them. ok is false when there is neither. err is a
// lockfile that is there and could not be read, which is not the same as no
// lockfile: see verifyBeforeRun.
func readProjectLockfile(dir string) (lock packageLockFile, ok bool, err error) {
	for _, name := range []string{"npm-shrinkwrap.json", "package-lock.json"} {
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(rerr, os.ErrNotExist) {
			continue
		}
		if rerr != nil {
			return packageLockFile{}, false, fmt.Errorf("%s could not be read: %w", name, rerr)
		}
		if jerr := json.Unmarshal(data, &lock); jerr != nil {
			return packageLockFile{}, false, fmt.Errorf("%s could not be parsed: %w", name, jerr)
		}
		return lock, true, nil
	}
	return packageLockFile{}, false, nil
}

// lockTargets lists what a lockfile installs on this platform, one target per
// distinct name, version and source. With installedRoot set, an entry already
// installed there at the same name and version is left out, because npm
// leaves it as it is and runs nothing of it.
func lockTargets(lock packageLockFile, platform nodePlatform, installedRoot string) []verifyTarget {
	declared := declaredSpecs(lock)
	seen := map[verifyTarget]bool{}
	var out []verifyTarget
	add := func(t verifyTarget) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for path, e := range lock.Packages {
		if e.Link {
			continue
		}
		name := lockEntryPackageName(path, e)
		if name == "" || e.Version == "" || !platform.allows(e.OS, e.CPU) {
			continue
		}
		if installedRoot != "" && isInstalledAt(installedRoot, path, name, e.Version) {
			continue
		}
		add(lockEntryTarget(name, e.Version, e.Resolved, e.Integrity, e.HasInstallScript, declared[name]))
	}
	var walk func(map[string]packageLockDep)
	walk = func(deps map[string]packageLockDep) {
		for name, dep := range deps {
			if dep.Version != "" {
				add(lockEntryTarget(name, dep.Version, dep.Resolved, dep.Integrity, false, nil))
			}
			walk(dep.Dependencies)
		}
	}
	walk(lock.Dependencies)
	sort.Slice(out, func(i, j int) bool { return out[i].spec < out[j].spec })
	return out
}

// isInstalledAt reports whether the package.json at root/path names this
// package at this version.
func isInstalledAt(root, path, name, version string) bool {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path), "package.json"))
	if err != nil {
		return false
	}
	var m struct{ Name, Version string }
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	return m.Name == name && m.Version == version
}

// lockEntryTarget classifies one lockfile entry by where it is fetched from.
func lockEntryTarget(name, version, resolved, integrity string, scripts bool, declared []string) verifyTarget {
	t := verifyTarget{spec: name + "@" + version, name: name, resolved: resolved, integrity: integrity, hasInstallScript: scripts, locked: true}
	lower := strings.ToLower(resolved)
	switch {
	case resolved == "":
	case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
		// A URL is the registry's tarball unless the dependency asked for a
		// URL. That is what holds an entry to the registry's record of its name
		// and version: otherwise any lockfile could call any tarball left-pad.
		if registryTarballName(resolved) == "" && declaresURL(declared) {
			t.sourceKind = "a URL"
		}
	default:
		t.sourceKind = nonRegistrySpecKind(resolved)
	}
	if t.sourceKind != "" {
		t.spec = name
	}
	return t
}

func declaresURL(specs []string) bool {
	for _, s := range specs {
		if nonRegistrySpecKind(s) == "a URL" {
			return true
		}
	}
	return false
}

// declaredSpecs collects, for each dependency name, every spec a lockfile
// entry declares for it.
func declaredSpecs(lock packageLockFile) map[string][]string {
	out := map[string][]string{}
	for _, e := range lock.Packages {
		for _, deps := range []map[string]string{e.Dependencies, e.DevDependencies, e.OptionalDependencies, e.PeerDependencies} {
			for name, spec := range deps {
				out[name] = append(out[name], spec)
			}
		}
	}
	return out
}

// manifestDeps is the dependency part of a package.json.
type manifestDeps struct {
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	Workspaces           json.RawMessage   `json:"workspaces"`
}

func (m manifestDeps) depMaps() []map[string]string {
	return []map[string]string{m.Dependencies, m.DevDependencies, m.OptionalDependencies, m.PeerDependencies}
}

func readManifestDeps(dir string) (manifestDeps, bool) {
	var m manifestDeps
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return m, false
	}
	if err := json.Unmarshal(data, &m); err != nil {
		LogWarn("Failed to parse package.json for verification: %v", err)
		return m, false
	}
	return m, true
}

// manifestDepSpec is name@spec for a package.json dependency, which the checks
// read as the declared version or range.
func manifestDepSpec(name, spec string) string {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "", strings.HasPrefix(spec, "catalog:"):
		// A pnpm catalog keeps the version in pnpm-workspace.yaml, which nvx does
		// not read, so the newest version is checked.
		return name
	case strings.Contains(spec, " - "):
		// A hyphen range is outside the range grammar nvx resolves (see
		// semver_range.go), and an unresolvable version prompts.
		return name
	}
	return name + "@" + spec
}

// lockMatchesManifest reports whether a lockfile was written for the
// package.json beside it, by comparing what each declares at the root. When
// they match, `npm install` installs the lockfile as it is.
func lockMatchesManifest(lock packageLockFile, m manifestDeps) bool {
	root, ok := lock.Packages[""]
	if !ok {
		return false
	}
	have := []map[string]string{root.Dependencies, root.DevDependencies, root.OptionalDependencies, root.PeerDependencies}
	for i, want := range m.depMaps() {
		if len(want) == 0 && len(have[i]) == 0 {
			continue
		}
		if !reflect.DeepEqual(want, have[i]) {
			return false
		}
	}
	return true
}

// stringList reads a JSON string or array of strings, which is how package
// metadata spells os and cpu.
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		// Malformed here is not worth losing the whole lockfile over. An entry
		// with no list is checked whatever the platform.
		*l = nil
		return nil
	}
	*l = many
	return nil
}

// nodePlatform is a platform in Node's names. The zero value matches every
// entry.
type nodePlatform struct{ os, cpu string }

func hostNodePlatform() nodePlatform {
	return nodePlatform{nodePlatformName(runtime.GOOS), nodeArchName(runtime.GOARCH)}
}

// installPlatform is where the install will run: this machine, or Linux in a
// container under the docker provider.
func installPlatform(req verifyRequest) nodePlatform {
	provider := req.launch.FilesystemProvider
	if provider == "" {
		if p, err := LoadPolicy(req.nvxHome); err == nil {
			provider = p.FilesystemProvider()
		}
	}
	p := hostNodePlatform()
	if strings.EqualFold(provider, "docker") {
		p.os = "linux"
	}
	return p
}

func nodePlatformName(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func nodeArchName(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "ppc64le":
		return "ppc64"
	}
	return goarch
}

// allows applies npm's os and cpu rule: an entry for another platform is
// skipped by npm, so it is not checked either.
func (p nodePlatform) allows(osList, cpuList []string) bool {
	if p.os == "" {
		return true
	}
	return platformListAllows(osList, p.os) && platformListAllows(cpuList, p.cpu)
}

// platformListAllows is npm-install-checks' checkList: a "!x" entry excludes
// x, and a list of plain entries allows only those.
func platformListAllows(list []string, value string) bool {
	if len(list) == 0 || (len(list) == 1 && list[0] == "any") {
		return true
	}
	match, negated := false, 0
	for _, v := range list {
		if strings.HasPrefix(v, "!") {
			if v[1:] == value {
				return false
			}
			negated++
			continue
		}
		match = match || v == value
	}
	return match || negated == len(list)
}
