//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// A project nvx used before 0.5.0 stays writable by every current sandbox, and
// until now there was no way to find out.
//
// Up to 0.5.0 every sandbox ran as one shared package identity and the grants it
// wrote were never revoked, so any project nvx touched carries an `(OI)(CI)(M)`
// ACE for a package SID that today's sandboxes still hold. A contained install in
// project A can therefore write into project B. `removeStaleAppContainerGrant`
// clears them, but only for the working directory of the session currently
// running -- so a project cleans itself the next time you use nvx there, and a
// project you never revisit stays exposed indefinitely.
//
// README has always disclosed this. What was missing was any way to observe it:
// the comparable leftover, the loopback exemption, warns on every contained launch
// and fails `doctor`, while this one was silent. nvx keeps no record of where it
// has run, so it cannot sweep the machine -- but it can answer the question for
// the directory the user is standing in, which is the one about to matter.
//
// Deliberately not a launch-path warning: the launch path already removes these
// from the working directory before running anything, so by then there is nothing
// left to report. `doctor` is the right home because it inspects without running.

// staleGrantReport describes the leftover package-SID grants on one directory.
type staleGrantReport struct {
	Dir  string
	SIDs []string
}

// scanStaleProjectGrants looks for leftover package-SID ACEs on the project root
// containing dir, falling back to dir itself when it is not inside a project.
func scanStaleProjectGrants(dir string) staleGrantReport {
	if dir == "" {
		return staleGrantReport{}
	}
	root := findProjectRoot(dir)
	if root == "" {
		root = dir
	}
	return staleGrantReport{Dir: root, SIDs: staleAppContainerSIDsOn(root)}
}

// reportStaleProjectGrants prints the finding and, under fix, removes it. Returns
// true if anything was found, so doctor can count it against health -- a project
// that any sandbox on the machine can write into is not a healthy install, and
// reporting it while exiting 0 is the shape of dishonesty this command keeps being
// caught by.
func reportStaleProjectGrants(dir string, fix bool) bool {
	rep := scanStaleProjectGrants(dir)
	if len(rep.SIDs) == 0 {
		return false
	}

	if fix {
		// The package SID argument is unused by the removal path -- it removes
		// every stale SID it finds -- but the signature documents intent.
		removeStaleAppContainerGrant("", rep.Dir)
		if remaining := staleAppContainerSIDsOn(rep.Dir); len(remaining) > 0 {
			LogWarn("  [FAIL] %d sandbox permission(s) remain on %s; removing them needs write access to its ACL", len(remaining), rep.Dir)
			return true
		}
		LogSuccess("Removed %d stale sandbox permission(s) from %s.", len(rep.SIDs), rep.Dir)
		return false
	}

	LogWarn("  [FAIL] %s carries %d sandbox permission(s) from before 0.5.0", rep.Dir, len(rep.SIDs))
	LogWarn("         any nvx sandbox on this machine can read and write this project, whatever its own policy says")
	LogInfo("         remove them with: nvx doctor --fix")
	// One worked example, not nineteen. %s not %q: a quoted Go string escapes the
	// backslashes and the user would paste a command that does not work.
	LogInfo("         or by hand, once per entry that 'icacls \"%s\"' lists as S-1-15-2-...:", rep.Dir)
	LogInfo("           icacls \"%s\" /remove:g *%s", rep.Dir, rep.SIDs[0])
	LogInfo("         nvx keeps no record of where it has run, so other projects you have not")
	LogInfo("         revisited may carry these too. Run 'nvx doctor' in each to check.")
	return true
}

// reportStaleProjectGrantsHere is the doctor hook: inspect the project the user is
// standing in. Off Windows this whole concern does not exist -- see the _other
// build.
func reportStaleProjectGrantsHere(fix bool) bool {
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	return reportStaleProjectGrants(filepath.Clean(wd), fix)
}
