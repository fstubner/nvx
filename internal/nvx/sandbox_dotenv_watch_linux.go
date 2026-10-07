//go:build linux

package nvx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// dotenvRescanInterval is how often the watcher re-searches the whole project
// once the per-user inotify watch limit (fs.inotify.max_user_watches) is
// exhausted and a directory can no longer be watched. Until that happens inotify
// reports every new file and no rescan runs. After it, a new .env in an
// unwatched folder raises no event, so the watcher re-walks the project on this
// interval and masks what it finds, which bounds how long such a file stays
// readable. The walk is the same bounded one the overflow path runs (capped at
// dotenvScanLimit entries). Measured 2026-10-07 in a golang:1.23 container on
// WSL2 kernel 6.18, with the watch limit exhausted so each watch add fails fast:
// one rescan of a 5,400-entry project took about 16 ms, run once every 2 s, so
// the watcher stays idle almost all the time.
const dotenvRescanInterval = 2 * time.Second

// watchDotenvFiles covers the dotenv files that appear during a run, which
// maskDotenvFiles cannot see at launch. Long-running contained processes (dev
// servers, MCP servers) meet two cases:
//
//   - A file created after launch, from inside the sandbox or outside it.
//   - A covered file that an editor or git replaces from outside. Since Linux
//     3.18 a rename over a file that is a mount point in another mount
//     namespace succeeds, and the kernel detaches that mount, so the new file
//     is uncovered.
//
// It runs on a thread of its own, which joins the target's mount namespace.
// The target got a copy of the supervisor's at clone time (applyLinuxNamespaces),
// so a mount the supervisor's own thread made later would not reach it. This
// thread is outside Landlock, which binds only the thread that called it, and
// it holds CAP_SYS_ADMIN in the user namespace that owns the target's mounts.
// The bounding-set drops do not touch it. They are per thread, and they only
// limit what an exec grants.
//
// inotify reports each new name in the project's directories. A dotenv name
// gets the mask mounted over it, read-only, everywhere the sandbox shows it. A
// new directory is watched and then searched, because files can land in it
// before its watch exists. When the event queue overflows, the whole project is
// searched again.
//
// A process that holds the file open keeps that descriptor, so a contained
// process that creates a .env itself can go on using it. Once the mask lands it
// cannot open the file again, rename it or delete it (EBUSY). A process that
// reads a new file before its mask lands still sees it.
//
// The thread is never unlocked, so it ends with the goroutine and its changed
// namespace goes with it. It stops when the target exits.
func watchDotenvFiles(w *dotenvWatcher) {
	runtime.LockOSThread()
	defer w.close()
	if err := w.join(); err != nil {
		// ESRCH means the target has already exited, and there is nothing to watch.
		if !errors.Is(err, syscall.ESRCH) {
			warnDotenvWatch(err)
		}
		return
	}
	w.walk(w.workDir)
	w.run()
}

// startDotenvWatcher starts watchDotenvFiles for the target pid. Call it on the
// thread that started the target, before anything reaps it, so the pidfd names
// that process and no later one with the same pid.
//
// mask is the file the watcher mounts (see createDotenvWatchMask). covered
// holds every mask in use, so a file already showing one is left alone.
func startDotenvWatcher(pid int, workDir, mask string, covered []os.FileInfo, plan []sandboxBind) {
	pidfd, _, errno := syscall.RawSyscall(sysPidfdOpen, uintptr(pid), 0, 0)
	if errno != 0 {
		warnDotenvWatch(fmt.Errorf("pidfd_open: %w", errno))
		return
	}
	go watchDotenvFiles(&dotenvWatcher{
		pidfd:   int(pidfd),
		workDir: workDir,
		mask:    mask,
		covered: covered,
		plan:    plan,
		inotify: -1,
		epoll:   -1,
		dirs:    map[int32]string{},
	})
}

func warnDotenvWatch(err error) {
	LogWarn("Could not watch this project for new .env files (%v); one created or replaced during this run stays readable in the sandbox.", err)
}

// sysPidfdOpen is pidfd_open(2), which has this number on every architecture.
// Linux 5.8 added setns on a pidfd. Landlock already needs 5.13.
const sysPidfdOpen = 434

// dotenvWatchEvents are the inotify events that put a new name in a directory.
// IN_ONLYDIR and IN_DONT_FOLLOW keep a watch on the directory that was found,
// even if the name is swapped for a link before the watch is added.
const dotenvWatchEvents = syscall.IN_CREATE | syscall.IN_MOVED_TO | syscall.IN_ONLYDIR | syscall.IN_DONT_FOLLOW

type dotenvWatcher struct {
	pidfd   int
	workDir string
	mask    string
	covered []os.FileInfo
	plan    []sandboxBind

	inotify, epoll int
	// dirs maps each watch to its directory. Adding a watch for a directory
	// already watched returns the same descriptor, so a directory renamed within
	// the project gets its new path when the walk reaches it.
	dirs       map[int32]string
	warnedFull bool
}

// join moves the calling thread into the target's mount namespace and opens
// the inotify and epoll descriptors.
func (w *dotenvWatcher) join() error {
	// setns into a mount namespace fails with EINVAL while the thread shares its
	// root and working directory with others, and Go starts every thread with
	// CLONE_FS.
	if err := syscall.Unshare(syscall.CLONE_FS); err != nil {
		return fmt.Errorf("unshare CLONE_FS: %w", err)
	}
	if _, _, errno := syscall.RawSyscall(setnsSyscall(), uintptr(w.pidfd), syscall.CLONE_NEWNS, 0); errno != 0 {
		return fmt.Errorf("join the sandbox's mount namespace: %w", errno)
	}
	var err error
	if w.inotify, err = syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK); err != nil {
		return fmt.Errorf("inotify_init1: %w", err)
	}
	if w.epoll, err = syscall.EpollCreate1(syscall.EPOLL_CLOEXEC); err != nil {
		return fmt.Errorf("epoll_create1: %w", err)
	}
	for _, fd := range []int{w.inotify, w.pidfd} {
		// #nosec G115 -- a file descriptor fits in int32
		ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(fd)}
		if err := syscall.EpollCtl(w.epoll, syscall.EPOLL_CTL_ADD, fd, &ev); err != nil {
			return fmt.Errorf("epoll_ctl: %w", err)
		}
	}
	return nil
}

func (w *dotenvWatcher) close() {
	for _, fd := range []int{w.epoll, w.inotify, w.pidfd} {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
	}
	w.epoll, w.inotify, w.pidfd = -1, -1, -1
}

// run handles inotify events until the target exits, which makes its pidfd
// readable.
func (w *dotenvWatcher) run() {
	events := make([]syscall.EpollEvent, 2)
	buf := make([]byte, 64*1024)
	for {
		// Block forever until a directory could not be watched; from then on wake
		// every dotenvRescanInterval to re-search the project, because a file in an
		// unwatched folder raises no inotify event. The pidfd still makes EpollWait
		// return at once when the target exits, so the timeout only fires when idle.
		timeout := -1
		if w.warnedFull {
			timeout = int(dotenvRescanInterval.Milliseconds())
		}
		n, err := syscall.EpollWait(w.epoll, events, timeout)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return
		}
		if n == 0 {
			w.walk(w.workDir) // periodic rescan after watch exhaustion
			continue
		}
		for _, e := range events[:n] {
			if int(e.Fd) == w.pidfd {
				return
			}
		}
		w.drain(buf)
	}
}

// drain reads and handles every queued inotify event.
func (w *dotenvWatcher) drain(buf []byte) {
	for {
		n, err := syscall.Read(w.inotify, buf)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			return
		}
		for off := 0; off+syscall.SizeofInotifyEvent <= n; {
			ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
			start := off + syscall.SizeofInotifyEvent
			end := start + int(ev.Len)
			if end > n {
				return
			}
			w.handle(ev.Wd, ev.Mask, strings.TrimRight(string(buf[start:end]), "\x00"))
			off = end
		}
	}
}

func (w *dotenvWatcher) handle(wd int32, mask uint32, name string) {
	switch {
	case mask&syscall.IN_Q_OVERFLOW != 0:
		w.walk(w.workDir)
		return
	case mask&syscall.IN_IGNORED != 0:
		delete(w.dirs, wd)
		return
	}
	dir, ok := w.dirs[wd]
	if !ok || name == "" {
		return
	}
	p := filepath.Join(dir, name)
	switch {
	case mask&syscall.IN_ISDIR != 0:
		if !slices.Contains(dotenvSkipDirs, name) {
			w.walk(p)
		}
	case isDotenvName(name):
		w.cover(p)
	}
}

// walk watches root and every directory beneath it, leaving out
// dotenvSkipDirs as findDotenvFiles does, and covers each dotenv file it finds.
// Each directory is watched before it is read, so a file created in between is
// reported by one or the other. Like the search at launch, which says so when
// the project reaches it, it stops after dotenvScanLimit entries, so an
// overflow does not cost more than a launch.
func (w *dotenvWatcher) walk(root string) {
	queue := []string{root}
	seen := 0
	for len(queue) > 0 && seen <= dotenvScanLimit {
		dir := queue[0]
		queue = queue[1:]
		wd, err := syscall.InotifyAddWatch(w.inotify, dir, dotenvWatchEvents)
		switch {
		case err == nil:
			// #nosec G115 -- inotify returns watch descriptors as int32 in its events
			w.dirs[int32(wd)] = dir
		case errors.Is(err, syscall.ENOSPC) && !w.warnedFull:
			// The per-user watch limit (fs.inotify.max_user_watches). What is there
			// now is still covered below.
			w.warnedFull = true
			LogWarn("Could not watch every folder of this project for new .env files (%v); one created later in an unwatched folder stays readable in the sandbox.", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		seen += len(entries)
		for _, e := range entries {
			name := e.Name()
			switch {
			case e.IsDir():
				if !slices.Contains(dotenvSkipDirs, name) {
					queue = append(queue, filepath.Join(dir, name))
				}
			case isDotenvName(name):
				w.cover(filepath.Join(dir, name))
			}
		}
	}
}

// cover mounts the mask over the file at p, as dotenvMaskTargets picks files:
// links resolved, regular files only. A file that already shows a mask is left
// alone, so a second search does not stack mounts.
func (w *dotenvWatcher) cover(p string) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	for _, m := range w.covered {
		if m != nil && os.SameFile(info, m) {
			return
		}
	}
	for _, view := range sandboxViews(w.plan, resolved) {
		if err := bindMountReadOnlyFrom(w.mask, view); err != nil && !errors.Is(err, syscall.ENOENT) {
			LogWarn("Could not hide %s from the sandbox: %v", view, err)
		}
	}
}

// sandboxViews returns every path at which the sandbox shows the file at p,
// itself a path inside the sandbox. A directory reached through a symbolic link
// is bound at its own path and at the link's (see sandboxBindPlan), and a mount
// over the file in one of those leaves the other showing it. maskDotenvFiles
// needs none of this, because it mounts before those binds are made.
func sandboxViews(plan []sandboxBind, p string) []string {
	host := ""
	for _, b := range plan {
		if dirWithin(p, b.dst) {
			rel, err := filepath.Rel(b.dst, p)
			if err != nil {
				return nil
			}
			host = filepath.Join(b.src, rel)
			break
		}
	}
	if host == "" {
		return nil
	}
	var views []string
	for _, b := range plan {
		if dirWithin(host, b.src) {
			rel, err := filepath.Rel(b.src, host)
			if err != nil {
				continue
			}
			views = append(views, filepath.Join(b.dst, rel))
		}
	}
	return views
}

// createDotenvWatchMask creates the mask watchDotenvFiles mounts, on the
// sandbox's root, and makes it a read-only mount of itself. Called after
// enterSandboxRoot and before Landlock, on the supervisor's locked thread, so
// the target's copy of the namespace holds it at the same path.
//
// The read-only mount refuses a chmod (EROFS), which the file's owner could
// otherwise make whatever Landlock grants, and a rename or unlink (EBUSY). The
// root is a tmpfs that ends with the namespace. In the guest home the file
// would outlive the run, since a tool's guest home is kept between runs, a
// mount point cannot be removed in the namespace that holds it, and this
// thread cannot unmount anything once Landlock applies.
func createDotenvWatchMask() (string, os.FileInfo, error) {
	mask, err := createDotenvMask("/")
	if err != nil {
		return "", nil, err
	}
	if err := bindMountReadOnly(mask); err != nil {
		_ = os.Remove(mask)
		return "", nil, err
	}
	info, err := os.Stat(mask)
	if err != nil {
		return "", nil, fmt.Errorf("read the dotenv mask: %w", err)
	}
	return mask, info, nil
}
