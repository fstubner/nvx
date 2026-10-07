//go:build windows

package nvx

import (
	"path/filepath"
	"strings"
)

// A batch file runs inside the sandbox only when cmd.exe is given its path in
// quotes.
//
// Windows runs a .cmd or .bat by starting `cmd.exe /c <command line>`, and
// inside an AppContainer cmd.exe answers an unquoted absolute path with
// "Access is denied." Measured 2026-10-07 on Windows 11 build 26300, with a
// batch file in a working directory the container may read and write:
//
//	cmd /d /c C:\...\x.cmd                    Access is denied.
//	cmd /d /c "C:\...\x.cmd"                  Access is denied. (cmd drops the quotes)
//	cmd /d /c C:\Windows\System32\whoami.exe  Access is denied.
//	cmd /d /s /c ""C:\...\x.cmd" a "b c" d"   runs
//	cmd /d /c .\x.cmd                         runs
//	cmd /d /c call "C:\...\x.cmd"             runs
//	cmd /d /c Q:\x.cmd                        runs, with Q: a subst drive for the same folder
//
// The subst drive is the telling case. The same file runs when it is named
// from a root whose every folder the container may list. Named from C:\, it
// needs list access on C:\, C:\Users and each folder below them, and the
// container has it on none of them. Granting that would need Administrator
// rights on C:\ and C:\Users, and would let every sandbox list the folders
// inside your profile. A quoted path needs neither. pnpm and yarn installed
// with `npm install -g`, and project bin shims under strict isolation, are
// batch files nvx launches by full path, so every one of them failed this way.
//
// Windows' own handling of a batch file cannot be steered into the quoted
// form. It passes the caller's command line to `cmd.exe /c` without /s, and
// cmd.exe then removes the quote in front of the path along with the last
// quote on the line. Measured the same day, CreateProcess on the batch file
// failed with the path quoted in the command line too. So nvx starts cmd.exe
// itself.

// isWindowsBatchFile reports whether path names a batch file.
func isWindowsBatchFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return true
	}
	return false
}

// windowsBatchLaunch returns the cmd.exe to start and the whole command line
// that runs batch with args, with the batch file's path in quotes.
//
// /s makes cmd.exe remove exactly the outer pair of quotes, so the pair around
// the path survives whatever the arguments hold. /d skips AutoRun commands from
// the registry, as npm does for the scripts it runs. The arguments are quoted
// as before, so a batch file sees them the way it did when Windows started
// cmd.exe for it.
func windowsBatchLaunch(batch string, args []string) (exe, cmdLine string, err error) {
	exe, err = systemToolPath("cmd.exe")
	if err != nil {
		return "", "", err
	}
	inner := `"` + batch + `"`
	if len(args) > 0 {
		inner += " " + buildWindowsArgString(args)
	}
	return exe, quoteWindowsArg(exe) + ` /d /s /c "` + inner + `"`, nil
}
