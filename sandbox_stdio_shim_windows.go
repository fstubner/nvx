//go:build windows

package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

// stdioShimJS is preloaded into every contained node process so that synchronous
// output capture works despite an AppContainer being unable to create named pipes.
// See the file itself for the mechanism and for what it deliberately does not fix.
//
//go:embed sandbox_stdio_shim.js
var stdioShimJS string

const stdioShimName = "nvx-stdio-shim.js"

// writeStdioShim drops the preload into the guest home and returns its path.
//
// The guest home rather than nvxHome, because the sandbox must be able to READ it
// and ~/.nvx is now traverse-only. That the sandbox can also WRITE there is not a
// weakness: the shim runs inside the sandbox with the sandbox's own privileges, so
// a package that rewrote it could only change what its own children do, which it
// could do anyway by not calling them.
func writeStdioShim(guestHome string) (string, error) {
	p := filepath.Join(guestHome, stdioShimName)
	if err := os.WriteFile(p, []byte(stdioShimJS), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// addNodeOptionsRequire appends `--require <path>` to NODE_OPTIONS, quoting the
// path because a profile directory can contain spaces.
func addNodeOptionsRequire(env []string, shimPath string) []string {
	if shimPath == "" {
		return env
	}
	// Node accepts forward slashes on Windows and they avoid backslash-escaping
	// questions inside the quoted NODE_OPTIONS value.
	arg := `--require "` + strings.ReplaceAll(shimPath, `\`, `/`) + `"`
	out := make([]string, 0, len(env)+1)
	found := false
	for _, e := range env {
		key, value, ok := strings.Cut(e, "=")
		if ok && strings.EqualFold(key, "NODE_OPTIONS") {
			found = true
			if strings.Contains(value, stdioShimName) {
				out = append(out, e)
				continue
			}
			out = append(out, "NODE_OPTIONS="+strings.TrimSpace(value+" "+arg))
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, "NODE_OPTIONS="+arg)
	}
	return out
}
