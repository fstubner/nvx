//go:build linux

package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// The contained process's filesystem view on Linux holds only what Landlock
// grants. Everything else on the host is absent from it.
//
// Landlock decides reads, writes and execution, but below ABI v9 it has no say
// over connect() to a pathname UNIX socket, and seccomp cannot filter that by
// path. Measured on WSL2 Ubuntu 24.04, kernel 6.18, a contained process
// connected to a host-created socket outside the project, and got HTTP 200 from
// /var/run/docker.sock, while its writes outside the project were refused. A
// socket that cannot be looked up cannot be connected to, so the view is built
// from the Landlock allowlist and nothing else.
//
// Hiding a list of well-known socket directories (/run, $XDG_RUNTIME_DIR) would
// be a smaller change. Sockets also live in /tmp (X11, SSH agents) and under the
// home directory (Docker Desktop), so a denylist would leave some of them
// reachable, and a new one appearing anywhere else would be reachable by
// default.

// openModeResolverPaths are the host resolver sockets kept in the view when the
// network is open. glibc's nss-resolve and nscd answer lookups over sockets
// here, and open mode has no proxy to resolve names on its behalf. In every
// other mode they stay hidden, because a lookup through the host resolver is a
// way out of a namespace that is meant to have none.
var openModeResolverPaths = []string{"/run/systemd/resolve", "/run/nscd"}

// sandboxVisiblePaths is every path the contained process's view is built
// from: the writable roots, the extra read/execute roots and the read-only
// roots, which are the paths applyLandlockSandbox grants.
func sandboxVisiblePaths(guestHome, workDir, nvxHome string, readExecRoots []string, privateProc bool, networkMode string) []string {
	paths := append([]string{}, sandboxWritableRoots(guestHome, workDir)...)
	paths = append(paths, readExecRoots...)
	for _, rule := range landlockReadOnlyRules(nvxHome, privateProc) {
		paths = append(paths, rule.path)
	}
	if strings.EqualFold(strings.TrimSpace(networkMode), "open") {
		paths = append(paths, openModeResolverPaths...)
	}
	return paths
}

// sandboxBind is one bind mount into the sandbox's root: src on the host,
// shown at dst inside.
type sandboxBind struct {
	src, dst string
	dir      bool
}

// sandboxBindPlan turns paths into the bind mounts that show them, in the order
// to make them.
//
// A path that goes through a symlink is shown at both its own name and its
// target, so /lib still works where it links to usr/lib, and so does a project
// reached through a symlinked parent. A path already inside another one is
// dropped, because the outer bind shows it already. Missing paths are skipped,
// as landlockReadOnlyRules skips them.
func sandboxBindPlan(paths []string) []sandboxBind {
	var binds []sandboxBind
	for _, p := range paths {
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		binds = append(binds, sandboxBind{src: resolved, dst: resolved, dir: info.IsDir()})
		if resolved != p {
			binds = append(binds, sandboxBind{src: resolved, dst: p, dir: info.IsDir()})
		}
	}

	// A parent sorts before anything beneath it, so the outer bind is kept.
	sort.Slice(binds, func(i, j int) bool { return binds[i].dst < binds[j].dst })
	var plan []sandboxBind
	for _, b := range binds {
		covered := false
		for _, kept := range plan {
			if dirWithin(b.dst, kept.dst) {
				covered = true
				break
			}
		}
		if !covered {
			plan = append(plan, b)
		}
	}
	return plan
}

// sandboxRootScratch is where the new root is assembled before it replaces
// "/". Any directory would do. The mount is private to this namespace, and
// every bind source is opened before it covers anything.
const sandboxRootScratch = "/tmp"

// sandboxTmpDir is the directory the sandbox shows as /tmp. It is the guest home's
// own tmp directory, the one $TMPDIR names. A tool that hard-codes /tmp, such as pnpm
// 12 for its store locks, used to fail on mkdir("/tmp"), because the sandbox root
// had no such directory. Showing the directory $TMPDIR already names keeps the two
// paths the same place, and the host's /tmp is never shown. It goes with the guest
// home. Returns "" when the guest home has no tmp directory.
//
// A link is not a directory here. The contained process can write the whole guest
// home, and a trusted tool's guest home outlives the run, so an earlier run can
// leave tmp as a link to a directory the sandbox may read and not write, such as
// nvx's runtimes. nvx opens the path with its own rights, and following the link
// would show that directory at /tmp and grant it in full.
func sandboxTmpDir(guestHome string) string {
	if guestHome == "" {
		return ""
	}
	dir := filepath.Join(guestHome, "tmp")
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// openDirNoFollow opens a directory for a bind or a rule without following a link
// at its last component. The check in sandboxTmpDir is one moment and this is
// another, and a run that shares the guest home can swap the directory between
// them. A link fails here.
func openDirNoFollow(path string) (int, error) {
	return syscall.Open(path, openPathFlag|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
}

// enterSandboxRoot makes the calling thread's root a tmpfs holding only the
// binds in plan and the directory tmp at /tmp, and detaches the host's root from
// its mount namespace. An empty tmp leaves the root without a /tmp.
//
// It runs in the supervisor on its locked thread, before Landlock, so the
// target inherits the view through its own CLONE_NEWNS copy. The target cannot
// undo it, because a Landlock-restricted process may not mount, unmount or
// pivot_root.
// The supervisor's other threads keep the host view, which is where the egress
// and --connect relays dial from.
func enterSandboxRoot(plan []sandboxBind, tmp string) error {
	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		return fmt.Errorf("unshare mount namespace: %w", err)
	}
	if err := syscall.Mount("none", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}

	// Opened first, because the scratch tmpfs may cover a source (a project in
	// /tmp). The binds below go through /proc/self/fd, which names the opened
	// directory whatever now sits over its path.
	fds := make([]int, 0, len(plan))
	defer func() {
		for _, fd := range fds {
			_ = syscall.Close(fd)
		}
	}()
	for _, b := range plan {
		fd, err := syscall.Open(b.src, openPathFlag|syscall.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open %s: %w", b.src, err)
		}
		fds = append(fds, fd)
	}
	tmpFD := -1
	if tmp != "" {
		fd, err := openDirNoFollow(tmp)
		if err != nil {
			return fmt.Errorf("open %s: %w", tmp, err)
		}
		fds = append(fds, fd)
		tmpFD = fd
	}

	flags := uintptr(syscall.MS_NOSUID | syscall.MS_NODEV)
	if err := syscall.Mount("tmpfs", sandboxRootScratch, "tmpfs", flags, "mode=0755"); err != nil {
		return fmt.Errorf("mount the sandbox root: %w", err)
	}
	// Before the binds below, so a project that lives under /tmp is mounted over
	// it and not hidden by it.
	if tmpFD >= 0 {
		target := filepath.Join(sandboxRootScratch, "/tmp")
		if err := makeMountPoint(target, true); err != nil {
			return fmt.Errorf("mount point for /tmp: %w", err)
		}
		src := "/proc/self/fd/" + strconv.Itoa(tmpFD)
		if err := syscall.Mount(src, target, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			return fmt.Errorf("bind %s at /tmp: %w", tmp, err)
		}
	}
	for i, b := range plan {
		target := filepath.Join(sandboxRootScratch, b.dst)
		if err := makeMountPoint(target, b.dir); err != nil {
			return fmt.Errorf("mount point for %s: %w", b.dst, err)
		}
		src := "/proc/self/fd/" + strconv.Itoa(fds[i])
		if err := syscall.Mount(src, target, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			return fmt.Errorf("bind %s: %w", b.dst, err)
		}
	}

	// pivot_root(".", ".") stacks the old root on the new one, and the detach
	// removes it. See pivot_root(2).
	if err := syscall.Chdir(sandboxRootScratch); err != nil {
		return fmt.Errorf("enter the sandbox root: %w", err)
	}
	if err := syscall.PivotRoot(".", "."); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	if err := syscall.Unmount(".", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("detach the host root: %w", err)
	}
	return syscall.Chdir("/")
}

// makeMountPoint creates an empty directory or file at path for a bind to land
// on, with any parents.
func makeMountPoint(path string, dir bool) error {
	if dir {
		return os.MkdirAll(path, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
