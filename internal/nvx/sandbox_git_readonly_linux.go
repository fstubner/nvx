//go:build linux

package nvx

import (
	"fmt"
	"syscall"
)

// mountGitMetadataReadOnly bind-mounts each of gitMetadataPaths(workDir) onto
// itself read-only, in the supervisor's private mount namespace.
//
// Landlock cannot do this. Its rules only add access, beneath a directory, so
// granting the working directory grants .git with it, and there is no rule that
// takes a subdirectory back out. A read-only mount does: the contained process
// still reads .git through the working directory's rule, and every write under
// it fails with EROFS whatever Landlock allows.
//
// Called before landlock_restrict_self, which refuses every mount afterwards.
// The target inherits these mounts through its own CLONE_NEWNS copy.
//
// nsErr is the result of entering the private mount namespace. With nothing to
// protect it does not matter. With something to protect it is fatal, as is any
// mount failure: the caller refuses to launch rather than run with .git
// writable. The target's own CLONE_NEWNS needs the same capability in the same
// user namespace, so a host that refuses the namespace refuses the target too.
func mountGitMetadataReadOnly(workDir string, nsErr error) error {
	paths := gitMetadataPaths(workDir)
	if len(paths) == 0 {
		return nil
	}
	if nsErr != nil {
		return fmt.Errorf("no private mount namespace for %s: %w", paths[0], nsErr)
	}
	for _, p := range paths {
		if err := bindMountReadOnly(p); err != nil {
			return err
		}
	}
	if err := dropSysAdminFromBoundingSet(); err != nil {
		return fmt.Errorf("drop CAP_SYS_ADMIN for the sandboxed command: %w", err)
	}
	return nil
}

// bindMountReadOnly makes path a read-only view of itself.
//
// A bind mount takes the flags of the mount it was made from, and inside a user
// namespace the flags inherited from the host's mounts are locked: a remount
// that drops nosuid, nodev, noexec or an atime flag is refused with EPERM. So
// the remount carries whatever of those the path already has. statfs reports
// them in ST_* bits, which have the same values as the MS_* bits used here.
func bindMountReadOnly(path string) error {
	return bindMountReadOnlyFrom(path, path)
}

// bindMountReadOnlyFrom shows src at path, read-only, the way bindMountReadOnly
// shows a path as itself.
func bindMountReadOnlyFrom(src, path string) error {
	if err := syscall.Mount(src, path, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("bind %s: %w", path, err)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return fmt.Errorf("read the mount flags of %s: %w", path, err)
	}
	const locked = syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC |
		syscall.MS_NOATIME | syscall.MS_NODIRATIME | syscall.MS_RELATIME
	// #nosec G115 -- statfs flags are a small bit set; only the bits in locked are kept
	keep := uintptr(st.Flags) & locked
	flags := syscall.MS_BIND | syscall.MS_REMOUNT | syscall.MS_RDONLY | keep
	if err := syscall.Mount("", path, "", flags, ""); err != nil {
		return fmt.Errorf("make %s read-only: %w", path, err)
	}
	return nil
}

// capSysAdmin and prCapBSetDrop are from linux/capability.h and linux/prctl.h.
const (
	capSysAdmin   = 21
	prCapBSetDrop = 24
)

// dropSysAdminFromBoundingSet removes CAP_SYS_ADMIN from what the target can
// hold after exec.
//
// The target ran as root in the supervisor's user namespace until 2026-10-07,
// and held every capability there, CAP_SYS_ADMIN over the mount namespace that
// carries the read-only .git mount included. It runs as the user now and
// dropTargetCapabilities empties the bounding set, so this drop is a second
// layer. Landlock refuses mount, umount and remount to a restricted process.
// This does not rely on that covering every interface that can change a mount's
// attributes: without CAP_SYS_ADMIN the target cannot change any of them.
//
// The bounding set limits what exec grants and leaves this thread's own
// capabilities alone, so the supervisor can still clone the target into its
// mount namespace. Per thread, like Landlock, so the caller must hold the OS
// thread it forks from.
func dropSysAdminFromBoundingSet() error {
	_, _, errno := syscall.RawSyscall6(prctlSyscall(), prCapBSetDrop, capSysAdmin, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
