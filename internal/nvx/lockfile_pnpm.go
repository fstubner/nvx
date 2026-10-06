package nvx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Reading pnpm-lock.yaml, lockfileVersion 5.x (pnpm 7), 6.x (pnpm 8) and 9.x
// (pnpm 9 to 12).
//
// `packages` holds one entry per package and version, keyed /name/1.0.0 in 5.x,
// /name@1.0.0 in 6.x and name@1.0.0 in 9.x, with a peer suffix on some. Its
// `resolution` is an integrity hash for a registry package, or a tarball URL,
// a git repository or a folder for anything else. The importers, one per
// workspace member, record what each package.json declared.
//
// pnpm 10 and later can put a second document first, separated by "---",
// listing pnpm itself and its config dependencies. Both are read.

func parsePnpmLock(data []byte) (pmLockfile, error) {
	docs, err := parseYAMLDocuments(data)
	if err != nil {
		return pmLockfile{}, err
	}
	if len(docs) == 0 {
		return pmLockfile{}, errors.New("it is empty")
	}
	out := pmLockfile{}
	root := map[string]string{}
	for _, doc := range docs {
		if doc.kind != yamlMap {
			return pmLockfile{}, errors.New("it is not a mapping")
		}
		major, err := pnpmLockMajor(doc.get("lockfileVersion"))
		if err != nil {
			return pmLockfile{}, err
		}
		importers := doc.get("importers")
		if importers == nil {
			// A 5.x or 6.x lockfile without workspaces records the root at
			// the top level.
			importers = &yamlNode{kind: yamlMap, keys: []string{"."}, fields: map[string]*yamlNode{".": doc}}
		}
		if importers.kind != yamlMap {
			return pmLockfile{}, fmt.Errorf("line %d: importers is not a mapping", importers.line)
		}
		for _, path := range importers.keys {
			specs, err := pnpmImporterSpecs(importers.fields[path], major)
			if err != nil {
				return pmLockfile{}, err
			}
			for name, spec := range specs {
				out.declared = append(out.declared, manifestDepSpec(name, spec))
				if path == "." {
					root[name] = spec
				}
			}
		}
		pkgs := doc.get("packages")
		if pkgs == nil {
			pkgs = &yamlNode{kind: yamlMap, fields: map[string]*yamlNode{}}
		}
		if pkgs.kind != yamlMap {
			return pmLockfile{}, fmt.Errorf("line %d: packages is not a mapping", pkgs.line)
		}
		have := map[string]bool{}
		for _, key := range pkgs.keys {
			e, skip, err := pnpmEntry(key, pkgs.fields[key], major)
			if err != nil {
				return pmLockfile{}, err
			}
			if !skip {
				out.entries = append(out.entries, e)
			}
			have[pnpmKeyWithoutPeers(key, major)] = true
		}
		for _, r := range pnpmReferences(doc, importers, major) {
			if key, spec, check := pnpmReferenceKey(r[0], r[1], major); check && !have[key] {
				out.unrecorded = append(out.unrecorded, spec)
			}
		}
	}
	out.rootRecords = func(name, spec string) bool {
		got, ok := root[name]
		return ok && got == spec
	}
	return out, nil
}

// pnpmLockMajor reads lockfileVersion, which is quoted from 6.0 on.
func pnpmLockMajor(n *yamlNode) (int, error) {
	v := n.str()
	if v == "" {
		return 0, errors.New("it has no lockfileVersion")
	}
	head, _, _ := strings.Cut(v, ".")
	major, err := strconv.Atoi(head)
	if err != nil {
		return 0, errors.New("its lockfileVersion is not a number")
	}
	switch major {
	case 5, 6, 9:
		return major, nil
	}
	return 0, fmt.Errorf("its lockfileVersion %d is one this version of nvx does not read", major)
}

// pnpmImporterSpecs is what one importer records its package.json declaring,
// by name. pnpm 10's config dependencies and the pnpm it runs as are listed
// the same way in the document that holds them.
func pnpmImporterSpecs(imp *yamlNode, major int) (map[string]string, error) {
	out := map[string]string{}
	if imp == nil || imp.kind == yamlNull {
		return out, nil
	}
	if imp.kind != yamlMap {
		return nil, fmt.Errorf("line %d: an importer is not a mapping", imp.line)
	}
	if major == 5 {
		for name, spec := range imp.get("specifiers").stringMap() {
			out[name] = spec
		}
		return out, nil
	}
	for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies", "configDependencies", "packageManagerDependencies"} {
		deps := imp.get(field)
		if deps == nil || deps.kind != yamlMap {
			continue
		}
		for _, name := range deps.keys {
			d := deps.fields[name]
			spec := d.get("specifier").str()
			if spec == "" {
				// A 6.x lockfile at the top level lists the root's
				// dependencies here too, as the same mapping.
				spec = d.str()
			}
			if spec != "" {
				out[name] = spec
			}
		}
	}
	return out, nil
}

// pnpmEntry reads one `packages` entry. skip is a folder on disk.
func pnpmEntry(key string, e *yamlNode, major int) (pmLockEntry, bool, error) {
	bad := func(what string) error {
		line := 0
		if e != nil {
			line = e.line
		}
		return fmt.Errorf("line %d: %s", line, what)
	}
	if e == nil || e.kind != yamlMap {
		return pmLockEntry{}, false, bad("a package entry that is not a mapping")
	}
	// A registry package's key names it: /name@1.0.0 before 9.x, name@1.0.0
	// in 9.x. Anything else, a URL, a git repository or a folder, is keyed
	// by where it comes from, and the entry names the package.
	keyName, keyRef, keyOK := splitPnpmKey(key, major)
	registryKey := keyOK && isPlainVersion(keyRef) && (major == 9 || strings.HasPrefix(key, "/"))
	name, version := e.get("name").str(), e.get("version").str()
	if registryKey && ((name != "" && name != keyName) || (version != "" && version != keyRef)) {
		// pnpm installs the name and version the entry states, so an entry
		// keyed as one package and naming another is not read either way.
		return pmLockEntry{}, false, bad("a package entry whose name or version is not the one its key names")
	}
	if name == "" {
		if !keyOK {
			return pmLockEntry{}, false, bad("a package entry nvx cannot read a name and version from")
		}
		name = keyName
	}
	if version == "" {
		version = keyRef
	}
	entry := pmLockEntry{name: name, version: version,
		os: e.get("os").list(), cpu: e.get("cpu").list(),
		scripts: e.get("requiresBuild").str() == "true"}

	res := e.get("resolution")
	if res == nil || res.kind != yamlMap {
		return pmLockEntry{}, false, bad("a package entry with no resolution")
	}
	typ := res.get("type").str()
	tarball, integrity := res.get("tarball").str(), res.get("integrity").str()
	switch {
	case typ == "directory" || res.get("directory") != nil:
		return pmLockEntry{}, true, nil
	case typ == "git" || res.get("repo") != nil:
		entry.sourceKind = "a git URL"
	case typ != "":
		return pmLockEntry{}, false, bad("a package entry whose resolution nvx does not read")
	case !registryKey:
		// A tarball the project asked for by URL or path.
		if tarball == "" {
			return pmLockEntry{}, false, bad("a package entry whose resolution nvx does not read")
		}
		if e, skip, ok := localSourceEntry(name, tarball); ok {
			return e, skip, nil
		}
		entry.sourceKind = nonRegistrySpecKind(tarball)
		if entry.sourceKind == "" {
			return pmLockEntry{}, false, bad("a package entry whose tarball nvx does not read")
		}
	case !isPlainVersion(version):
		return pmLockEntry{}, false, bad("a package entry whose version nvx does not read")
	case tarball != "" || integrity != "":
		// A registry package, held to the registry's record of it like a
		// package-lock.json entry: by its hash, and by its URL when the
		// lockfile records one.
		entry.resolved, entry.integrity = tarball, integrity
	default:
		return pmLockEntry{}, false, bad("a package entry whose resolution nvx does not read")
	}
	return entry, false, nil
}

// pnpmReferences lists, as name and reference pairs, every dependency the
// importers and the package entries name. 9.x keeps the entries' dependencies
// under `snapshots`, and earlier versions on the `packages` entries.
func pnpmReferences(doc, importers *yamlNode, major int) [][2]string {
	var out [][2]string
	for _, path := range importers.keys {
		imp := importers.fields[path]
		for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies", "configDependencies", "packageManagerDependencies"} {
			deps := imp.get(field)
			if deps == nil || deps.kind != yamlMap {
				continue
			}
			for _, name := range deps.keys {
				d := deps.fields[name]
				ref := d.get("version").str()
				if ref == "" {
					ref = d.str() // 5.x: name: reference
				}
				out = append(out, [2]string{name, ref})
			}
		}
	}
	entries := doc.get("snapshots")
	if major < 9 {
		entries = doc.get("packages")
	}
	if entries == nil || entries.kind != yamlMap {
		return out
	}
	for _, key := range entries.keys {
		for _, field := range []string{"dependencies", "optionalDependencies"} {
			deps := entries.fields[key].get(field)
			if deps == nil || deps.kind != yamlMap {
				continue
			}
			for _, name := range deps.keys {
				out = append(out, [2]string{name, deps.fields[name].str()})
			}
		}
	}
	return out
}

// pnpmReferenceKey is the `packages` key a dependency reference names, and
// the spec to check when there is no such entry. check is false for a
// reference with no entry of its own: a link to a folder.
func pnpmReferenceKey(name, ref string, major int) (key, spec string, check bool) {
	if ref == "" || strings.HasPrefix(ref, "link:") {
		return "", "", false
	}
	if major == 5 {
		version, _, _ := strings.Cut(ref, "_")
		switch {
		case strings.HasPrefix(ref, "/"):
			key = pnpmKeyWithoutPeers(ref, 5)
			n, v, _ := splitPnpmKey(ref, 5)
			return key, n + "@" + v, true
		case isPlainVersion(version):
			return "/" + name + "/" + version, name + "@" + version, true
		}
		return ref, manifestDepSpec(name, ref), true
	}
	r := pnpmKeyWithoutPeers(ref, major)
	switch {
	case major == 6 && strings.HasPrefix(r, "/"):
		// An alias: the reference is the key, /left-pad@1.3.0.
		return r, strings.TrimPrefix(r, "/"), true
	case isPlainVersion(r):
		key = name + "@" + r
		if major == 6 {
			key = "/" + key
		}
		return key, name + "@" + r, true
	}
	if _, v, ok := splitLocator(r); ok && isPlainVersion(v) && major == 9 {
		return r, r, true // an alias in 9.x: left-pad@1.3.0
	}
	if major == 9 {
		return name + "@" + r, manifestDepSpec(name, r), true
	}
	return r, manifestDepSpec(name, r), true
}

// pnpmKeyWithoutPeers drops the suffix that names the peers a package was
// installed with: (react@18.3.1) from 6.x on, _react@18.3.1 in 5.x.
func pnpmKeyWithoutPeers(key string, major int) string {
	if major == 5 {
		if i := strings.LastIndex(key, "/"); i >= 0 {
			if j := strings.Index(key[i:], "_"); j >= 0 {
				return key[:i+j]
			}
		}
		return key
	}
	if i := strings.Index(key, "("); i > 0 {
		return key[:i]
	}
	return key
}

// splitPnpmKey reads the name and version out of a `packages` key.
func splitPnpmKey(key string, major int) (name, version string, ok bool) {
	key = strings.TrimPrefix(key, "/")
	if major == 5 {
		// name/1.0.0 or @scope/name/1.0.0, then an optional _peer suffix.
		parts := strings.SplitN(key, "/", 3)
		n := 1
		if strings.HasPrefix(key, "@") {
			n = 2
		}
		if len(parts) <= n {
			return "", "", false
		}
		name = strings.Join(parts[:n], "/")
		version = strings.Join(parts[n:], "/")
		version, _, _ = strings.Cut(version, "_")
		if !isPlainVersion(version) {
			return "", "", false
		}
		return name, version, true
	}
	// name@1.0.0(peer@2.0.0): the suffix names what peers it was built with.
	if i := strings.Index(key, "("); i > 0 {
		key = key[:i]
	}
	return splitLocator(key)
}
