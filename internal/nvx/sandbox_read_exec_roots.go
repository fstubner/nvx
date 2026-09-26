package nvx

import (
	"os"
	"path/filepath"
	"strings"
)

// Extra directories a contained process may read and execute from.
//
// Some tools keep the program they run somewhere nvx grants nothing. Playwright
// is the case that forced this: its browsers live in
// %LOCALAPPDATA%\ms-playwright (~/.cache/ms-playwright elsewhere), and a
// contained process could not list that directory at all -- measured on Windows
// 2026-08-28, EPERM inside against 27 entries outside. The MCP containment
// design had assumed the blocker for browser-driving servers was Windows
// refusing connections INTO an AppContainer. That is a real constraint, but it
// applies to reaching a browser that is already running on the host; when
// Playwright launches its own, the browser is a child inside the container and
// speaks to it over intra-container loopback, which nvx already proves works.
// The binary simply was not reachable.
//
// Read and execute only. These paths are never added to the writable roots, on
// any platform, whatever else a policy says -- a directory you run a browser
// from is not one a package install should be able to rewrite.

// resolveReadExecRoots turns policy entries into absolute paths that exist.
//
// Entries are expanded so a policy can be written portably: environment
// variables in either $VAR or %VAR% form, and a leading ~ for the real home.
// Anything that does not resolve to an existing directory is dropped with a
// warning rather than failing the launch -- a stale entry for a tool that is not
// installed here should not stop the command running.
//
// An entry is also dropped when it names a variable that is not set, or
// resolves to a volume root, the home directory, or a directory holding
// nvxHome. `%UNSET%\` expanded to `\`, which is C:\, and `$UNSET/` to `/`, so
// a variable missing on one machine granted read and execute on the whole
// disk. `~` alone granted the whole home, which holds every credential store.
func resolveReadExecRoots(entries []string, nvxHome string) []string {
	var out []string
	seen := map[string]bool{}
	home, _ := os.UserHomeDir()
	for _, raw := range entries {
		p, unset := expandPathVars(strings.TrimSpace(raw))
		if unset != "" {
			LogWarn("Ignoring allow_read_exec entry %q: %s is not set here.", raw, unset)
			continue
		}
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			LogWarn("Ignoring allow_read_exec entry %q: %v.", raw, err)
			continue
		}
		if tooBroadForReadExec(abs, home, nvxHome) {
			LogWarn("Ignoring allow_read_exec entry %q: %s is a drive root, the home directory, or holds the nvx home. Name the tool's own directory instead.", raw, abs)
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			LogWarn("Ignoring allow_read_exec entry %q: %s does not exist here.", raw, abs)
			continue
		}
		if !info.IsDir() {
			// A single file would work on Windows and not under Landlock's
			// directory-shaped rules. Refusing both keeps the policy meaning the
			// same everywhere rather than quietly differing by platform.
			LogWarn("Ignoring allow_read_exec entry %q: it must be a directory.", raw)
			continue
		}
		key := strings.ToLower(filepath.Clean(abs))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, abs)
	}
	return out
}

// tooBroadForReadExec reports whether dir is a volume root, the home directory,
// or an ancestor of nvxHome. Each would hand contained code far more than one
// tool's directory.
func tooBroadForReadExec(dir, home, nvxHome string) bool {
	clean := filepath.Clean(dir)
	if filepath.Dir(clean) == clean {
		return true // a volume or filesystem root
	}
	if home != "" && dirWithin(filepath.Clean(home), clean) {
		return true
	}
	return nvxHome != "" && dirWithin(filepath.Clean(nvxHome), clean)
}

// expandPathVars resolves ~, $VAR/${VAR} and %VAR% in a policy path. The
// second return names the first variable that is unset or empty, and is ""
// when every one resolved.
//
// Both spellings, because this policy file is shared across platforms and a
// developer writing it on Windows reaches for %LOCALAPPDATA% while the same
// project on Linux wants $HOME. Supporting one would make the field usable on
// one platform per project.
func expandPathVars(p string) (string, string) {
	if p == "" {
		return "", ""
	}
	unset := ""
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "the home directory"
		}
		p = home + p[1:]
	}
	// %VAR% first: os.ExpandEnv does not understand it.
	//
	// Scanning resumes after each substitution rather than restarting, so a
	// variable whose value contains its own name expands once instead of for ever.
	// It used to restart from the beginning: with SELFREF=%SELFREF% this spun
	// without bound and nvx never launched anything -- no error, no output, just a
	// process to kill. Found by an acceptance pass on 2026-08-28.
	//
	// The bound is a second guard, not the fix. One pass cannot loop, but a chain
	// of variables expanding into each other still grows the string, and a policy
	// file is not worth an unbounded allocation.
	const maxExpansions = 32
	from := 0
	for n := 0; n < maxExpansions; n++ {
		start := strings.Index(p[from:], "%")
		if start < 0 {
			break
		}
		start += from
		end := strings.Index(p[start+1:], "%")
		if end < 0 {
			break
		}
		name := p[start+1 : start+1+end]
		value := os.Getenv(name)
		if value == "" && unset == "" {
			unset = "%" + name + "%"
		}
		p = p[:start] + value + p[start+1+end+1:]
		from = start + len(value)
	}
	p = os.Expand(p, func(name string) string {
		value := os.Getenv(name)
		if value == "" && unset == "" {
			unset = "$" + name
		}
		return value
	})
	return p, unset
}
