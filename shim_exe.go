package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Windows shims are nvx itself, hard-linked under each wrapped command's name.
//
// They used to be three files per command -- npm.cmd, npm.ps1 and an
// extensionless sh script for Git Bash -- each a one-liner that started
// `nvx.exe shim npm`. Every one of those costs a shell process to interpret it,
// and on the uncontained path that shell then started nvx, which started
// cmd.exe to run npm.cmd, which started node. Measured 2026-09-02 with
// Get-CimInstance Win32_Process, `npm run dev` for a script that runs `node -e`
// was seven processes: two cmd.exe instances and two nvx.exe instances between
// the user's shell and the node that did the work.
//
// A file named npm.exe is what all three shells resolve first: cmd.exe and
// PowerShell walk PATHEXT (.COM, .EXE, then .BAT and .CMD), and Git Bash tries
// `npm` and then `npm.exe`. Because it is a hard link, it IS nvx -- no
// interpreter, no extra process, and no copy per command to fall out of date.
// nvx recognises the name it was started under (shimInvocationArgs) and behaves
// as `nvx shim npm`.

// shimCommandForExecutable returns the wrapped command that an executable name
// stands for, or "" when the binary is nvx itself (or anything else that is not
// a shim name, such as the staged sandbox supervisor's per-build filename).
func shimCommandForExecutable(exe string) string {
	base := filepath.Base(exe)
	if strings.EqualFold(filepath.Ext(base), ".exe") {
		base = base[:len(base)-len(".exe")]
	}
	for _, c := range allShimCommands() {
		if strings.EqualFold(c, base) {
			return c
		}
	}
	return ""
}

// shimInvocationArgs rewrites os.Args for a process started under a shim's name
// into the `nvx shim <name> <args...>` form the rest of nvx already handles.
//
// The rewrite has to happen BEFORE parseStartupFlags reads os.Args, for the
// same reason the .cmd shims passed `shim npm %*`: everything after the wrapped
// command's name belongs to that command. `npm --no-sandbox install` must hand
// npm its --no-sandbox, not disable the sandbox; with "shim" in front, the flag
// parser stops at the first non-flag token and never sees it.
func shimInvocationArgs(exe string, args []string) []string {
	cmd := shimCommandForExecutable(exe)
	if cmd == "" || len(args) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0], "shim", cmd)
	return append(out, args[1:]...)
}

// nvxBinaryFor returns the nvx binary to name in files and messages when the
// running executable is exe.
//
// os.Executable() reports the name this process was started under, which for
// a shim is npm.exe. Embedding that into a project-bin shim would produce
// `npm.exe shim vite ...`, which the rewrite above turns into `npm shim vite`;
// printing it in a hint would tell the user to type a command that does not
// mean what it says. The real nvx is the sibling every shim is linked to.
func nvxBinaryFor(exe string) string {
	if shimCommandForExecutable(exe) == "" {
		return exe
	}
	sibling := filepath.Join(filepath.Dir(exe), nvxExecutableName())
	if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
		return sibling
	}
	return exe
}

// selfNvxBinary is os.Executable() corrected for a shim name; see nvxBinaryFor.
func selfNvxBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	return nvxBinaryFor(self), nil
}

// legacyShimSuffixes are the files an older nvx wrote per command on Windows.
// All three are removed when the exe shims are written: bash prefers a bare
// `npm` over npm.exe, and a PATHEXT that lists .CMD before .EXE prefers npm.cmd,
// so a leftover would keep routing through the slower shim -- or, after an
// upgrade, through a stale one.
var legacyShimSuffixes = []string{".cmd", ".ps1", ""}

// writeWindowsExeShims links every wrapped command's <cmd>.exe to target (the
// installed nvx.exe beside them), replacing whatever an older nvx left.
func writeWindowsExeShims(shimDir, target string) error {
	sweepStaleShimExes(shimDir)
	for _, cmd := range allShimCommands() {
		for _, suffix := range legacyShimSuffixes {
			if err := os.Remove(filepath.Join(shimDir, cmd+suffix)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove old %s%s shim: %w", cmd, suffix, err)
			}
		}
		if err := linkShimExe(target, filepath.Join(shimDir, cmd+".exe")); err != nil {
			return fmt.Errorf("write %s.exe shim: %w", cmd, err)
		}
	}
	return nil
}

// linkShimExe makes dst a hard link to target, or a copy where linking is not
// possible (a filesystem without hard links). See refreshLink.
func linkShimExe(target, dst string) error {
	return refreshLink(target, dst)
}

// refreshLink makes dst the same file as src: a hard link where the filesystem
// allows one, a copy otherwise.
//
// A dst that already IS src is left alone: `nvx env` regenerates shims at every
// shell start, and re-linking seven files each time is churn. Anything else is
// replaced -- including a file with src's size and modification time. A first
// version took that as proof the link was current, and for a link it proves
// nothing: installNvxCopy renames a fresh nvx.exe over the old one and every
// link stays on the OLD file, a node.exe reinstalled from the same archive
// carries the archive's timestamp, and a CI runner wrote two files inside one
// timestamp tick and watched the relink get skipped. Size and time are
// consulted only on the copy path, where they are what stops a filesystem
// without hard links from copying the binary again at every shell start.
//
// The link is made under a temporary name and renamed over dst, so no partial
// state is ever visible. A dst that is executing cannot be replaced that way
// on Windows, but it can be renamed; the renamed-aside file is deleted by
// sweepStaleShimExes on a later run, once nothing is executing it.
func refreshLink(src, dst string) error {
	if sameExistingFile(src, dst) {
		return nil
	}
	tmp := fmt.Sprintf("%s.link-%d", dst, os.Getpid())
	if err := os.Link(src, tmp); err == nil {
		if err := replaceFile(tmp, dst); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	if sameSizeAndTime(src, dst) {
		return nil
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		if rerr := os.Rename(dst, asideName(dst)); rerr != nil {
			return err
		}
	}
	if err := installNvxCopy(src, dst); err != nil {
		return err
	}
	// A copy is recognised as current by size and time on the next run, the
	// same way the staged sandbox supervisor identifies its build.
	if info, err := os.Stat(src); err == nil {
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	return nil
}

// replaceFile renames tmp over dst, moving a dst that is in use aside first.
func replaceFile(tmp, dst string) error {
	err := os.Rename(tmp, dst)
	if err == nil {
		return nil
	}
	if rerr := os.Rename(dst, asideName(dst)); rerr != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// asideName is where a file that is executing gets moved so its name can be
// reused. sweepStaleShimExes matches it.
func asideName(dst string) string {
	return fmt.Sprintf("%s.stale-%d", dst, os.Getpid())
}

// sameSizeAndTime reports whether b is a copy of a made by refreshLink: same
// size, same modification time. Only consulted when they are not the same file.
func sameSizeAndTime(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return ai.Size() == bi.Size() && ai.ModTime().Equal(bi.ModTime())
}

// sweepStaleShimExes deletes shims that linkShimExe had to rename aside because
// they were running at the time. One still running refuses to delete, which is
// the correct outcome and not an error.
func sweepStaleShimExes(shimDir string) {
	matches, err := filepath.Glob(filepath.Join(shimDir, "*.exe.stale-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
