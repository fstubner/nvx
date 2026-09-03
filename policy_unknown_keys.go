package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// A misspelt key in a policy file silently removes the protection it was meant
// to configure.
//
// `"blocked_packges": ["evil"]` parses, exits 0, and blocks nothing: encoding/json
// ignores fields it does not recognise, so the file reads as valid and the
// blocklist is empty. Every protection in this file is opt-in through a key
// name, so every one of them can be disabled by a typo that looks like it worked.
//
// Reported rather than refused. DisallowUnknownFields is the obvious tool and is
// the wrong one here: policy files in the wild carry keys this version does not
// know -- `prompts` sub-keys were removed for doing nothing and are deliberately
// still parsed, and a file written by a newer nvx has to stay readable by an
// older one. Refusing would turn a stray key into "nvx will not run", which for a
// tool that gates every install is a worse failure than the one being fixed.
//
// The suggestion uses the same edit distance the typosquat check uses, because
// the case this exists for is one wrong letter.

// policyKeyPaths returns every key path a Policy can legitimately contain, as
// dotted paths ("isolation.network.mode"). Derived from the struct tags so it
// cannot drift from the type.
// The second return is the paths whose CHILDREN are data rather than settings --
// map-typed fields, where the user chooses the key names.
//
// Without it, `runtime.versions` (a map[string]string) had its contents walked as
// though they were setting names, so the README's own example --
//
//	{ "runtime": { "default": "node", "versions": { "node": "20" } } }
//
// -- reported `runtime.versions.node` as "not an nvx policy setting and is being
// ignored", twice per command, and suggested `isolation.filesystem.mode`. The
// setting was honoured the whole time. This is the warning whose entire purpose
// is that a misspelt key silently disables a protection, so firing it falsely on
// a config copied out of the project's own documentation is how a reader learns
// to skip the ones that are real.
func policyKeyPaths() (known, openToUserKeys map[string]bool) {
	known = map[string]bool{}
	openToUserKeys = map[string]bool{}
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name == "" {
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			known[path] = true

			ft := f.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Map {
				openToUserKeys[path] = true
				continue
			}
			walk(f.Type, path)
		}
	}
	walk(reflect.TypeOf(Policy{}), "")
	return known, openToUserKeys
}

// unknownPolicyKeys returns the key paths in data that Policy has no field for,
// in a stable order.
func unknownPolicyKeys(data []byte) []string {
	known, openToUserKeys := policyKeyPaths()

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil // not an object; the real parse reports the error
	}

	var unknown []string
	var walk func(obj map[string]json.RawMessage, prefix string)
	walk = func(obj map[string]json.RawMessage, prefix string) {
		for key, raw := range obj {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if !known[path] {
				unknown = append(unknown, path)
				continue // do not descend into something already unrecognised
			}
			if openToUserKeys[path] {
				// A map: the keys below are the user's own names for things, not
				// setting names, so nothing under here can be "unrecognised".
				continue
			}
			var child map[string]json.RawMessage
			if json.Unmarshal(raw, &child) == nil {
				walk(child, path)
			}
		}
	}
	walk(root, "")
	sort.Strings(unknown)
	return unknown
}

// nearestPolicyKey returns the known key path closest to unknown, or "" if
// nothing is close enough to be worth suggesting.
//
// Compared on the LAST segment: a typo is in the key the author wrote, and
// comparing whole dotted paths makes "isolation.network.mdoe" look far from
// everything because the prefix dominates.
func nearestPolicyKey(unknown string, known map[string]bool) string {
	split := func(p string) (parent, leaf string) {
		if i := strings.LastIndex(p, "."); i >= 0 {
			return p[:i], p[i+1:]
		}
		return "", p
	}
	wantParent, target := split(unknown)

	// Candidates are ranked by edit distance on the leaf, then by whether they sit
	// in the same object.
	//
	// The parent tiebreak is not cosmetic. "mode" appears under both
	// isolation.network and isolation.filesystem, and "enabled" appears under
	// four different objects, so leaf distance alone picked whichever the map
	// happened to yield -- suggesting isolation.filesystem.mode for a typo in
	// isolation.network.mode, which sends the reader to the wrong setting. Sorted
	// before comparing so the answer does not depend on map order either.
	best, bestDist, bestSameParent := "", 0, false
	paths := make([]string, 0, len(known))
	for path := range known {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		parent, leaf := split(path)
		d := LevenshteinDistance(target, leaf)
		// Never suggest something further away than a typo plausibly is, and never
		// suggest a key shorter than the distance itself (every short word is
		// "close" to every other).
		if d == 0 || d > 2 || d >= len(target) {
			continue
		}
		sameParent := parent == wantParent
		switch {
		case best == "":
		case sameParent && !bestSameParent:
		case sameParent == bestSameParent && d < bestDist:
		default:
			continue
		}
		best, bestDist, bestSameParent = path, d, sameParent
	}
	return best
}

// warnedPolicyKeys remembers what this process has already said, so one
// misspelt setting is reported once rather than once per policy load.
//
// LoadPolicy runs several times in a single command -- the shim decides what to
// do with it, then the sandbox loads it again -- and every load warned. One
// stray key produced the same two lines two or three times over, which an
// acceptance pass called out as noise. A repeated warning is not a louder
// warning; it is one people learn to scroll past.
var (
	warnedPolicyKeysMu sync.Mutex
	warnedPolicyKeys   = map[string]bool{}
)

// warnAboutUnknownPolicyKeys reports keys nvx does not recognise in a policy
// file, naming the nearest real key when one is close. Each (file, key) is
// reported at most once per process.
func warnAboutUnknownPolicyKeys(path string, data []byte) {
	unknown := unknownPolicyKeys(data)
	if len(unknown) == 0 {
		return
	}
	known, _ := policyKeyPaths()

	warnedPolicyKeysMu.Lock()
	var fresh []string
	for _, key := range unknown {
		id := path + "\x00" + key
		if warnedPolicyKeys[id] {
			continue
		}
		warnedPolicyKeys[id] = true
		fresh = append(fresh, key)
	}
	warnedPolicyKeysMu.Unlock()

	if len(fresh) == 0 {
		return
	}
	for _, key := range fresh {
		if near := nearestPolicyKey(key, known); near != "" {
			LogWarn("%s: %q is not an nvx policy setting and is being ignored. Did you mean %q?", path, key, near)
			continue
		}
		LogWarn("%s: %q is not an nvx policy setting and is being ignored.", path, key)
	}
	LogInfo("A misspelt setting reads as valid and does nothing, so the protection it was meant to configure is absent.")
}
