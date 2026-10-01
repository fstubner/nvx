package nvx

import (
	"os"
	"path/filepath"
	"strings"
)

// sandboxWritableRoots declares what a contained process may write.
//
// It exists because F22 was caused by two callers disagreeing: one granted the
// guest home and the working directory, the other also granted nvxHome and the
// runtime binary directory, and nothing in the type system or the test suite
// objected. Seatbelt and Landlock each apply this its own way -- profile text,
// path rules -- but they no longer decide the policy independently.
//
// Windows reads this too, but cannot share the loop: there the guest home is
// required and takes an integrity label while the working directory is
// best-effort and skipped at the profile root. So prepareAppContainerFilesystem
// grants that pair itself and REFUSES TO LAUNCH if this function names a root it
// does not implement -- fail closed, rather than silently containing less than
// the declaration says.
//
// That guard exists because the two really did drift. The header used to claim
// this was "the single declaration ... for every platform and every isolation
// provider" while Windows read none of it, and an acceptance pass proved it by
// widening this to include the working directory's parent: the unit tests went
// red and the real Windows containment probe stayed green. The same sabotage now
// stops the Windows launch with the offending path named.
//
// ~/.nvx is nvx's control plane: policy.json (the trust baseline every project
// policy is compared against), grants/ (policy pins, approved egress hosts,
// trusted tools), cache/ (command name to absolute path, later executed), and
// tool_home/ (other tools' persisted credentials). A contained process that can
// write any of it can arrange its own trust on the next run, so pinning binds only
// while this stays out of reach.
//
// The guest home legitimately lives *under* nvxHome (~/.nvx/sandbox_home/<session>)
// and is writable. That is fine and is the point: granting a subdirectory is not
// granting the root. Callers must never widen a root to its parent.
func sandboxWritableRoots(guestHome, workDir string) []string {
	roots := make([]string, 0, 2)
	if guestHome != "" {
		roots = append(roots, guestHome)
	}
	if workDir != "" {
		roots = append(roots, workDir)
	}
	return roots
}

// gitMetadataPaths returns the repository metadata inside workDir that a
// contained process may read but never write: workDir/.git, and, where that is
// a file naming the real git directory (a linked worktree, a submodule, a
// --separate-git-dir checkout), that directory too when it lies inside workDir.
// Paths that do not exist are left out.
//
// The working directory is a writable root, and git never runs contained. A
// contained install that writes .git/hooks/pre-commit, or core.hooksPath or
// core.fsmonitor into .git/config, has that code run as the user, uncontained,
// on the next `git commit` or `git status`. Reproduced on Windows and Linux
// before this existed, at isolation level strict as well. Each platform
// subtracts these paths from the writable root its own way: a read-only bind
// mount on Linux, a later deny rule in the Seatbelt profile, a deny entry for
// the project's capability on Windows.
//
// The .git file itself is included, because rewriting it to name a git
// directory the contained process controls has the same effect.
func gitMetadataPaths(workDir string) []string {
	if workDir == "" {
		return nil
	}
	dotGit := filepath.Join(workDir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return nil
	}
	paths := []string{dotGit}
	if info.Mode().IsRegular() {
		if gitDir := gitDirFromFile(dotGit); gitDir != "" && dirWithin(gitDir, workDir) && !dirsEqual(gitDir, workDir) {
			if _, err := os.Stat(gitDir); err == nil {
				paths = append(paths, gitDir)
			}
		}
	}
	return paths
}

// gitDirFromFile reads the "gitdir: <path>" line git writes into a .git file,
// and returns that path made absolute against the file's directory.
func gitDirFromFile(dotGitFile string) string {
	data, err := os.ReadFile(dotGitFile)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(data), "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return ""
	}
	dir := strings.TrimSpace(rest)
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(dotGitFile), dir)
	}
	return filepath.Clean(dir)
}

// workDirReachesControlPlane reports whether granting workDir would also grant
// nvx's own directory or the user's home: workDir is one of them, above one of
// them, or inside nvxHome.
//
// The working directory is a writable root on every platform, and until
// 2026-09-26 nothing looked at which directory it was. A contained command
// started in ~ or / -- where editors commonly start MCP servers -- could write
// ~/.nvx/grants, policy.json and ~/.bashrc. Measured on Linux and on a macOS
// runner: from a project, all three writes were refused; from the home
// directory, all three landed. Windows already skipped the profile root, and
// nothing else.
func workDirReachesControlPlane(nvxHome, workDir string) bool {
	if workDir == "" {
		return false
	}
	home, _ := os.UserHomeDir()
	for _, protected := range []string{nvxHome, home} {
		if protected != "" && dirWithin(protected, workDir) {
			return true
		}
	}
	return nvxHome != "" && dirWithin(workDir, nvxHome)
}

// containedWorkDir is the directory a contained command starts in and may write
// as its own: workDir, or the guest home when workDir would reach nvx's own
// directory or the user's home.
func containedWorkDir(nvxHome, guestHome, workDir string) string {
	if !workDirReachesControlPlane(nvxHome, workDir) {
		return workDir
	}
	warnWorkDirNotWritable(workDir)
	return guestHome
}

func warnWorkDirNotWritable(workDir string) {
	LogWarn("The sandbox may not write %s: it contains your home directory or nvx's own settings. The command starts in the sandbox's home instead; run it from a project folder to work on files there.", workDir)
}
