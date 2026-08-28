package main

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
func resolveReadExecRoots(entries []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range entries {
		p := expandPathVars(strings.TrimSpace(raw))
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			LogWarn("Ignoring allow_read_exec entry %q: %v.", raw, err)
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

// expandPathVars resolves ~, $VAR/${VAR} and %VAR% in a policy path.
//
// Both spellings, because this policy file is shared across platforms and a
// developer writing it on Windows reaches for %LOCALAPPDATA% while the same
// project on Linux wants $HOME. Supporting one would make the field usable on
// one platform per project.
func expandPathVars(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + p[1:]
		}
	}
	// %VAR% first: os.ExpandEnv does not understand it.
	for {
		start := strings.Index(p, "%")
		if start < 0 {
			break
		}
		end := strings.Index(p[start+1:], "%")
		if end < 0 {
			break
		}
		name := p[start+1 : start+1+end]
		p = p[:start] + os.Getenv(name) + p[start+1+end+1:]
	}
	return os.ExpandEnv(p)
}
