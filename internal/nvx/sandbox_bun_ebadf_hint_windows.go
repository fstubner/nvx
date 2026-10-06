//go:build windows

package nvx

import (
	"path/filepath"
	"strings"
	"unicode"
)

// bun's package manager cannot run in the sandbox for a project outside the
// system drive.
//
// It opens package.json and asks Windows for the file's path by handle, which an
// AppContainer is refused. Bun's fallback (lowbox_dos_name_fallback in
// src/sys/windows/mod.rs, tag bun-v1.4.2) rebuilds the path only for the volume
// that holds the system directory. Anywhere else the command dies with a bare
// "error: An internal error occurred (EBADF)". Measured 2026-10-04 with bun
// 1.4.2: `bun install` fails on D: and H: and works on C:, with or without the
// drive-root grants an older `nvx setup` made. The upstream fix is oven-sh/bun#38365, and
// nvx cannot make that call succeed from outside.
//
// bunx does not fail this way (measured the same day), so only the subcommands
// that did are named.

// bunPackageManagerSubcommands are the bun subcommands measured to fail with
// EBADF off the system drive.
var bunPackageManagerSubcommands = map[string]bool{
	"install": true, "i": true, "add": true, "a": true, "remove": true, "rm": true,
	"update": true, "up": true, "patch": true, "pm": true,
}

// bunPackageManagerOffSystemDrive reports whether a failed command is a bun
// package-manager run in a project on a different drive than systemDir.
//
// The exit code is the only evidence of failure, so the note it gates is worded
// as one possible cause. A drive it cannot read as "X:" (a UNC share, a device
// path) answers false rather than guess.
func bunPackageManagerOffSystemDrive(command string, args []string, workDir, systemDir string, exitCode int) bool {
	if exitCode == 0 {
		return false
	}
	base := strings.ToLower(filepath.Base(command))
	if strings.TrimSuffix(base, ".exe") != "bun" {
		return false
	}
	sub := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub = strings.ToLower(a)
			break
		}
	}
	if !bunPackageManagerSubcommands[sub] {
		return false
	}
	work, ok := driveLetterOf(workDir)
	if !ok {
		return false
	}
	system, ok := driveLetterOf(systemDir)
	return ok && work != system
}

// driveLetterOf returns the upper-case letter of a path that starts with "X:".
func driveLetterOf(p string) (letter rune, ok bool) {
	v := filepath.VolumeName(p)
	if len(v) != 2 || v[1] != ':' || !unicode.IsLetter(rune(v[0])) {
		return 0, false
	}
	return unicode.ToUpper(rune(v[0])), true
}

// noteBunOffSystemDrive prints the explanation after bun's own output. It runs
// once per launch, after the child has exited.
func noteBunOffSystemDrive(command string, args []string, workDir string, exitCode int) {
	systemDir, err := systemDirectory()
	if err != nil || !bunPackageManagerOffSystemDrive(command, args, workDir, systemDir, exitCode) {
		return
	}
	drive, _ := driveLetterOf(systemDir)
	LogWarn("bun cannot run its package manager inside the Windows sandbox in a project outside %c: (a bun limitation, oven-sh/bun#38365).", drive)
	LogWarn("If the error above is EBADF, run it with `nvx --no-sandbox bun ...`, which runs it uncontained, or move the project to %c:.", drive)
}
