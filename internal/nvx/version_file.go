package nvx

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// projectVersion is a runtime version the working directory declares, as a spec
// that install and use accept ("lts/*", or "bun@1.2" for a runtime other than
// Node), and the file it came from.
type projectVersion struct {
	spec   string
	source string
}

// projectVersionSpecs lists what the directory declares, read the way `nvx auto`
// reads it: every runtime's own detection, Node first. `nvx install` and `nvx
// use` with no argument ignored a .nvmrc although the docs say install accepts
// whatever one says.
func projectVersionSpecs(cwd string) []projectVersion {
	var out []projectVersion
	for _, name := range orderedRuntimeNames() {
		query, source, err := Providers[name].DetectConfig(cwd)
		if err != nil || query == "" {
			continue
		}
		spec := query
		if name != "node" {
			spec = name + "@" + query
		}
		out = append(out, projectVersion{spec: spec, source: source})
	}
	return out
}

// noteIgnoredVersionFiles says so when a file that looks like a version
// declaration is there and nvx does not read it. Silence read as "nvx looked
// and found nothing", which is the wrong conclusion for a project that does
// declare a version, only in a format nvx has no reader for.
//
// Looks where DetectVersionConfig looks: the directory and each one above it.
func noteIgnoredVersionFiles(cwd string) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	for {
		toolVersions := filepath.Join(dir, ".tool-versions")
		if info, err := os.Stat(toolVersions); err == nil && !info.IsDir() {
			LogInfo("Found %s, which nvx does not read. nvx reads .nvmrc, .node-version, .bun-version and package.json engines.", toolVersions)
		}
		pkg := filepath.Join(dir, "package.json")
		if content, err := os.ReadFile(pkg); err == nil {
			var parsed struct {
				DevEngines struct {
					Runtime json.RawMessage `json:"runtime"`
				} `json:"devEngines"`
			}
			if json.Unmarshal(content, &parsed) == nil && len(parsed.DevEngines.Runtime) > 0 {
				LogInfo("Found devEngines.runtime in %s, which nvx does not read. nvx reads .nvmrc, .node-version, .bun-version and package.json engines.", pkg)
			}
		}
		if dir = nextVersionSearchDir(dir); dir == "" {
			return
		}
	}
}

// nextVersionSearchDir is the directory to look in after dir when searching
// upward for a version file, or "" when the search ends: at the filesystem root
// or after the user's home directory. A file above home belongs to no project of
// this user's, and the shim does this search on every run.
func nextVersionSearchDir(dir string) string {
	parent := filepath.Dir(dir)
	if parent == dir {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && rel == "." {
			return ""
		}
	}
	return parent
}
