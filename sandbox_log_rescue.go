package main

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A failing contained install tells you to read a log that no longer exists.
//
// npm writes its debug log under the cache directory, which nvx redirects into
// the ephemeral guest home. So a failed install ends with:
//
//	npm error A complete log of this run can be found in:
//	C:\Users\<u>\.nvx\sandbox_home\<id>\AppData\...\npm-cache\_logs\...-debug-0.log
//
// and that whole tree is deleted when the run ends. The path is dead by the time
// it is printed. Reported from real use 2026-08-20, on exactly the occasion the
// log matters: a failure the user wanted to diagnose.
//
// The guest home is ephemeral on purpose and that does not change. What changes is
// that the logs are copied out first, and only when the command failed -- a
// successful run's logs are noise nobody asks for.

// rescuedLogsDir is where a failed run's logs are kept, outside the guest home so
// they survive it. Under nvxHome rather than the project: a contained process must
// not be able to write there, and the project is not nvx's to litter.
func rescuedLogsDir(nvxHome, sandboxID string) string {
	return filepath.Join(nvxHome, "logs", sandboxID)
}

// rescueSandboxLogs copies any debug logs out of the guest home before it is
// deleted, and returns where they went. Empty when there was nothing to copy.
//
// Best-effort throughout: this runs while a command is already failing, and an
// error copying a log must not replace the error the user actually needs to see.
func rescueSandboxLogs(nvxHome, guestHome, sandboxID string) string {
	sources := findDebugLogDirs(guestHome)
	if len(sources) == 0 {
		return ""
	}
	dest := rescuedLogsDir(nvxHome, sandboxID)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return ""
	}

	copied := 0
	for _, dir := range sources {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if err := copyLogFile(filepath.Join(dir, e.Name()), filepath.Join(dest, e.Name())); err == nil {
				copied++
			}
		}
	}
	if copied == 0 {
		_ = os.Remove(dest)
		return ""
	}
	return dest
}

// findDebugLogDirs locates `_logs` directories inside the guest home.
//
// Matching on the directory name rather than reconstructing npm's cache layout:
// that layout differs by platform and npm version, and yarn and pnpm use the same
// `_logs` convention. A name match finds all of them and cannot go stale.
func findDebugLogDirs(guestHome string) []string {
	if guestHome == "" {
		return nil
	}
	var dirs []string
	// Depth-limited: the guest home holds an unpacked cache, and walking all of it
	// on every failure would cost more than the logs are worth.
	const maxDepth = 8
	base := filepath.Clean(guestHome)
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip it, do not abort the rescue
		}
		if !d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(base, path)
		if rerr == nil && strings.Count(rel, string(os.PathSeparator)) > maxDepth {
			return filepath.SkipDir
		}
		if strings.EqualFold(d.Name(), "_logs") {
			dirs = append(dirs, path)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}

// copyLogFile copies one debug log. Local rather than reusing copyFile, which is
// Windows-only: the guest home is ephemeral on every platform, so this fix is not
// Windows-specific and should not be built that way.
//
// Size-capped. A debug log is normally tens of kilobytes, but it is written by the
// contained process, so treating it as arbitrarily large input is the safer
// assumption -- rescuing a log must not be a way to fill the user's disk.
func copyLogFile(src, dst string) error {
	const maxLogBytes = 8 << 20

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, io.LimitReader(in, maxLogBytes)); err != nil {
		return err
	}
	return out.Close()
}

// rescuedLogRetention is how long a failed run's logs are kept.
//
// They exist to be read after a failure, which happens in the minutes or days
// after it, not the months. Nothing else reclaims them: guest homes and package
// profiles each have a sweep and these did not, so they accumulated for as long
// as nvx had been installed. Measured on the development machine 2026-08-30,
// found by an acceptance pass: 3,146 directories, 181 MB, and `nvx cleanup` left
// every one of them.
const rescuedLogRetention = 14 * 24 * time.Hour

// rescuedLogBudgetPerRun bounds one command's share of the backlog. Higher than
// the guest-home and package budgets because removing a small folder of log files
// is cheaper than either of those, and a backlog measured in thousands needs to
// drain in tens of commands rather than hundreds.
const rescuedLogBudgetPerRun = 64

// sweepRescuedLogs deletes rescued log directories older than the retention
// window and reports how many went. A budget of 0 means no limit.
//
// Age is the whole rule, deliberately. A rescued log belongs to a run that has
// already ended -- that is what rescuing means -- so unlike a guest home or a
// package profile there is no live owner to check for, and nothing here can be
// in use.
func sweepRescuedLogs(nvxHome string, budget int) int {
	if nvxHome == "" {
		return 0
	}
	root := filepath.Join(nvxHome, "logs")
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	now := time.Now()
	removed := 0
	for _, e := range entries {
		if budget > 0 && removed >= budget {
			break
		}
		if !e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || now.Sub(info.ModTime()) < rescuedLogRetention {
			continue
		}
		if os.RemoveAll(filepath.Join(root, e.Name())) == nil {
			removed++
		}
	}
	return removed
}
