//go:build linux

package nvx

import (
	"fmt"
	"os"
)

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
