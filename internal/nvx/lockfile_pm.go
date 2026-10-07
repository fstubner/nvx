package nvx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The lockfiles of pnpm, Yarn and Bun.
//
// A bare `pnpm install`, `yarn` or `bun install` installs what its lockfile
// lists. The checks read package.json until 2026-10-06, so they saw the
// project's own dependencies and none of what those brought in: in a project
// whose pnpm-lock.yaml held is-odd and the is-number it depends on, a
// blocked_packages entry for is-number did not stop `pnpm install`.
//
// Each lockfile is read into the same entries, which go through the checks a
// package-lock.json entry gets. A lockfile that is there and cannot be read is
// an error, never a partial list: see verifyBeforeRun.

// pmLockfile is what a pnpm, Yarn or Bun lockfile says a project installs.
type pmLockfile struct {
	file    string
	entries []pmLockEntry
	// declared is every dependency the lockfile records a project manifest
	// declaring, the root's and each workspace member's, as name@spec.
	declared []string
	// membersUnrecorded is a lockfile that does not record what workspace
	// members declare, which is Yarn 1's.
	membersUnrecorded bool
	// rootRecords says whether the lockfile records the root package.json
	// declaring name at spec. A lockfile written for an older package.json
	// does not, and the package manager resolves that dependency afresh.
	rootRecords func(name, spec string) bool
	// unrecorded is every dependency an entry or workspace depends on that
	// has no entry of its own, as name@spec. The package manager resolves it
	// afresh: pnpm 10 and Yarn 1 both installed is-number when its entry was
	// deleted and is-odd's still named it.
	unrecorded []string
}

// pmLockEntry is one package a lockfile installs.
type pmLockEntry struct {
	name, version string
	// resolved and integrity are the tarball URL and hash, where the lockfile
	// records them. pnpm and Bun record a hash and no URL.
	resolved, integrity string
	// sourceKind names a source other than the registry, as
	// nonRegistrySpecKind does, or is "".
	sourceKind string
	os, cpu    []string
	scripts    bool
}

// pmLockfileName is the lockfile cmd installs from, other than npm's.
func pmLockfileName(cmd string) string {
	switch strings.ToLower(cmd) {
	case "pnpm":
		return "pnpm-lock.yaml"
	case "yarn":
		return "yarn.lock"
	case "bun":
		return "bun.lock"
	}
	return ""
}

// readPMLockfile reads the lockfile cmd installs from in dir. ok is false when
// there is none. err is one that is there and could not be read.
func readPMLockfile(cmd, dir string) (lock pmLockfile, ok bool, err error) {
	name := pmLockfileName(cmd)
	if name == "" {
		return pmLockfile{}, false, nil
	}
	data, rerr := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(rerr, os.ErrNotExist) {
		if name == "bun.lock" {
			if _, serr := os.Stat(filepath.Join(dir, "bun.lockb")); serr == nil {
				LogWarn("bun.lockb is Bun's binary lockfile, which nvx does not read. The checks cover what package.json declares, and not the packages those bring in. Running `bun install --save-text-lockfile` once writes bun.lock, which nvx reads.")
			}
		}
		return pmLockfile{}, false, nil
	}
	if rerr != nil {
		return pmLockfile{}, false, fmt.Errorf("%s could not be read: %w", name, rerr)
	}
	var perr error
	switch name {
	case "pnpm-lock.yaml":
		lock, perr = parsePnpmLock(data)
	case "yarn.lock":
		lock, perr = parseYarnLock(data)
	case "bun.lock":
		lock, perr = parseBunLock(data)
	}
	if perr != nil {
		return pmLockfile{}, false, fmt.Errorf("%s could not be parsed: %w", name, perr)
	}
	lock.file = name
	return lock, true, nil
}

// pmLockTargets lists what lock installs on this platform, the dependencies
// it names and has no entry for, and what package.json in dir declares that
// lock does not record.
func pmLockTargets(lock pmLockfile, platform nodePlatform, dir string) []verifyTarget {
	seen := map[verifyTarget]bool{}
	var targets []verifyTarget
	for _, e := range lock.entries {
		if !platform.allows(e.os, e.cpu) {
			continue
		}
		var t verifyTarget
		if e.sourceKind != "" {
			t = verifyTarget{spec: e.name, name: e.name, sourceKind: e.sourceKind, locked: true}
		} else {
			t = lockEntryTarget(e.name, e.version, e.resolved, e.integrity, e.scripts, nil)
		}
		if !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].spec < targets[j].spec })
	locked := len(targets)

	m, hasManifest := readManifestDeps(dir)
	chosen := append(manifestSpecs(m), lock.declared...)
	if lock.membersUnrecorded {
		chosen = append(chosen, workspaceMemberSpecs(dir, m)...)
	}
	targets = markTransitive(targets, chosen)

	if len(lock.unrecorded) > 0 {
		names := map[string]bool{}
		for _, spec := range lock.unrecorded {
			t := verifyTarget{spec: spec}
			if !seen[t] {
				seen[t] = true
				targets = append(targets, t)
				names[targetPackageName(t)] = true
			}
		}
		list := make([]string, 0, len(names))
		for n := range names {
			list = append(list, n)
		}
		sort.Strings(list)
		LogWarn("%s has no entry for %s, which other entries in it depend on. The package manager resolves those afresh, so they are checked as declared, and the packages they bring in are not checked.",
			lock.file, strings.Join(list, ", "))
	}

	if hasManifest && lock.rootRecords != nil {
		var stale []string
		for _, deps := range []map[string]string{m.Dependencies, m.DevDependencies, m.OptionalDependencies} {
			for name, spec := range deps {
				if !lock.rootRecords(name, spec) {
					stale = append(stale, name)
					targets = append(targets, verifyTarget{spec: manifestDepSpec(name, spec)})
				}
			}
		}
		if len(stale) > 0 {
			sort.Strings(stale)
			LogWarn("%s was written for another version of package.json, which did not declare %s as it does now. The package manager resolves those afresh, so they are checked as package.json declares them, and the packages they bring in are not checked.",
				lock.file, strings.Join(stale, ", "))
		}
	}
	LogDetail("Checking the %d packages %s installs on this platform.", locked, lock.file)
	return targets
}

// workspacePatterns reads package.json's workspaces field, which is a list of
// patterns or an object holding them as "packages".
func workspacePatterns(raw json.RawMessage) []string {
	if s := strings.TrimSpace(string(raw)); s == "" || s == "null" {
		return nil
	}
	var patterns []string
	if json.Unmarshal(raw, &patterns) != nil {
		var obj struct {
			Packages []string `json:"packages"`
		}
		if json.Unmarshal(raw, &obj) != nil {
			return nil
		}
		patterns = obj.Packages
	}
	return patterns
}

// workspaceMemberSpecs reads the dependencies of each workspace member that
// package.json's workspaces field names, for a lockfile that does not record
// them. A pattern is matched as filepath.Glob matches it, so ** is one folder.
func workspaceMemberSpecs(dir string, m manifestDeps) []string {
	var specs []string
	for _, p := range workspacePatterns(m.Workspaces) {
		if strings.HasPrefix(p, "!") {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(p, "**", "*")), "package.json"))
		for _, match := range matches {
			if member, ok := readManifestDeps(filepath.Dir(match)); ok {
				specs = append(specs, manifestSpecs(member)...)
			}
		}
	}
	return specs
}

// isPlainVersion reports a registry version, as opposed to a URL, path or
// other reference a lockfile writes in its place.
func isPlainVersion(v string) bool {
	return v != "" && v[0] >= '0' && v[0] <= '9' && !strings.ContainsAny(v, ":/\\ ")
}

// isTarballPath reports a file: path that names a tarball, as opposed to a
// folder.
func isTarballPath(p string) bool {
	p, _, _ = strings.Cut(p, "#")
	p, _, _ = strings.Cut(p, "::")
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tar")
}

// splitLocator splits name@reference, where a scoped name starts with "@".
func splitLocator(s string) (name, ref string, ok bool) {
	if len(s) < 2 {
		return "", "", false
	}
	i := strings.Index(s[1:], "@")
	if i < 0 {
		return "", "", false
	}
	name, ref = s[:i+1], s[i+2:]
	if name == "" || ref == "" || (strings.HasPrefix(name, "@") && !strings.Contains(name, "/")) {
		return "", "", false
	}
	return name, ref, true
}

// localSourceEntry classifies a reference to the local disk or a workspace.
// skip is a folder: the project's own code, which a package-lock.json records
// as a link and the checks leave out in the same way.
func localSourceEntry(name, ref string) (e pmLockEntry, skip, ok bool) {
	lower := strings.ToLower(ref)
	switch {
	case strings.HasPrefix(lower, "workspace:"), strings.HasPrefix(lower, "link:"), strings.HasPrefix(lower, "portal:"):
		return pmLockEntry{}, true, true
	case strings.HasPrefix(lower, "file:"):
		if isTarballPath(ref[len("file:"):]) {
			return pmLockEntry{name: name, sourceKind: "a file: spec"}, false, true
		}
		return pmLockEntry{}, true, true
	}
	return pmLockEntry{}, false, false
}
