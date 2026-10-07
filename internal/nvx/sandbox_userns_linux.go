//go:build linux

package nvx

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// prCapBSetRead is PR_CAPBSET_READ from linux/prctl.h.
const prCapBSetRead = 23

// supervisorCapabilities lists every capability the running kernel knows. The
// supervisor carries them across its own exec as ambient capabilities, because
// it is not root in its user namespace and exec would otherwise clear them (see
// supervisorSysProcAttr). Root held all of them, and the setup uses several:
// CAP_SYS_ADMIN for the mounts, CAP_SYS_CHROOT with it for the .env watcher's
// setns, CAP_SYS_RESOURCE for the user-namespace limit, CAP_SETPCAP for the
// bounding-set drops and CAP_NET_ADMIN for `ip` and `iptables`, which get them
// as ambient capabilities too.
//
// PR_CAPBSET_READ fails with EINVAL past the kernel's last capability. Raising
// one the kernel does not know would fail the launch.
func supervisorCapabilities() []uintptr {
	var caps []uintptr
	for c := uintptr(0); c < 64; c++ {
		if _, _, errno := syscall.RawSyscall6(prctlSyscall(), prCapBSetRead, c, 0, 0, 0, 0); errno == syscall.EINVAL {
			break
		}
		caps = append(caps, c)
	}
	return caps
}

// linuxCapabilityVersion3 is _LINUX_CAPABILITY_VERSION_3, which takes two data
// structs, one per 32 capabilities.
const linuxCapabilityVersion3 = 0x20080522

// capUserHeader and capUserData are struct __user_cap_header_struct and struct
// __user_cap_data_struct from linux/capability.h.
type capUserHeader struct {
	version uint32
	pid     int32
}

type capUserData struct {
	effective   uint32
	permitted   uint32
	inheritable uint32
}

// dropTargetCapabilities leaves the target no capability to gain at exec. Call
// it on the thread that forks the target, after the last step that needs a
// capability to pass to a child.
//
// The target runs as the user, so exec grants it nothing by itself. Two ways
// remain, and this closes both. The ambient set crosses exec to a process that
// is not root, so the inheritable set is cleared, and the kernel lowers the
// ambient set with it. The bounding set limits what exec grants to root and to
// a file with capabilities, so it is emptied. That covers a user who runs nvx
// as root, who is root in the namespace too, and a target that is itself a file
// with capabilities, which no_new_privs would let keep what this thread holds.
// Measured 2026-10-07 without this, the target held all 41 capabilities, the
// four the bounding set had dropped included.
//
// Per thread, like Landlock. The .env watcher's thread keeps what it needs for
// its setns and mounts.
func dropTargetCapabilities() error {
	hdr := capUserHeader{version: linuxCapabilityVersion3}
	var data [2]capUserData
	if _, _, errno := syscall.RawSyscall(syscall.SYS_CAPGET,
		uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); errno != 0 {
		return fmt.Errorf("capget: %w", errno)
	}
	data[0].inheritable, data[1].inheritable = 0, 0
	if _, _, errno := syscall.RawSyscall(syscall.SYS_CAPSET,
		uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); errno != 0 {
		return fmt.Errorf("capset: %w", errno)
	}
	for _, c := range supervisorCapabilities() {
		if err := dropFromBoundingSet(c); err != nil {
			return fmt.Errorf("drop capability %d from the bounding set: %w", c, err)
		}
	}
	return nil
}

// maxUserNamespacesPath is the per-user-namespace ceiling on how many user
// namespaces may be created beneath this one (the UCOUNT_USER_NAMESPACES limit,
// linux/user_namespace.h). Writing 0 stops the contained process creating one.
const maxUserNamespacesPath = "/proc/sys/user/max_user_namespaces"

// capSysResource is CAP_SYS_RESOURCE from linux/capability.h. It is what writing
// maxUserNamespacesPath needs, so the target leaves it behind with the other
// capabilities and cannot raise the limit back.
const capSysResource = 24

// denyNestedUserNamespaces stops the contained process from creating a user
// namespace of its own.
//
// Without it the run-time .env watcher can be walked around. The target holds no
// CAP_SYS_ADMIN (maskDotenvFiles drops it), so it cannot unshare a mount
// namespace on its own. But unshare(CLONE_NEWUSER) needs no privilege, and a new
// user namespace grants full capabilities inside it, CAP_SYS_ADMIN included, over
// a mount namespace created in the same call. So unshare(CLONE_NEWUSER|
// CLONE_NEWNS) hands the target a private copy of the mount tree, frozen at that
// moment. The masks watchDotenvFiles mounts later, for .env files that appear
// during the run, land only in the target's original namespace and never reach
// the copy, so the file is read in the clear. Measured on WSL2 kernel 6.18
// before this: a .env created mid-run was read through such a nested namespace,
// while the launch-time .env stayed denied.
//
// The limit lives in the ucounts of the supervisor's own user namespace, which
// the target shares. Writing 0 makes every later unshare or clone asking for
// CLONE_NEWUSER from the target fail with ENOSPC. The kernel checks it in the
// user-namespace creation path itself, not at a syscall boundary, so it covers
// clone3 as well, which a seccomp clone-flag filter cannot inspect.
//
// Run before Landlock, which then keeps /proc read-only to the target so it
// cannot reopen the file to raise the limit, and CAP_SYS_RESOURCE leaves the
// bounding set so it could not write the file even if it could open it. Either
// alone holds; both are cheap. Fail-closed like the mounts and drops around it:
// a target that could still nest would read the run-time .env files this hides.
func denyNestedUserNamespaces() error {
	if err := os.WriteFile(maxUserNamespacesPath, []byte("0"), 0); err != nil {
		return fmt.Errorf("deny nested user namespaces: %w", err)
	}
	if err := dropFromBoundingSet(capSysResource); err != nil {
		return fmt.Errorf("drop CAP_SYS_RESOURCE for the sandboxed command: %w", err)
	}
	return nil
}
