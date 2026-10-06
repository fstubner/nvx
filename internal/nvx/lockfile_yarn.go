package nvx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Reading yarn.lock: Yarn 1's own format, and the YAML that Yarn 2 and later
// write, which starts with a __metadata entry.
//
// Yarn 1 keys each entry by the specs that resolved to it, "is-glob@^4.0.1,
// is-glob@~4.0.1", and records the version, the tarball URL and its hash. It
// records no os or cpu, so every entry is checked on every platform.
//
// Yarn 2 and later key entries the same way and record a resolution,
// "is-glob@npm:4.0.3", which names the registry package Yarn fetches. Its
// checksum is of the archive Yarn stores, not of the registry's tarball, so it
// cannot be compared with the registry's hash. A registry package is fetched
// by name and version from the configured registry, unless the resolution
// carries an __archiveUrl, which is held to the registry's tarball URL.

func parseYarnLock(data []byte) (pmLockfile, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, line := range strings.Split(text, "\n") {
		if line == "__metadata:" {
			return parseYarnBerryLock(data)
		}
	}
	return parseYarnClassicLock(text)
}

type yarnClassicEntry struct {
	line        int
	descriptors []string
	fields      map[string]string
	deps        [][2]string // name and range, from dependencies
}

func parseYarnClassicLock(text string) (pmLockfile, error) {
	var entries []*yarnClassicEntry
	var cur *yarnClassicEntry
	section := ""
	for i, line := range strings.Split(text, "\n") {
		num := i + 1
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "\t") {
			return pmLockfile{}, fmt.Errorf("line %d: tab indentation", num)
		}
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		switch indent := len(line) - len(trimmed); indent {
		case 0:
			if !strings.HasSuffix(line, ":") {
				return pmLockfile{}, fmt.Errorf("line %d: a line that is not an entry", num)
			}
			descs, err := splitYarnDescriptors(line[:len(line)-1])
			if err != nil {
				return pmLockfile{}, fmt.Errorf("line %d: %v", num, err)
			}
			cur = &yarnClassicEntry{line: num, descriptors: descs, fields: map[string]string{}}
			entries = append(entries, cur)
			section = ""
		case 2:
			if cur == nil {
				return pmLockfile{}, fmt.Errorf("line %d: a field outside an entry", num)
			}
			if strings.HasSuffix(trimmed, ":") {
				section = trimmed[:len(trimmed)-1]
				continue
			}
			section = ""
			key, value, err := yarnClassicField(trimmed)
			if err != nil {
				return pmLockfile{}, fmt.Errorf("line %d: %v", num, err)
			}
			if _, dup := cur.fields[key]; dup {
				return pmLockfile{}, fmt.Errorf("line %d: a field that appears twice in one entry", num)
			}
			cur.fields[key] = value
		case 4:
			// A dependency list. The checks need what was resolved, which is
			// each dependency's own entry.
			if cur == nil || section == "" {
				return pmLockfile{}, fmt.Errorf("line %d: unexpected indentation", num)
			}
			name, rng, err := yarnClassicField(trimmed)
			if err != nil {
				return pmLockfile{}, fmt.Errorf("line %d: %v", num, err)
			}
			// Yarn 1 leaves out an optional dependency for another platform:
			// a lockfile written on Windows has no fsevents entry. So only a
			// missing required dependency is one Yarn resolves afresh here.
			if section == "dependencies" {
				cur.deps = append(cur.deps, [2]string{name, rng})
			}
		default:
			return pmLockfile{}, fmt.Errorf("line %d: unexpected indentation", num)
		}
	}

	recorded := map[string]bool{}
	out := pmLockfile{membersUnrecorded: true}
	for _, ce := range entries {
		e, skip, err := yarnClassicEntryTarget(ce)
		if err != nil {
			return pmLockfile{}, fmt.Errorf("line %d: %v", ce.line, err)
		}
		for _, d := range ce.descriptors {
			recorded[d] = true
		}
		if !skip {
			out.entries = append(out.entries, e)
		}
	}
	for _, ce := range entries {
		for _, d := range ce.deps {
			if !recorded[d[0]+"@"+d[1]] {
				out.unrecorded = append(out.unrecorded, manifestDepSpec(d[0], d[1]))
			}
		}
	}
	out.rootRecords = func(name, spec string) bool { return recorded[name+"@"+spec] }
	return out, nil
}

func yarnClassicEntryTarget(ce *yarnClassicEntry) (pmLockEntry, bool, error) {
	version := ce.fields["version"]
	if version == "" {
		return pmLockEntry{}, false, errors.New("an entry with no version")
	}
	name, kind := "", ""
	for _, d := range ce.descriptors {
		n, ref, ok := splitLocator(d)
		if !ok {
			return pmLockEntry{}, false, errors.New("an entry nvx cannot read a package name from")
		}
		// lp@npm:left-pad@1.3.0 installs left-pad.
		if strings.HasPrefix(ref, "npm:") {
			if real, realRef, ok := splitLocator(ref[len("npm:"):]); ok {
				n, ref = real, realRef
			} else {
				ref = ref[len("npm:"):]
			}
		}
		if name != "" && n != name {
			return pmLockEntry{}, false, errors.New("an entry whose specs name different packages")
		}
		name = n
		if k := yarnRangeKind(ref); k != "" {
			if e, skip, ok := localSourceEntry(name, ref); ok {
				return e, skip, nil
			}
			kind = k
		}
	}
	if kind != "" {
		return pmLockEntry{name: name, version: version, sourceKind: kind}, false, nil
	}
	if !isPlainVersion(version) {
		return pmLockEntry{}, false, errors.New("an entry whose version nvx does not read")
	}
	// A spec for a registry version: Yarn fetches the resolved URL and checks
	// the integrity hash, so both are held to the registry's record.
	return pmLockEntry{name: name, version: version, resolved: ce.fields["resolved"], integrity: ce.fields["integrity"]}, false, nil
}

// yarnRangeKind is nonRegistrySpecKind for the range in a Yarn 1 key, where
// "~3.1.2" is a version range. Only "~/" starts a path in a spec.
func yarnRangeKind(ref string) string {
	if strings.HasPrefix(ref, "~") && !strings.HasPrefix(ref, "~/") && !strings.HasPrefix(ref, `~\`) {
		return ""
	}
	return nonRegistrySpecKind(ref)
}

// splitYarnDescriptors splits an entry's key, `a@^1, "b@^2"`, into its specs.
func splitYarnDescriptors(s string) ([]string, error) {
	var out []string
	for s != "" {
		var d string
		if s[0] == '"' {
			v, n, err := yarnQuoted(s)
			if err != nil {
				return nil, err
			}
			d, s = v, s[n:]
		} else {
			i := strings.Index(s, ",")
			if i < 0 {
				i = len(s)
			}
			d, s = strings.TrimSpace(s[:i]), s[i:]
		}
		if d == "" {
			return nil, errors.New("an empty spec in an entry's key")
		}
		out = append(out, d)
		s = strings.TrimLeft(s, " ")
		if s == "" {
			break
		}
		if s[0] != ',' {
			return nil, errors.New("an entry key nvx does not read")
		}
		s = strings.TrimLeft(s[1:], " ")
	}
	if len(out) == 0 {
		return nil, errors.New("an entry with no spec")
	}
	return out, nil
}

// yarnClassicField reads `key value`, either of them quoted.
func yarnClassicField(s string) (key, value string, err error) {
	key, rest, err := yarnToken(s)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(rest, " ") {
		return "", "", errors.New("a field with no value")
	}
	value, rest, err = yarnToken(strings.TrimLeft(rest, " "))
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(rest) != "" {
		return "", "", errors.New("text after a field's value")
	}
	return key, value, nil
}

func yarnToken(s string) (tok, rest string, err error) {
	if s == "" {
		return "", "", errors.New("a missing value")
	}
	if s[0] == '"' {
		v, n, err := yarnQuoted(s)
		return v, s[n:], err
	}
	i := strings.Index(s, " ")
	if i < 0 {
		return s, "", nil
	}
	return s[:i], s[i:], nil
}

// yarnQuoted reads a JSON string at the start of s, which is how Yarn 1
// quotes.
func yarnQuoted(s string) (string, int, error) {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			var v string
			if err := json.Unmarshal([]byte(s[:i+1]), &v); err != nil {
				return "", 0, errors.New("a quoted string nvx does not read")
			}
			return v, i + 1, nil
		}
	}
	return "", 0, errors.New("a quoted string that does not end on its line")
}

func parseYarnBerryLock(data []byte) (pmLockfile, error) {
	docs, err := parseYAMLDocuments(data)
	if err != nil {
		return pmLockfile{}, err
	}
	if len(docs) != 1 || docs[0].kind != yamlMap {
		return pmLockfile{}, errors.New("it is not one mapping")
	}
	doc := docs[0]
	if doc.get("__metadata").get("version").str() == "" {
		return pmLockfile{}, errors.New("its __metadata has no version")
	}
	out := pmLockfile{}
	root := map[string]string{}
	// Each key lists the specs that resolved to its entry. A local one ends
	// in ::locator=..., which a dependency's spec does not carry.
	recorded := map[string]bool{}
	for _, key := range doc.keys {
		for _, d := range strings.Split(key, ", ") {
			d, _, _ = strings.Cut(d, "::")
			recorded[d] = true
		}
	}
	for _, key := range doc.keys {
		if key == "__metadata" {
			continue
		}
		e := doc.fields[key]
		if e.kind != yamlMap {
			return pmLockfile{}, fmt.Errorf("line %d: an entry that is not a mapping", e.line)
		}
		name, ref, ok := splitLocator(e.get("resolution").str())
		if !ok {
			return pmLockfile{}, fmt.Errorf("line %d: an entry with no resolution nvx reads", e.line)
		}
		for _, field := range []string{"dependencies", "optionalDependencies"} {
			deps := e.get(field)
			if deps == nil || deps.kind != yamlMap {
				continue
			}
			for _, dep := range deps.keys {
				spec := deps.fields[dep].str()
				if strings.HasPrefix(spec, "npm:") && !recorded[dep+"@"+spec] {
					out.unrecorded = append(out.unrecorded, manifestDepSpec(dep, berryDeclared(spec)))
				}
			}
		}
		if strings.HasPrefix(ref, "workspace:") {
			// A workspace's entry records what its package.json declares.
			for _, field := range []string{"dependencies", "peerDependencies"} {
				for dep, spec := range e.get(field).stringMap() {
					out.declared = append(out.declared, manifestDepSpec(dep, berryDeclared(spec)))
					if ref == "workspace:." {
						root[dep] = spec
					}
				}
			}
			continue
		}
		entry, skip, err := berryEntry(name, ref, 0)
		if err != nil {
			return pmLockfile{}, fmt.Errorf("line %d: %v", e.line, err)
		}
		if skip {
			continue
		}
		entry.os, entry.cpu = berryConditions(e.get("conditions").str())
		out.entries = append(out.entries, entry)
	}
	out.rootRecords = func(name, spec string) bool {
		got, ok := root[name]
		return ok && (got == spec || got == "npm:"+spec)
	}
	return out, nil
}

// berryDeclared is a dependency's spec as package.json wrote it. Yarn writes
// a registry range as npm:^1.0.0, and an alias, npm:left-pad@1.3.0, keeps its
// prefix, which is what makes it one.
func berryDeclared(spec string) string {
	if rest, ok := strings.CutPrefix(spec, "npm:"); ok {
		if _, _, alias := splitLocator(rest); !alias {
			return rest
		}
	}
	return spec
}

// berryEntry reads a Yarn 2+ resolution's reference.
func berryEntry(name, ref string, depth int) (pmLockEntry, bool, error) {
	switch {
	case strings.HasPrefix(ref, "npm:"):
		version, params, _ := strings.Cut(ref[len("npm:"):], "::")
		if !isPlainVersion(version) {
			return pmLockEntry{}, false, errors.New("a registry entry whose version nvx does not read")
		}
		e := pmLockEntry{name: name, version: version}
		if params != "" {
			q, err := url.ParseQuery(params)
			if err != nil {
				return pmLockEntry{}, false, errors.New("a registry entry whose parameters nvx does not read")
			}
			// Where Yarn fetches a tarball that is not at the registry's usual
			// path. It is held to the registry's tarball URL like a
			// package-lock.json entry's.
			e.resolved = q.Get("__archiveUrl")
		}
		return e, false, nil
	case strings.HasPrefix(ref, "patch:"):
		// patch:<the original, URL-encoded>#<patch>. The original is fetched
		// and then patched, so the original is what is checked.
		inner, _, _ := strings.Cut(ref[len("patch:"):], "#")
		decoded, err := url.PathUnescape(inner)
		if err != nil || depth > 2 {
			return pmLockEntry{}, false, errors.New("a patch entry nvx does not read")
		}
		innerName, innerRef, ok := splitLocator(decoded)
		if !ok {
			return pmLockEntry{}, false, errors.New("a patch entry nvx does not read")
		}
		return berryEntry(innerName, innerRef, depth+1)
	case strings.HasPrefix(ref, "exec:"):
		return pmLockEntry{name: name, sourceKind: "an exec: spec"}, false, nil
	}
	if e, skip, ok := localSourceEntry(name, ref); ok {
		return e, skip, nil
	}
	if kind := nonRegistrySpecKind(ref); kind != "" {
		return pmLockEntry{name: name, sourceKind: kind}, false, nil
	}
	return pmLockEntry{}, false, errors.New("an entry whose resolution nvx does not read")
}

// berryConditions reads "os=darwin & cpu=arm64" into os and cpu lists. A
// condition it cannot read leaves the entry checked on every platform.
func berryConditions(s string) (osList, cpuList []string) {
	if s == "" {
		return nil, nil
	}
	for _, term := range strings.Split(s, " & ") {
		k, v, ok := strings.Cut(strings.TrimSpace(term), "=")
		if !ok || v == "" || strings.ContainsAny(v, "!|()& =") {
			return nil, nil
		}
		switch k {
		case "os":
			osList = append(osList, v)
		case "cpu":
			cpuList = append(cpuList, v)
		case "libc":
		default:
			return nil, nil
		}
	}
	return osList, cpuList
}
