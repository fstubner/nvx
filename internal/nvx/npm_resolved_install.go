package nvx

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Installing what was checked.
//
// npm ran twice. The first run resolved the tree and nvx checked what it wrote.
// The second run resolved the tree again and installed that. The two trees agree
// only while the registry does. A version published between the runs was
// installed without ever being checked, and the second run did all the work of
// the first a second time.
//
// The second run now starts from the lockfile the first one wrote. npm installs
// a lockfile that matches package.json as it stands and asks the registry for
// nothing but the tarballs, so what is installed is what was checked. The
// lockfile is put in the project before the install, and taken out again if npm
// does not end up using it, so the project's own lockfile is what npm would have
// left. See adopt.
//
// This is not done for every command npm resolves, only where the second run
// would otherwise be a fresh resolution of something already resolved:
//
//   - A bare install, with or without a lockfile. The lockfile is all it needs.
//   - An install that names packages, when every name can be pinned to the
//     version the lockfile holds without changing what npm saves in package.json.
//     npm treats a named package as a request and asks the registry about it
//     again however the lockfile reads, so the name is made exact first.
//   - Not update or dedupe. They ask the registry for something newer than the
//     lockfile holds, which is what they are for. A lockfile cannot stand in for
//     that, and installing instead of updating would also run the project's own
//     install scripts, which npm update does not.
//   - Not an install that names a range, an alias, a URL or anything else pinning
//     would change the meaning of, nor one with a flag nvx does not know takes no
//     value. Those run as they did.

// adoptCheckedLockfile turns the handoff off. A variable so a test can show what
// an install gets without it.
var adoptCheckedLockfile = true

// resolvedInstall is what npm's resolution pass was given and wrote.
type resolvedInstall struct {
	root string
	// lock is the lockfile the pass wrote, and lockfile what it says.
	lock     []byte
	lockfile packageLockFile
	// The project's own files as the pass was given them, nil for a file that
	// did not exist. If they differ when the install starts, the project changed
	// while the checks ran and the lockfile no longer describes it.
	manifest, projectLock []byte
}

// record keeps what the pass wrote, when it is something the install can start
// from. scratch is where the pass ran, given the project's files by name.
func (r *resolvedInstall) record(root, scratch string, given map[string][]byte) {
	if r == nil {
		return
	}
	*r = resolvedInstall{}
	// A shrinkwrap is what npm reads first, and the pass wrote to it. The
	// project's lockfile is not the one the install would use.
	if given["npm-shrinkwrap.json"] != nil {
		LogDetail("The project has an npm-shrinkwrap.json, so the install resolves again.")
		return
	}
	lock, err := os.ReadFile(filepath.Join(scratch, "package-lock.json"))
	if err != nil {
		return
	}
	written, ok := readManifestDeps(scratch)
	if !ok {
		return
	}
	lock, written = withoutProjectLink(lock, written, root, scratch)
	var parsed packageLockFile
	if json.Unmarshal(lock, &parsed) != nil {
		return
	}
	// The lockfile must describe the package.json the pass ended with, which
	// carries the packages the command named. One that does not is a lockfile npm
	// would resolve further, and so not one to install from.
	if !lockMatchesManifest(parsed, written) {
		LogDetail("The lockfile npm wrote does not match the package.json it ended with, so the install resolves again.")
		return
	}
	if path := firstUnhashedEntry(parsed); path != "" {
		LogDetail("The lockfile npm wrote holds %s, which is not a tarball with a hash, so the install resolves again.", path)
		return
	}
	*r = resolvedInstall{
		root: root, lock: lock, lockfile: parsed,
		manifest: given["package.json"], projectLock: given["package-lock.json"],
	}
}

// withoutProjectLink takes out of the lockfile and the package.json the pass
// wrote what npm 7 to 10 add to an install that names no package, when it is run
// from the project with another folder as its prefix, which is how the pass runs.
// They install the project into that folder as a file: dependency, so the
// lockfile gains the project's own files as an entry, a link to it, and a
// dependency on it. Measured 2026-10-08, npm 7.24.2, 8.19.4, 9.9.4, 10.9.3 and
// 10.9.9 do this and 11.11.0 does not. With 10.9.9 and a project of 321 packages
// the rest of the lockfile was the same as one resolved on its own. The project
// is found by its path, the one the dependency's file: spec names. Nothing else
// is changed, and a lockfile without it comes back as it was.
func withoutProjectLink(lock []byte, written manifestDeps, root, scratch string) ([]byte, manifestDeps) {
	rel, err := filepath.Rel(scratch, root)
	if err != nil {
		return lock, written
	}
	rel = filepath.ToSlash(rel)
	name := ""
	for dep, spec := range written.Dependencies {
		if spec == "file:"+rel {
			name = dep
		}
	}
	if name == "" {
		return lock, written
	}

	var top map[string]json.RawMessage
	var packages map[string]json.RawMessage
	var rootEntry map[string]json.RawMessage
	var rootDeps map[string]string
	if json.Unmarshal(lock, &top) != nil || json.Unmarshal(top["packages"], &packages) != nil ||
		json.Unmarshal(packages[""], &rootEntry) != nil || json.Unmarshal(rootEntry["dependencies"], &rootDeps) != nil ||
		rootDeps[name] != "file:"+rel {
		return lock, written
	}
	// The link is what npm writes for it. Anything else by that name is not this.
	if raw, there := packages["node_modules/"+name]; there {
		var link struct {
			Link     bool   `json:"link"`
			Resolved string `json:"resolved"`
		}
		if json.Unmarshal(raw, &link) != nil || !link.Link || link.Resolved != rel {
			return lock, written
		}
	}
	delete(rootDeps, name)
	delete(packages, rel)
	delete(packages, "node_modules/"+name)
	var ok bool
	if rootEntry["dependencies"], ok = marshalRaw(rootDeps); !ok {
		return lock, written
	}
	if packages[""], ok = marshalRaw(rootEntry); !ok {
		return lock, written
	}
	if top["packages"], ok = marshalRaw(packages); !ok {
		return lock, written
	}
	// lockfileVersion 2 repeats the tree in the older form npm 6 reads.
	var legacy map[string]json.RawMessage
	if json.Unmarshal(top["dependencies"], &legacy) == nil {
		delete(legacy, name)
		if top["dependencies"], ok = marshalRaw(legacy); !ok {
			return lock, written
		}
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return lock, written
	}

	clean := written
	clean.Dependencies = map[string]string{}
	for dep, spec := range written.Dependencies {
		if dep != name {
			clean.Dependencies[dep] = spec
		}
	}
	return out, clean
}

func marshalRaw(v any) (json.RawMessage, bool) {
	b, err := json.Marshal(v)
	return b, err == nil
}

// firstUnhashedEntry returns the path of a package the lockfile lists that is not
// a tarball fetched over HTTP with a hash to check it against, or "" when every
// one is. Those are the entries the checks hold to the registry's record of
// their name and version. A link, a git source, a local folder and a package
// bundled inside another have no such record, so a lockfile with one is left
// for npm to resolve as it did.
func firstUnhashedEntry(lock packageLockFile) string {
	var first string
	for path, e := range lock.Packages {
		if path == "" {
			continue
		}
		resolved := strings.ToLower(e.Resolved)
		if e.Link || e.Integrity == "" || !(strings.HasPrefix(resolved, "https://") || strings.HasPrefix(resolved, "http://")) {
			// The least path, so that the same lockfile always names the same one.
			if first == "" || path < first {
				first = path
			}
		}
	}
	return first
}

// versions maps each package at the top of the lockfile's tree to its version,
// leaving out links and aliases, whose names do not say what they install.
func (r *resolvedInstall) versions() map[string]string {
	out := map[string]string{}
	for path, e := range r.lockfile.Packages {
		name, ok := strings.CutPrefix(path, "node_modules/")
		if !ok || strings.Contains(name, "node_modules/") || e.Link || e.Name != "" || e.Version == "" {
			continue
		}
		out[name] = e.Version
	}
	return out
}

// adopt puts the resolved lockfile in the project and returns the command to run
// in place of args, with a function to call once it has finished. When the
// command is not one the lockfile can stand in for, or the project is not as the
// pass found it, it returns args and a nil function and nothing has changed.
//
// finish takes the lockfile out again if npm did not write it. A lockfile npm
// ends up using is rewritten by npm whatever happens to the install, so a file
// still dated as it was left is a file npm did not use. That is what a run that
// stops before npm saves leaves, and so do a project whose settings turn the
// lockfile off and an install with --no-save. Native npm leaves the lockfile
// alone in all of them, and so does this.
func (r *resolvedInstall) adopt(cmdName string, args []string, pmCmd string, pmArgs []string) ([]string, func()) {
	if !adoptCheckedLockfile || r == nil || len(r.lock) == 0 {
		return args, nil
	}
	// The command nvx resolved is the command it will run. A package manager
	// reached through corepack or its entry script has not been read the same way.
	if !strings.EqualFold(cmdName, "npm") || !strings.EqualFold(pmCmd, "npm") || !slices.Equal(args, pmArgs) {
		return args, nil
	}
	read, _ := readCommand("npm", args)
	verb := commandVerbIndex(read, npmResolveVerbs...)
	if verb < 0 || len(read) != len(args) {
		return args, nil
	}
	if v := strings.ToLower(read[verb]); v != "install" && v != "install-test" {
		LogDetail("npm %s resolves the tree itself, so it does that again after the checks.", v)
		return args, nil
	}
	run, ok := pinInstallArgs(args, verb, r.versions())
	if !ok {
		LogDetail("npm resolves this install again after the checks, because it names a package or a flag that cannot be pinned to the checked lockfile.")
		return args, nil
	}
	if !r.projectUnchanged() {
		LogDetail("The project changed while its install was being checked, so npm resolves it again.")
		return args, nil
	}
	s, ok := stageLockfile(filepath.Join(r.root, "package-lock.json"), r.lock)
	if !ok {
		return args, nil
	}
	LogDetail("Installing from the lockfile the checks were run on, so what is installed is what was checked.")
	return run, s.finish
}

// projectUnchanged reports whether the project's package.json and lockfile are
// the bytes the resolution pass was given, and there is still no shrinkwrap.
func (r *resolvedInstall) projectUnchanged() bool {
	same := func(name string, given []byte) bool {
		now, err := os.ReadFile(filepath.Join(r.root, name))
		if errors.Is(err, os.ErrNotExist) {
			return given == nil
		}
		return err == nil && given != nil && bytes.Equal(now, given)
	}
	return same("package.json", r.manifest) && same("package-lock.json", r.projectLock) && same("npm-shrinkwrap.json", nil)
}

// stagedLockfile is a lockfile put in the project before an install.
type stagedLockfile struct {
	path    string
	content []byte
	// modTime is the date the file was left with, which only npm writing it can
	// change.
	modTime time.Time
	// What was there before.
	existed bool
	prev    []byte
	mode    os.FileMode
	prevMod time.Time
}

// stagedLockfileDate is the date a staged lockfile is left with. A fixed, plain,
// old date, so that a file npm has written cannot be mistaken for one it has not.
var stagedLockfileDate = time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

// stageLockfile writes content to path, over a lockfile that is there already,
// and leaves it dated stagedLockfileDate.
func stageLockfile(path string, content []byte) (*stagedLockfile, bool) {
	s := &stagedLockfile{path: path, content: content, mode: 0o644}
	// Lstat, so that a lockfile which is a link is left to npm as it was.
	if info, err := os.Lstat(path); err == nil {
		prev, rerr := os.ReadFile(path)
		if rerr != nil || !info.Mode().IsRegular() {
			return nil, false
		}
		s.existed, s.prev, s.mode, s.prevMod = true, prev, info.Mode().Perm(), info.ModTime()
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false
	}
	if err := os.WriteFile(path, content, s.mode); err != nil {
		return nil, false
	}
	if err := os.Chtimes(path, stagedLockfileDate, stagedLockfileDate); err != nil {
		s.restore()
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil {
		s.restore()
		return nil, false
	}
	s.modTime = info.ModTime()
	return s, true
}

// finish puts the project's lockfile back as it was if npm did not write the
// staged one. A file changed by anything else is left alone.
func (s *stagedLockfile) finish() {
	info, err := os.Stat(s.path)
	if err != nil || !info.ModTime().Equal(s.modTime) || info.Size() != int64(len(s.content)) {
		return
	}
	if now, err := os.ReadFile(s.path); err != nil || !bytes.Equal(now, s.content) {
		return
	}
	s.restore()
}

// restore puts back what was there before the staged file, or removes it.
func (s *stagedLockfile) restore() {
	if !s.existed {
		_ = os.Remove(s.path)
		return
	}
	if err := os.WriteFile(s.path, s.prev, s.mode); err == nil {
		_ = os.Chtimes(s.path, s.prevMod, s.prevMod)
	}
}

var (
	// A package name as the registry has accepted them for years. Anything else
	// is left for npm to read.
	pinnableName = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)
	exactVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	// A dist-tag such as latest or next. A tag with a digit in it could be read
	// as a range, so it is not one this pins.
	distTagName = regexp.MustCompile(`^[a-z][a-z-]+$`)
)

// npmInstallFlagsWithoutValue are the install flags that take no value, so that
// the token after one is a package and not the flag's argument. A flag that is
// not here and is not written --flag=value might swallow the next token, and
// then nvx cannot tell what the packages are.
var npmInstallFlagsWithoutValue = map[string]bool{
	"-D": true, "--save-dev": true, "-P": true, "--save-prod": true, "-O": true,
	"--save-optional": true, "--save-peer": true, "-E": true, "--save-exact": true,
	"-B": true, "--save-bundle": true, "-S": true, "--save": true, "--no-save": true,
	"--audit": true, "--no-audit": true, "--fund": true, "--no-fund": true,
	"-f": true, "--force": true, "--legacy-peer-deps": true, "--strict-peer-deps": true,
	"--ignore-scripts": true, "--foreground-scripts": true, "--prefer-offline": true,
	"--prefer-online": true, "--offline": true, "--no-optional": true, "--production": true,
	"--no-bin-links": true, "--install-links": true, "--global-style": true,
	"--legacy-bundling": true, "--no-progress": true, "--no-update-notifier": true,
	"--silent": true, "-s": true, "--quiet": true, "-q": true, "--verbose": true,
	"-d": true, "-dd": true, "-ddd": true, "--no-color": true, "--dry-run": true,
	"--no-package-lock": true,
}

// pinInstallArgs returns args with each package it names written as an exact
// version taken from versions, and whether that could be done for all of them.
// verb is where npm's command sits in args.
//
// npm saves a package named without a version, or with a tag, as a range around
// the version it installed, and saves an exact version the same way. Naming the
// version the lockfile holds therefore saves what the original command would
// have saved. A range is saved as it was typed, which pinning would change.
func pinInstallArgs(args []string, verb int, versions map[string]string) ([]string, bool) {
	out := slices.Clone(args)
	for i := verb + 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return nil, false
		case strings.HasPrefix(a, "-"):
			if !strings.Contains(a, "=") && !npmInstallFlagsWithoutValue[a] {
				return nil, false
			}
		default:
			pinned, ok := pinSpec(a, versions)
			if !ok {
				return nil, false
			}
			out[i] = pinned
		}
	}
	return out, true
}

// pinSpec writes one named package as name@version, or reports that it cannot.
func pinSpec(spec string, versions map[string]string) (string, bool) {
	name, rest := spec, ""
	if at := strings.LastIndex(spec, "@"); at > 0 {
		name, rest = spec[:at], spec[at+1:]
	}
	if !pinnableName.MatchString(name) {
		return "", false
	}
	version, ok := versions[name]
	if !ok {
		return "", false
	}
	switch {
	case rest == "" || distTagName.MatchString(rest):
		return name + "@" + version, true
	case exactVersion.MatchString(rest) && rest == version:
		return spec, true
	}
	return "", false
}
