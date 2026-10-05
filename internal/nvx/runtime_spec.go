package nvx

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// parseRuntimeSpec interprets an install/use/default argument that may name a
// runtime. Forms:
//
//	"20"        -> node, "20"        (bare version defaults to node, nvm-compatible)
//	"lts"       -> node, "lts"
//	"bun@1.2"   -> bun,  "1.2"
//	"bun"       -> bun,  "latest"    (bare runtime name means its latest)
//	"node@lts"  -> node, "lts"
//
// A token before "@" is treated as a runtime only when it is a registered
// provider; otherwise the whole argument is a node version query.
func parseRuntimeSpec(arg string) (RuntimeProvider, string) {
	arg = strings.TrimSpace(arg)
	if i := strings.Index(arg, "@"); i > 0 {
		name := strings.ToLower(arg[:i])
		if p, ok := Providers[name]; ok {
			// A trailing "@" with nothing after it yields an empty version, which
			// callers refuse. It used to mean "latest", so `nvx install node@`
			// silently downloaded whatever the newest release happened to be -- a
			// major nobody asked for. Someone typing a bare "node@" has far more
			// likely lost the version off the end of the line than chosen to say
			// "latest" the long way round, and `nvx install node` already means
			// latest and reads like it.
			return p, strings.TrimSpace(arg[i+1:])
		}
		return Providers["node"], arg
	}
	if p, ok := Providers[strings.ToLower(arg)]; ok {
		return p, "latest"
	}
	return Providers["node"], arg
}

// orderedRuntimeNames lists registered runtimes with node first, then the rest
// alphabetically, for deterministic output.
func orderedRuntimeNames() []string {
	var rest []string
	for name := range Providers {
		if name != "node" {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	out := make([]string, 0, len(rest)+1)
	if _, ok := Providers["node"]; ok {
		out = append(out, "node")
	}
	return append(out, rest...)
}

func runtimeDisplayName(name string) string {
	switch name {
	case "node":
		return "Node.js"
	case "":
		return "runtime"
	default:
		return strings.ToUpper(name[:1]) + name[1:]
	}
}

// runtimeCurrentLinkPath returns the global-default link for a runtime. Node
// keeps the legacy ~/.nvx/current link (the installer PATH depends on it);
// other runtimes use ~/.nvx/current-<runtime>.
func runtimeCurrentLinkPath(nvxHome, runtimeName string) string {
	if runtimeName == "" || runtimeName == "node" {
		return currentLinkPath(nvxHome)
	}
	return filepath.Join(nvxHome, "current-"+runtimeName)
}

// getActiveShellVersionFor returns the version of runtimeName currently on PATH
// for this shell, or "" if none. It is scoped to a single runtime so multiple
// runtimes can be active at once.
func getActiveShellVersionFor(nvxHome, runtimeName string) string {
	runtimeDir := filepath.Clean(filepath.Join(nvxHome, "versions", runtimeName))
	for _, part := range filepath.SplitList(os.Getenv("PATH")) {
		if part == "" {
			continue
		}
		normPart := filepath.Clean(part)
		if !strings.HasPrefix(strings.ToLower(normPart), strings.ToLower(runtimeDir)+string(os.PathSeparator)) {
			continue
		}
		rel, err := filepath.Rel(runtimeDir, normPart)
		if err != nil {
			continue
		}
		for _, sub := range strings.Split(rel, string(os.PathSeparator)) {
			if strings.HasPrefix(sub, "v") {
				return sub
			}
		}
	}
	return ""
}

func getGlobalDefaultVersionFor(nvxHome, runtimeName string) string {
	target, err := os.Readlink(runtimeCurrentLinkPath(nvxHome, runtimeName))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// runtimeFromVersionDir returns the runtime segment of a versions/<runtime>/<ver>
// path when it names a registered runtime, else "" (legacy flat layout or
// unknown), in which case callers fall back to node-compatible behavior.
func runtimeFromVersionDir(nvxHome, versionDir string) string {
	versionsDir := filepath.Join(nvxHome, "versions")
	rel, err := filepath.Rel(versionsDir, versionDir)
	if err != nil {
		return ""
	}
	seg := rel
	if i := strings.IndexAny(rel, `/\`); i >= 0 {
		seg = rel[:i]
	}
	seg = strings.ToLower(seg)
	if _, ok := Providers[seg]; ok {
		return seg
	}
	return ""
}

// projectPin is the version a project declares for one runtime and the file
// that declares it. Both are empty when nothing does.
type projectPin struct {
	query  string
	source string
}

// projectPinFor reads the version file that applies to rt in the working
// directory, found the way `nvx auto` finds it.
func projectPinFor(rt RuntimeProvider) projectPin {
	cwd, err := os.Getwd()
	if err != nil {
		return projectPin{}
	}
	query, source, err := rt.DetectConfig(cwd)
	if err != nil || query == "" {
		return projectPin{}
	}
	return projectPin{query: query, source: source}
}

// sessionRuntimeVersion is the installed version of rt that a shimmed command
// runs when the policy pins none. In order: the version this shell has active,
// the version the project pins when it is installed, then the global default.
//
// The shell comes first because `nvx use` and the shell integration both work
// by putting a version on PATH, and in that shell a bare `node` runs it without
// passing through the shim at all. The project's file comes next so a shim
// started where no integration runs (an IDE task, a git hook, cron, CI) gets
// the version the project asks for. Measured 2026-10-01 before this existed:
// with .nvmrc at 20.11 and the default at 22, `node --version` through the shim
// printed v22.23.3. A pin that is not installed falls through to the default,
// and warnIfProjectPinsAnotherVersion says how to install it. The shim never
// installs anything itself.
func sessionRuntimeVersion(nvxHome string, rt RuntimeProvider, pin projectPin) string {
	if v := getActiveShellVersionFor(nvxHome, rt.Name()); v != "" {
		return v
	}
	if pin.query != "" {
		if v, err := resolveLocalVersion(rt, pin.query, nvxHome); err == nil {
			return v
		}
	}
	return getGlobalDefaultVersionFor(nvxHome, rt.Name())
}
