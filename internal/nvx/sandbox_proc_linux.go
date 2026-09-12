//go:build linux

package nvx

import (
	"fmt"
	"syscall"
)

// mountPrivateProc gives the sandbox a procfs of its own, showing only the
// processes inside it.
//
// Bun needs /proc. Measured on Linux with nvx's own sandbox: `bun --version`
// and a `bun -e` one-liner work, while `bun script.js` aborts and `bun install`
// reports "JSON document is too deeply nested" and "StackOverflow" against a
// 65-byte package.json that parses fine a second later outside. Every read
// under /proc inside the sandbox returned EACCES -- /proc/self/limits,
// /proc/self/maps, /proc/self/status, all of them -- because the Landlock
// ruleset grants nothing there. Node does not read them and was unaffected,
// which is why this went unnoticed.
//
// The grant has to come with this mount, not without it. Nothing remounted
// /proc, so the one visible inside the sandbox was the HOST's, listing every
// process on the machine: /proc/<pid>/cmdline is world-readable and
// /proc/<pid>/environ is readable for the user's own processes, which is where
// credentials live. Granting that would have handed contained code the
// environment of every other program the user is running -- the opposite of
// what this sandbox is for.
//
// So: a new mount namespace for the supervisor, mounts made private so nothing
// propagates back to the host, and a fresh procfs. The supervisor is PID 1 of
// its own PID namespace, so this procfs contains the supervisor, the target and
// the target's children, and nothing else. The target inherits the mount
// through its own CLONE_NEWNS copy.
//
// Called before landlock_restrict_self, because afterwards the supervisor is
// itself restricted. Reaping is unaffected: it uses wait4, never /proc, which
// sandbox_reap_linux.go documents as a deliberate choice.
func mountPrivateProc() error {
	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		return fmt.Errorf("unshare mount namespace: %w", err)
	}
	// Recursively private first: without it the procfs mount below can propagate
	// out to the host's mount table, which is a change to the user's machine
	// that outlives the run.
	if err := syscall.Mount("none", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}
	flags := uintptr(syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC)
	if err := syscall.Mount("proc", "/proc", "proc", flags, ""); err != nil {
		return fmt.Errorf("mount proc: %w", err)
	}
	return nil
}
