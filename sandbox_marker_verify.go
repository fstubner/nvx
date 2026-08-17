package main

import (
	"os"
	"os/user"
	"path/filepath"
)

// containmentDisproved reports whether this process can be PROVEN to be running
// outside any nvx sandbox.
//
// NVX_SANDBOX=1 tells nvx "you are already inside the sandbox, do not contain
// again". It is an ordinary inherited environment variable, so anything that can
// set it in the ambient environment -- an export in a shell profile, a CI variable,
// a malicious postinstall that edits an rc file -- silently disables containment
// for every later nvx run. Nothing announces it and nothing checks it.
//
// The marker cannot simply be replaced with an argument. Inside the sandbox the
// contained process runs arbitrary commands, and a nested nvx (npm -> node -> nvx
// shim) is a grandchild that inherits the environment, not our argv. So the marker
// stays and gets verified instead.
//
// The check is deliberately one-directional: it can only ever DISPROVE containment,
// never confirm it. A wrong "not contained" answer costs an unnecessary sandbox,
// which is safe. A wrong "contained" answer would skip containment entirely, so
// that verdict is never produced -- anything inconclusive leaves the marker
// trusted, exactly as before.
//
// The probe: every platform's containment grants write access to the guest home and
// the working directory only, never the user's real home directory. So if we can
// create a file there, no nvx sandbox is in force.
func containmentDisproved() bool {
	home := realHomeDir()
	if home == "" {
		return false // cannot tell; leave the marker trusted
	}

	f, err := os.CreateTemp(home, ".nvx-containment-check-*")
	if err != nil {
		// Denied, missing, or read-only. Any of these is inconclusive: it is what a
		// contained process sees, but also what an unusual host looks like.
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// realHomeDir resolves the user's home directory WITHOUT consulting $HOME or
// %USERPROFILE%, because the sandbox redirects both to the ephemeral guest home --
// which is writable, and would make the probe report "not contained" from inside.
//
// user.Current reads the OS account database (/etc/passwd, or the token's profile
// path on Windows), which the contained process cannot redirect.
func realHomeDir() string {
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		return ""
	}
	// A guest home lives under nvxHome; if the account database somehow points
	// there, the probe would be meaningless.
	if home := filepath.Clean(u.HomeDir); home != "" && home != "." {
		return home
	}
	return ""
}
