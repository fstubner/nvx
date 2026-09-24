//go:build linux

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// linuxSystemToolDirs is where a Linux system tool is looked for, in this
// order. It is the PATH that bringUpLoopback and runIptables used to hand the
// child in cmd.Env, now used for the lookup itself.
var linuxSystemToolDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// systemToolPath returns the absolute path of a Linux system tool, taken from
// the fixed system directories above, never from PATH.
//
// name is a bare program name: "ip", "iptables". A tool that is in none of
// those directories is an error, not a reason to search PATH.
//
// The calls this serves set cmd.Env to a PATH of these directories, which
// reads as pinning the program and does not: exec.Command resolves a bare name
// through the calling process's own PATH when the Cmd is built, and cmd.Env
// only reaches the child. Measured 2026-09-25 in an ubuntu:24.04 container: a
// script called `ip` first on the process PATH ran from bringUpLoopback, and
// one called `iptables` ran from runIptables, each with that restricted
// cmd.Env in place. See the Windows half in system_tool_windows.go for why
// PATH is not nvx's to trust.
func systemToolPath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("system tool %q: want a bare program name", name)
	}
	for _, dir := range linuxSystemToolDirs {
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
			continue
		}
		return p, nil
	}
	return "", fmt.Errorf("system tool %s: not found in %s", name, strings.Join(linuxSystemToolDirs, ", "))
}
