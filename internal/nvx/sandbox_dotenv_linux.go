//go:build linux

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// maskDotenvFiles covers each dotenv file in the project with an empty file
// the contained process cannot read, in the supervisor's private mount
// namespace. See isDotenvName for which files.
//
// Landlock cannot take a file back out of a directory it grants, as with .git
// (see mountGitMetadataReadOnly). A bind mount over the file can. The mask is
// one new empty file with mode 0000, mounted read-only over each:
//
//   - A read fails with EACCES, as for any file the process may not read.
//     /dev/null would answer with empty content, which a tool takes for a .env
//     with nothing in it.
//   - A write fails with EACCES too, and a chmod that would allow one with
//     EROFS, so the file the user keeps is not changed.
//   - A rename or unlink fails with EBUSY on a mount point, and a hard link with
//     EXDEV, so the real file cannot be moved to a name nothing covers.
//
// Mode 0000 holds only against a process without CAP_DAC_OVERRIDE and
// CAP_DAC_READ_SEARCH. Root in the supervisor's user namespace holds both over
// every file the user owns, the mask included, and the target ran as that root
// until 2026-10-07. So they leave the bounding set with CAP_SYS_ADMIN, which
// would let the target unmount the mask. The target runs as the user now and
// dropTargetCapabilities empties the bounding set, so these drops are a second
// layer. Without the capabilities the target reads and writes the user's files
// as the user does outside the sandbox. They go even when the launch finds no
// dotenv file, because watchDotenvFiles may cover one later.
//
// This covers the files present at launch. watchDotenvFiles covers those
// created or replaced during the run. The walk stops after dotenvScanLimit
// entries, with a warning.
//
// Called before enterSandboxRoot, whose recursive bind of the working
// directory carries these mounts into the sandbox's view, and before Landlock,
// which refuses mounts. Any failure is fatal to the launch, as for .git. It
// returns the mask it mounted, or nil, so the watcher can tell a covered file
// from a new one.
func maskDotenvFiles(workDir, guestHome string, nsErr error) (os.FileInfo, error) {
	if workDir == "" {
		return nil, nil
	}
	targets := dotenvMaskTargets(workDir)
	if nsErr != nil {
		if len(targets) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("no private mount namespace for %s: %w", targets[0], nsErr)
	}
	var maskInfo os.FileInfo
	if len(targets) > 0 {
		mask, err := createDotenvMask(guestHome)
		if err != nil {
			return nil, err
		}
		// The mounts hold the file open. Its name goes now, so nothing in the guest
		// home leads to it.
		defer os.Remove(mask)
		if maskInfo, err = os.Stat(mask); err != nil {
			return nil, fmt.Errorf("read the dotenv mask: %w", err)
		}
		for _, p := range targets {
			if err := bindMountReadOnlyFrom(mask, p); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range []uintptr{capSysAdmin, capDacOverride, capDacReadSearch} {
		if err := dropFromBoundingSet(c); err != nil {
			return nil, fmt.Errorf("drop capability %d for the sandboxed command: %w", c, err)
		}
	}
	return maskInfo, nil
}

// dotenvMaskTargets returns the files to cover: each dotenv file under workDir
// with symbolic links resolved, because a mount lands on a link's target, and
// only regular files, because a file can only be mounted over a file.
func dotenvMaskTargets(workDir string) []string {
	found, complete := findDotenvFiles(workDir, dotenvScanLimit)
	if !complete {
		LogWarn("Stopped looking for .env files after %d entries under %s; any further down stay readable in the sandbox.", dotenvScanLimit, workDir)
	}
	var targets []string
	for _, f := range found {
		resolved, err := filepath.EvalSymlinks(f)
		if err != nil {
			continue
		}
		if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
			continue
		}
		targets = append(targets, resolved)
	}
	return dedupeStrings(targets)
}

// createDotenvMask creates the empty, mode 0000 file maskDotenvFiles mounts.
func createDotenvMask(dir string) (string, error) {
	f, err := os.CreateTemp(dir, ".nvx-dotenv-mask-*")
	if err != nil {
		return "", fmt.Errorf("create the dotenv mask: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("create the dotenv mask: %w", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("make the dotenv mask unreadable: %w", err)
	}
	return path, nil
}

// capDacOverride and capDacReadSearch are from linux/capability.h.
const (
	capDacOverride   = 1
	capDacReadSearch = 2
)

// dropFromBoundingSet removes capability c from what the target can hold after
// exec. See dropSysAdminFromBoundingSet.
func dropFromBoundingSet(c uintptr) error {
	_, _, errno := syscall.RawSyscall6(prctlSyscall(), prCapBSetDrop, c, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
