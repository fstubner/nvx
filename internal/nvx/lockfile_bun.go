package nvx

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Reading bun.lock, the text lockfile Bun writes from 1.2 on: JSON with
// trailing commas. bun.lockb, the binary one before it, is not read.
//
// Each `packages` entry is an array: the package as name@version, the
// registry it came from ("" for the default), its metadata (dependencies, os,
// cpu), and its integrity hash. A workspace, folder, git or tarball entry
// carries that source in place of the version.

type bunLockFile struct {
	LockfileVersion *int                         `json:"lockfileVersion"`
	Workspaces      map[string]manifestDeps      `json:"workspaces"`
	Packages        map[string][]json.RawMessage `json:"packages"`
}

func parseBunLock(data []byte) (pmLockfile, error) {
	clean, err := withoutTrailingCommas(data)
	if err != nil {
		return pmLockfile{}, err
	}
	var lock bunLockFile
	if err := json.Unmarshal(clean, &lock); err != nil {
		return pmLockfile{}, err
	}
	if lock.LockfileVersion == nil {
		return pmLockfile{}, errors.New("it has no lockfileVersion")
	}
	if v := *lock.LockfileVersion; v < 0 || v > 2 {
		return pmLockfile{}, fmt.Errorf("its lockfileVersion %d is one this version of nvx does not read", v)
	}
	out := pmLockfile{}
	for path, ws := range lock.Workspaces {
		out.declared = append(out.declared, manifestSpecs(ws)...)
		if path == "" {
			root := ws
			out.rootRecords = func(name, spec string) bool {
				for _, deps := range root.depMaps() {
					if got, ok := deps[name]; ok && got == spec {
						return true
					}
				}
				return false
			}
		}
	}
	if out.rootRecords == nil {
		out.rootRecords = func(string, string) bool { return false }
	}
	// A key is where the package sits in node_modules: is-odd, or
	// is-even/is-odd for a copy nested under is-even. A dependency is found
	// as Node finds it: beside the package that needs it, or in a folder
	// above. A workspace's dependencies are looked for from the top.
	type need struct {
		from       []string
		name, spec string
	}
	var needs []need
	for _, ws := range lock.Workspaces {
		for _, deps := range []map[string]string{ws.Dependencies, ws.DevDependencies, ws.OptionalDependencies} {
			for name, spec := range deps {
				needs = append(needs, need{nil, name, spec})
			}
		}
	}
	for key, raw := range lock.Packages {
		e, skip, meta, err := bunEntry(raw)
		if err != nil {
			// The key is a dependency path, a package name, which is not a
			// secret, and it is what finds the entry.
			return pmLockfile{}, fmt.Errorf("the entry for %q: %v", key, err)
		}
		if !skip {
			out.entries = append(out.entries, e)
		}
		from := bunKeyPath(key)
		for name, spec := range meta.Dependencies {
			needs = append(needs, need{from, name, spec})
		}
		for name, spec := range meta.OptionalDependencies {
			needs = append(needs, need{from, name, spec})
		}
	}
	for _, n := range needs {
		if isLocalSpec(n.name + "@" + n.spec) {
			continue
		}
		found := false
		for i := len(n.from); i >= 0 && !found; i-- {
			found = lock.Packages[strings.Join(append(append([]string{}, n.from[:i]...), n.name), "/")] != nil
		}
		if !found {
			out.unrecorded = append(out.unrecorded, manifestDepSpec(n.name, n.spec))
		}
	}
	return out, nil
}

// bunKeyPath splits a `packages` key into the packages along its path, where
// a scoped name takes two segments: is-even/@scope/x is is-even, @scope/x.
func bunKeyPath(key string) []string {
	segs := strings.Split(key, "/")
	var out []string
	for i := 0; i < len(segs); i++ {
		if strings.HasPrefix(segs[i], "@") && i+1 < len(segs) {
			out = append(out, segs[i]+"/"+segs[i+1])
			i++
			continue
		}
		out = append(out, segs[i])
	}
	return out
}

// bunMeta is the part of an entry's metadata the checks read.
type bunMeta struct {
	OS                   stringList        `json:"os"`
	CPU                  stringList        `json:"cpu"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// bunEntry reads one `packages` entry, and its metadata for the dependencies
// it names. skip is a folder or workspace.
func bunEntry(raw []json.RawMessage) (e pmLockEntry, skip bool, meta bunMeta, err error) {
	var resolution string
	if len(raw) == 0 || json.Unmarshal(raw[0], &resolution) != nil {
		return e, false, meta, errors.New("it does not start with the package it installs")
	}
	name, ref, ok := splitLocator(resolution)
	if !ok {
		return e, false, meta, errors.New("nvx cannot read a package name from it")
	}
	// The metadata is the third element of a registry entry and the second
	// of most others: the first object in the array.
	for _, r := range raw[1:] {
		if t := strings.TrimSpace(string(r)); strings.HasPrefix(t, "{") {
			if json.Unmarshal(r, &meta) != nil {
				return e, false, meta, errors.New("its metadata does not parse")
			}
			break
		}
	}
	if local, skip, ok := localSourceEntry(name, ref); ok {
		return local, skip, meta, nil
	}
	if !isPlainVersion(ref) {
		if kind := nonRegistrySpecKind(ref); kind != "" {
			return pmLockEntry{name: name, sourceKind: kind}, false, meta, nil
		}
		if ref == "root:" {
			return e, true, meta, nil
		}
		return e, false, meta, errors.New("nvx does not read its source")
	}
	e = pmLockEntry{name: name, version: ref, os: meta.OS, cpu: meta.CPU}
	if len(raw) > 3 {
		if err := json.Unmarshal(raw[3], &e.integrity); err != nil {
			return pmLockEntry{}, false, meta, errors.New("its integrity hash does not parse")
		}
	}
	return e, false, meta, nil
}

// withoutTrailingCommas drops the commas before a closing } or ] that JSON
// does not allow and bun.lock has. A comment is an error: Bun writes none.
func withoutTrailingCommas(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '/':
			return nil, errors.New("it has a comment, which Bun does not write")
		case ',':
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
}
