package nvx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// devEngines.runtime is npm's, and nvx does not read it to pick a version.
//
// npm writes the field and enforces it. With onFail "error", its default, an
// `npm install` under another Node.js fails with EBADDEVENGINES. nvx says so
// where it matters. `nvx use` and `nvx install` with no argument already did
// (noteIgnoredVersionFiles) and were the only place. The shim, which is what runs
// all day, and the cd hook stayed silent while npm refused. Measured 2026-10-07
// with devEngines.runtime asking for 24.21.0 and the default at 22.
//
// Choosing a version from it is a separate decision. It would need a rank among
// .nvmrc, .node-version, engines and volta, a reading of onFail, and a position
// on the array form with several runtimes.

// devEnginesNode returns the Node.js entry of devEngines.runtime in the nearest
// package.json at or above dir that has one, and that file. "" when there is none.
func devEnginesNode(dir string) (version, source string) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", ""
	}
	for {
		pkg := filepath.Join(dir, "package.json")
		if content, err := os.ReadFile(pkg); err == nil {
			if v, ok := nodeInDevEngines(content); ok {
				return v, pkg
			}
		}
		if dir = nextVersionSearchDir(dir); dir == "" {
			return "", ""
		}
	}
}

// nodeInDevEngines reads devEngines.runtime from a package.json. The field is
// one object or an array of them, each naming a runtime. ok is false for any
// other shape, for a runtime that is not Node.js, and for onFail values under
// which npm does not fail.
func nodeInDevEngines(content []byte) (version string, ok bool) {
	var parsed struct {
		DevEngines struct {
			Runtime json.RawMessage `json:"runtime"`
		} `json:"devEngines"`
	}
	if json.Unmarshal(content, &parsed) != nil || len(parsed.DevEngines.Runtime) == 0 {
		return "", false
	}
	type entry struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		OnFail  string `json:"onFail"`
	}
	var entries []entry
	var one entry
	if json.Unmarshal(parsed.DevEngines.Runtime, &one) == nil {
		entries = []entry{one}
	} else if json.Unmarshal(parsed.DevEngines.Runtime, &entries) != nil {
		return "", false
	}
	for _, e := range entries {
		if !strings.EqualFold(e.Name, "node") || strings.TrimSpace(e.Version) == "" {
			continue
		}
		// "error" is npm's default. Under warn, ignore or download npm does not
		// refuse, and a warning from nvx would be noise.
		if e.OnFail != "" && !strings.EqualFold(e.OnFail, "error") {
			continue
		}
		return strings.TrimSpace(e.Version), true
	}
	return "", false
}

// devEnginesDisagreement reports a project that asks for a Node.js in
// devEngines.runtime, declares nothing nvx reads, and would run another version.
// running is the version a shimmed command would use here.
func devEnginesDisagreement(nvxHome string) (want, running string, ok bool) {
	// The cheap question first. Nearly no project has devEngines, and this runs on
	// every shimmed command and every cd, so a project without it pays for one walk
	// up the tree and nothing else.
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", false
	}
	want, _ = devEnginesNode(cwd)
	if want == "" {
		return "", "", false
	}
	rt := Providers["node"]
	pin := projectPinFor(rt)
	if pin.query != "" {
		return "", "", false // a file nvx reads decides, and its own warning covers a mismatch
	}
	if _, err := parseVersionRange(want); err != nil {
		return "", "", false // nvx cannot tell, so it does not say
	}
	running = sessionRuntimeVersion(nvxHome, rt, pin)
	if running == "" || versionSatisfies(nvxHome, running, want) {
		return "", "", false
	}
	return want, running, true
}

// warnIfDevEnginesDisagree is the shim's message. Only the outermost shimmed
// command in a process tree says it, as with the other shim notes, so a script
// that runs node a hundred times does not print it a hundred times.
func warnIfDevEnginesDisagree(nvxHome, cmdName string) {
	if runtimeForShim(cmdName).Name() != "node" {
		return
	}
	if want, running, ok := devEnginesDisagreement(nvxHome); ok {
		LogWarn("package.json asks for Node.js %s in devEngines.runtime, which nvx does not read, so this is running %s.", want, running)
		LogInfo("Put the version in .nvmrc or engines.node and nvx will use it. npm enforces devEngines whichever Node.js runs it.")
	}
}

// noteDevEnginesInAuto is the cd hook's. Nothing is switched, so it says why.
func noteDevEnginesInAuto(nvxHome string) {
	if want, _, ok := devEnginesDisagreement(nvxHome); ok {
		LogInfo("[nvx] package.json asks for Node.js %s in devEngines.runtime, which nvx does not read. Put the version in .nvmrc or engines.node for nvx to switch to it.", want)
	}
}
