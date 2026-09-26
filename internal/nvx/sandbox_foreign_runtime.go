package nvx

import (
	"os"
	"path/filepath"
	"strings"
)

// Containing a runtime nvx does not manage.
//
// nvx grants the sandbox read and execute on its own versions/ tree, which is
// the only runtime it knew about. A developer using mise, fnm, NVM for Windows
// or a plain nodejs.org install has node somewhere else entirely, and the
// contained command then cannot execute the very binary it was asked to run:
// measured on Linux 2026-09-20 with a foreign node on PATH and nvx managing
// nothing, the sandbox started (Landlock and the namespaces both active) and
// the launch failed with "fork/exec .../bin/npm: permission denied".
//
// The mechanism to fix that already existed -- `isolation.filesystem.
// allow_read_exec` -- and naming the foreign runtime's root there by hand makes
// the same command succeed. This file only removes the "by hand": nvx is about
// to execute that binary, so being able to read it is not a widening of the
// sandbox, it is the minimum for the command to run at all.
//
// What this does NOT do is make the foreign tree writable. It joins the
// read/execute roots, which are never added to the writable set on any
// platform -- see sandbox_read_exec_roots.go.

// foreignRuntimeReadExecRoot returns the directory a contained command needs
// read and execute on when its runtime is not one nvx manages, or "" when
// nothing should be added.
//
// The root is the runtime's version directory rather than the binary's own
// directory. On Unix the binary sits in <version>/bin while the package
// manager it runs lives in <version>/lib/node_modules, so granting only bin/
// gets npm as far as failing to find itself.
func foreignRuntimeReadExecRoot(nvxHome, cmdPath string) string {
	if nvxHome == "" || cmdPath == "" {
		return ""
	}
	// Managed-ness is asked of the resolved path as well as the literal one: a
	// command reached through a link like ~/.nvx/current is nvx's own runtime
	// wearing another name, and staging a copy of it would be pointless work.
	cmdPath = filepath.Clean(cmdPath)
	if isUnderNvxVersions(nvxHome, cmdPath) {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(cmdPath); err == nil {
		if isUnderNvxVersions(nvxHome, filepath.Clean(resolved)) {
			return ""
		}
	}

	// The ROOT is computed from where the command sits on PATH, never from a
	// symlink's target. `npm` in a Node install is a link into
	// <version>/lib/node_modules/npm/bin/npm-cli.js, so following it first and
	// climbing from there granted npm's own JavaScript and left node.exe out:
	// measured on Linux 2026-09-20, the contained install then died on
	// "/usr/bin/env: 'node': Permission denied". The link's own location is the
	// one that describes the install's layout.
	dir := filepath.Dir(cmdPath)
	root := dir
	// <version>/bin/node -> <version>. Windows layouts put node.exe at the
	// version root already, so there is nothing to climb there.
	if strings.EqualFold(filepath.Base(dir), "bin") {
		root = filepath.Dir(dir)
	}
	if isTooBroadToGrant(root) {
		// A runtime installed into a shared system prefix. Climbing to /usr or
		// C:\Program Files would hand the sandbox most of the machine to read,
		// which is the opposite of the point, so nvx grants the binary's own
		// directory and says what it did. A tool that needs more than that can
		// be named in allow_read_exec deliberately.
		if isTooBroadToGrant(dir) {
			return ""
		}
		return dir
	}
	return root
}

// isUnderNvxVersions reports whether a path sits inside nvx's own runtime tree.
//
// Cross-platform on purpose. The Windows sandbox has its own copy of this test
// (isNvxManagedRuntimePath) written against the same versions/ layout; this one
// is the shape every platform needs to answer "did nvx install that".
func isUnderNvxVersions(nvxHome, path string) bool {
	versionsRoot := filepath.Join(nvxHome, "versions")
	rel, err := filepath.Rel(versionsRoot, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isTooBroadToGrant reports directories nvx will not hand to a sandbox whole.
//
// These are shared prefixes: everything installed on the machine lives under
// them, so granting one is not "let the command read its own runtime", it is
// "let the command read every program here". A filesystem root is included for
// the degenerate case of a binary sitting directly on a drive.
func isTooBroadToGrant(dir string) bool {
	clean := filepath.Clean(dir)
	if parent := filepath.Dir(clean); parent == clean {
		return true // a filesystem or drive root
	}
	broad := []string{
		"/usr", "/usr/local", "/usr/bin", "/usr/local/bin", "/opt", "/bin", "/sbin",
		"/usr/lib", "/var", "/etc", "/home", "/Applications", "/Library", "/System",
	}
	for _, b := range broad {
		if strings.EqualFold(clean, filepath.Clean(b)) {
			return true
		}
	}
	// Windows shared prefixes, read from the environment so a machine with a
	// relocated Program Files is covered too.
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramData", "SystemRoot", "windir", "LOCALAPPDATA", "APPDATA"} {
		if v := os.Getenv(env); v != "" && strings.EqualFold(clean, filepath.Clean(v)) {
			return true
		}
	}
	return false
}

// withForeignRuntimeRoot appends the foreign runtime's root to the policy's
// own read/execute roots, if the command resolves to a runtime nvx does not
// manage and the root is one it is willing to grant.
//
// Resolution repeats what the sandbox itself will do -- the nvx-managed
// version first, then PATH with nvx's own shim directories removed -- because
// the roots have to be decided before the launch that resolves the binary.
// Both halves read the same functions, so they cannot disagree about which
// binary is meant.
func withForeignRuntimeRoot(nvxHome, cmdName string, roots []string) []string {
	rt := runtimeForShim(cmdName)
	activeVer := getActiveShellVersionFor(nvxHome, rt.Name())
	if activeVer == "" {
		activeVer = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}
	if resolvePinnedCommandPath(cmdName, nvxHome, activeVer, rt) != "" {
		return roots // nvx manages this runtime; its own tree is granted already
	}
	cmdPath, err := lookPathSkippingNvxShims(cmdName, nvxHome)
	if err != nil {
		return roots
	}
	root := foreignRuntimeReadExecRoot(nvxHome, cmdPath)
	if root == "" {
		return roots
	}
	for _, existing := range roots {
		if strings.EqualFold(filepath.Clean(existing), root) {
			return roots
		}
	}
	LogDetail("Runtime for %s is not managed by nvx (%s); granting the sandbox read and execute there.", cmdName, root)
	return append(roots, root)
}
