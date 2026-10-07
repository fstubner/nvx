//go:build windows

package nvx

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// watchDotenvFiles hides dotenv files from the sandbox as they appear in the
// project, for as long as a contained process runs. The launch protects the
// files present at launch. A dev server, an MCP server or a shell can run for
// hours, and a .env created in that time, or one an editor or git replaces by
// renaming a new file over it, inherits the project's permissions again. The
// contained process could read it until the next launch.
//
// It watches scope and its subdirectories for names added or renamed in. A
// dotenv file outside dotenvSkipDirs is protected, and a directory moved in is
// searched like the project is at launch. When Windows reports that changes were
// lost, the whole project is searched again.
//
// Protection is by permissions, which Windows checks when a file is opened. A
// contained process that creates a .env itself keeps the handle it created it
// with and can finish writing it, but cannot open it again afterwards. macOS
// refuses the create itself.
//
// The watch reacts to a file that already exists, so a new .env is open to the
// sandbox for milliseconds. Measured 2026-10-07 on Windows 11 26300, a
// contained node process polling for the file read it in ten of ten trials. The
// launch scan has no such gap, as it runs before the contained process does.
// docs/enforcement-matrix.md note 15 has the numbers.
//
// Best effort, like the launch. If the watch cannot start, the run goes ahead
// with one line saying so. stop ends the watch and returns once nothing of it
// is left running.
func watchDotenvFiles(nvxHome, scope string) (stop func()) {
	w, err := startDotenvWatch(nvxHome, scope)
	if err != nil {
		LogWarn("Could not watch %s for new .env files, so one created during this run stays readable in the sandbox until the next launch: %v", scope, err)
		return func() {}
	}
	go w.run()
	return w.stop
}

// dotenvWatchMask asks for files and directories being created, deleted or
// renamed. Only the creations and the new names of renames are acted on.
const dotenvWatchMask = syscall.FILE_NOTIFY_CHANGE_FILE_NAME | syscall.FILE_NOTIFY_CHANGE_DIR_NAME

// errorNotifyEnumDir is how Windows says it could not keep up and the changes
// since the last read are lost. A read that returns no bytes says the same.
const errorNotifyEnumDir = syscall.Errno(1022)

var (
	procCreateEventW        = modKernel32.NewProc("CreateEventW")
	procResetEvent          = modKernel32.NewProc("ResetEvent")
	procGetOverlappedResult = modKernel32.NewProc("GetOverlappedResult")
)

type dotenvWatch struct {
	nvxHome, scope string
	dir, event     syscall.Handle
	ov             syscall.Overlapped
	buf            []byte
	done           chan struct{}

	// recorded is set once the project's record is full (see
	// maxProtectedDotenv): the files that have a record, by dotenvKey. Only
	// those are looked at from then on.
	recorded map[string]bool

	// mu orders issuing a read against stop's cancel. Without it a read issued
	// just after the cancel would wait for good, and stop with it.
	mu      sync.Mutex
	stopped bool
}

// startDotenvWatch opens scope and issues the first read, so changes made once
// it returns are reported.
func startDotenvWatch(nvxHome, scope string) (*dotenvWatch, error) {
	p, err := syscall.UTF16PtrFromString(scope)
	if err != nil {
		return nil, err
	}
	dir, err := syscall.CreateFile(p, syscall.FILE_LIST_DIRECTORY,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	ev, _, e := procCreateEventW.Call(0, 1, 0, 0) // manual reset, not signalled
	if ev == 0 {
		syscall.CloseHandle(dir)
		return nil, e
	}
	w := &dotenvWatch{
		nvxHome: nvxHome, scope: scope,
		dir: dir, event: syscall.Handle(ev),
		buf:  make([]byte, 64*1024), // the most a read over the network may ask for
		done: make(chan struct{}),
	}
	if err := w.read(); err != nil {
		syscall.CloseHandle(w.event)
		syscall.CloseHandle(w.dir)
		return nil, err
	}
	return w, nil
}

// read issues one asynchronous read of the changes. The caller holds mu, or
// is the only one using w.
func (w *dotenvWatch) read() error {
	procResetEvent.Call(uintptr(w.event))
	w.ov = syscall.Overlapped{HEvent: w.event}
	return syscall.ReadDirectoryChanges(w.dir, &w.buf[0], uint32(len(w.buf)), true, dotenvWatchMask, nil, &w.ov, 0)
}

func (w *dotenvWatch) run() {
	defer close(w.done)
	for {
		var n uint32
		var err error
		if r, _, e := procGetOverlappedResult.Call(uintptr(w.dir), uintptr(unsafe.Pointer(&w.ov)),
			uintptr(unsafe.Pointer(&n)), 1); r == 0 {
			err = e
		}
		w.mu.Lock()
		stopped := w.stopped
		w.mu.Unlock()
		if stopped {
			return
		}
		switch {
		case errors.Is(err, errorNotifyEnumDir) || (err == nil && n == 0):
			found, _ := findDotenvFiles(w.scope, dotenvScanLimit)
			w.protect(found)
		case err != nil:
			LogWarn("Stopped watching %s for new .env files, so one created during the rest of this run stays readable in the sandbox until the next launch: %v", w.scope, err)
			return
		default:
			w.protect(w.dotenvFilesIn(w.buf[:n]))
		}

		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		err = w.read()
		w.mu.Unlock()
		if err != nil {
			LogWarn("Stopped watching %s for new .env files, so one created during the rest of this run stays readable in the sandbox until the next launch: %v", w.scope, err)
			return
		}
	}
}

// stop cancels the pending read, waits for run to return, and closes the
// handles. The read has finished by then, so Windows no longer writes into
// w.ov or w.buf.
func (w *dotenvWatch) stop() {
	w.mu.Lock()
	w.stopped = true
	syscall.CancelIoEx(w.dir, &w.ov)
	w.mu.Unlock()
	<-w.done
	syscall.CloseHandle(w.event)
	syscall.CloseHandle(w.dir)
}

// protect hides found from the sandbox. Once the project's record is full it
// warns, once, and from then on looks only at files that already have a record,
// which an editor or git may still replace. A contained process creating files
// by the thousand then costs nothing further, and nothing more is written.
func (w *dotenvWatch) protect(found []string) {
	if w.recorded != nil {
		found = slices.DeleteFunc(found, func(p string) bool { return !w.recorded[dotenvKey(p)] })
	}
	if !protectDotenvFiles(w.nvxHome, w.scope, found) || w.recorded != nil {
		return
	}
	warnDotenvCap(w.scope)
	w.recorded = map[string]bool{}
	for _, r := range loadProjectGrants(w.nvxHome, w.scope).ProtectedDotenv {
		w.recorded[dotenvKey(r.Path)] = true
	}
}

// dotenvKey is how a path is compared with a recorded one: the way
// sameGrantPath does, with the case folded so it can be a map key.
func dotenvKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

// dotenvFilesIn returns the dotenv files a batch of change records names: the
// files added or renamed in, and those under a directory added or renamed in.
func (w *dotenvWatch) dotenvFilesIn(records []byte) []string {
	var found []string
	for off := 0; off+12 <= len(records); {
		rec := (*syscall.FileNotifyInformation)(unsafe.Pointer(&records[off]))
		if rec.Action == syscall.FILE_ACTION_ADDED || rec.Action == syscall.FILE_ACTION_RENAMED_NEW_NAME {
			name := unsafe.Slice(&rec.FileName, rec.FileNameLength/2)
			found = append(found, w.dotenvFilesAt(syscall.UTF16ToString(name))...)
		}
		if rec.NextEntryOffset == 0 {
			break
		}
		off += int(rec.NextEntryOffset)
	}
	return found
}

// dotenvFilesAt returns the dotenv files at rel, a path relative to the
// project: rel itself, or what findDotenvFiles finds under it if it is a
// directory. Nothing under dotenvSkipDirs, and nothing through a link.
func (w *dotenvWatch) dotenvFilesAt(rel string) []string {
	parts := strings.Split(rel, `\`)
	for _, p := range parts {
		if slices.Contains(dotenvSkipDirs, p) {
			return nil
		}
	}
	path := filepath.Join(w.scope, rel)
	info, err := os.Lstat(path)
	if err != nil {
		return nil // gone again
	}
	if info.IsDir() {
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return nil
		}
		found, complete := findDotenvFiles(path, dotenvScanLimit)
		if !complete {
			LogWarn("Stopped looking for .env files after %d entries under %s; any further down stay readable in the sandbox.", dotenvScanLimit, path)
		}
		return found
	}
	if isDotenvName(parts[len(parts)-1]) {
		return []string{path}
	}
	return nil
}
