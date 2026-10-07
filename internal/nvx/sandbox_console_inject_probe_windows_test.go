//go:build windows

package nvx

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// A contained process cannot type into the console nvx shares with the user's
// shell. This is the Windows analogue of TIOCSTI on Linux: a process that holds
// a handle to the console's input buffer can push key records into it with
// WriteConsoleInput, and after nvx exits the shell reads them as typed input, so
// a postinstall could leave a command there to run as the user outside the
// sandbox.
//
// The contained child shares nvx's console, because nvx creates it with neither
// DETACHED_PROCESS nor CREATE_NEW_CONSOLE (see appContainerCreationFlags). So
// the question is whether the AppContainer's access check lets it open a console
// input handle and write to it. This probe tries both doors a process has:
// CreateFile("CONIN$") and the inherited STD_INPUT_HANDLE. The parent then reads
// its own console input buffer back and fails if the marker arrived.
//
// NVX_PROBE=1 and a real console, like the other AppContainer probes. go test
// launched from a shell with no console (a bare pipe) cannot host it, and it
// skips rather than passing, so the assertion is never counted as met without
// running.

const consoleInjectMarker = "nvxZ" // short, and unlikely to be typed by accident

var (
	procWriteConsoleInputW      = modKernel32.NewProc("WriteConsoleInputW")
	procPeekConsoleInputW       = modKernel32.NewProc("PeekConsoleInputW")
	procFlushConsoleInputBuffer = modKernel32.NewProc("FlushConsoleInputBuffer")
	procGetConsoleModeInject    = modKernel32.NewProc("GetConsoleMode")
	procCreateFileWInject       = modKernel32.NewProc("CreateFileW")
	procAttachConsoleInject     = modKernel32.NewProc("AttachConsole")
	procFreeConsoleInject       = modKernel32.NewProc("FreeConsole")
)

const nvxConsoleInjectParentPID = "NVX_CONSOLE_INJECT_PPID"

// inputRecord is INPUT_RECORD with a KEY_EVENT_RECORD body, the 20-byte layout
// WriteConsoleInputW and PeekConsoleInputW read.
type inputRecord struct {
	EventType       uint16
	_               uint16
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

const keyEvent = 0x0001

func openConin() (syscall.Handle, error) {
	name, err := syscall.UTF16PtrFromString("CONIN$")
	if err != nil {
		return 0, err
	}
	const genericRead = 0x80000000
	const genericWrite = 0x40000000
	const fileShareRW = 0x3
	const openExisting = 3
	h, _, callErr := procCreateFileWInject.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(genericRead|genericWrite),
		uintptr(fileShareRW),
		0,
		uintptr(openExisting),
		0, 0,
	)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return 0, callErr
	}
	return syscall.Handle(h), nil
}

// consoleHasInput reports whether h is a usable console input handle.
func consoleHasInput(h syscall.Handle) bool {
	var mode uint32
	r, _, _ := procGetConsoleModeInject.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}

// injectString writes s into the console input buffer h, one key-down record per
// rune. Returns the number written and the first error.
func injectString(h syscall.Handle, s string) (uint32, error) {
	recs := make([]inputRecord, 0, len(s))
	for _, r := range s {
		recs = append(recs, inputRecord{EventType: keyEvent, KeyDown: 1, RepeatCount: 1, UnicodeChar: uint16(r)})
	}
	var written uint32
	ret, _, callErr := procWriteConsoleInputW.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&recs[0])),
		uintptr(len(recs)),
		uintptr(unsafe.Pointer(&written)),
	)
	if ret == 0 {
		return 0, callErr
	}
	return written, nil
}

// consoleInjectChild runs inside the AppContainer and tries to type the marker.
func consoleInjectChild() int {
	report := func(door, result string) { fmt.Printf("%s=%s\n", door, result) }

	if h, err := openConin(); err != nil {
		report("CONIN_OPEN", "err:"+errText(err))
	} else {
		report("CONIN_OPEN", "ok")
		if _, werr := injectString(h, consoleInjectMarker); werr != nil {
			report("CONIN_WRITE", "err:"+errText(werr))
		} else {
			report("CONIN_WRITE", "ok")
		}
		_ = syscall.CloseHandle(h)
	}

	stdin, _ := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if stdin == 0 || stdin == syscall.InvalidHandle || !consoleHasInput(stdin) {
		report("STDIN_WRITE", "not-a-console")
	} else if _, werr := injectString(stdin, consoleInjectMarker); werr != nil {
		report("STDIN_WRITE", "err:"+errText(werr))
	} else {
		report("STDIN_WRITE", "ok")
	}

	// The third door: detach and attach to the parent's console by pid, the way
	// a process with no console of its own would reach one.
	ppid := os.Getenv(nvxConsoleInjectParentPID)
	procFreeConsoleInject.Call()
	n, perr := strconv.Atoi(ppid)
	if perr != nil {
		report("ATTACH", "no-ppid")
		return 0
	}
	if r, _, aerr := procAttachConsoleInject.Call(uintptr(n)); r == 0 {
		report("ATTACH", "err:"+errText(aerr))
		return 0
	}
	report("ATTACH", "ok")
	if h, err := openConin(); err != nil {
		report("ATTACH_WRITE", "open-err:"+errText(err))
	} else if _, werr := injectString(h, consoleInjectMarker); werr != nil {
		report("ATTACH_WRITE", "err:"+errText(werr))
		_ = syscall.CloseHandle(h)
	} else {
		report("ATTACH_WRITE", "ok")
		_ = syscall.CloseHandle(h)
	}
	return 0
}

func errText(err error) string {
	return strings.ReplaceAll(strings.TrimSpace(err.Error()), " ", "_")
}

// peekConsole returns the printable characters currently in the console input
// buffer h, read from its key-down records without removing them.
func peekConsole(h syscall.Handle) string {
	var recs [256]inputRecord
	var n uint32
	ret, _, _ := procPeekConsoleInputW.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(&recs[0])),
		uintptr(len(recs)),
		uintptr(unsafe.Pointer(&n)),
	)
	if ret == 0 {
		return ""
	}
	var b strings.Builder
	for i := uint32(0); i < n; i++ {
		r := recs[i]
		if r.EventType == keyEvent && r.KeyDown != 0 && r.UnicodeChar != 0 {
			b.WriteRune(rune(r.UnicodeChar))
		}
	}
	return b.String()
}

func TestAContainedProcessCannotInjectConsoleInput(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (launches a real AppContainer)")
	}
	if os.Getenv("NVX_CONSOLE_INJECT_CHILD") == "1" {
		os.Exit(consoleInjectChild())
	}

	// A console in the parent lets the test read the input buffer back, the
	// strongest confirmation. It is not required: the refusal of the contained
	// WriteConsoleInput is asserted from the child's own report, which holds
	// whether or not the parent can read back. A parent console is flushed first
	// so anything found afterward was injected during this run.
	parentConin, perr := openConin()
	haveParentConsole := perr == nil && consoleHasInput(parentConin)
	if parentConin != 0 && parentConin != syscall.InvalidHandle {
		defer syscall.CloseHandle(parentConin)
	}
	if haveParentConsole {
		procFlushConsoleInputBuffer.Call(uintptr(parentConin))
	}

	const probeProfile = "nvx.sandbox.consoleinject"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	childExe := stageProbeChild(t, guestHome, "consoleprobe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome), "NVX_PROBE=1", "NVX_CONSOLE_INJECT_CHILD=1",
		nvxConsoleInjectParentPID+"="+strconv.Itoa(os.Getpid()))
	exitCode, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestAContainedProcessCannotInjectConsoleInput"},
		env, workDir, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readProbeOutput(t, read)

	t.Logf("child exit=%d err=%v", exitCode, launchErr)
	t.Logf("child output:\n%s", strings.TrimSpace(out))
	requireContainedRunLaunched(t, out)
	requireAppContainerLaunch(t, launchErr)

	// A WriteConsoleInput that returned success injected the records, whether or
	// not this process can read them back. So any door reporting ok is a failure
	// on its own.
	if strings.Contains(out, "CONIN_WRITE=ok") ||
		strings.Contains(out, "STDIN_WRITE=ok") ||
		strings.Contains(out, "ATTACH_WRITE=ok") {
		t.Fatalf("a contained process's WriteConsoleInput succeeded: it typed %q into a console it shares with "+
			"the user's shell, so a postinstall could leave a command to run outside the sandbox.\nchild:\n%s",
			consoleInjectMarker, strings.TrimSpace(out))
	}

	// When this process has a console, confirm the marker did not reach the
	// buffer even if a write reported an error. PeekConsoleInput reads back
	// exactly what a successful WriteConsoleInput plants (proved in the scratch
	// round-trip during development), so an empty buffer is a real negative.
	if haveParentConsole {
		if landed := peekConsole(parentConin); strings.Contains(landed, consoleInjectMarker) {
			t.Fatalf("the injection marker %q reached nvx's console input buffer; a contained process typed into it.\nchild:\n%s",
				consoleInjectMarker, strings.TrimSpace(out))
		}
	}

	// The assertion means something only if the child actually reached a console
	// input handle to be refused on. If it could not, and this process has none
	// either, there was no console in this environment and nothing was tested.
	childReachedAConsole := strings.Contains(out, "CONIN_OPEN=ok") ||
		strings.Contains(out, "ATTACH=ok")
	if !childReachedAConsole && !haveParentConsole {
		t.Skip("no Windows console in this environment, so console input injection could not be exercised")
	}
	t.Logf("RESULT: the contained process could not type into the console. %s", strings.Join(strings.Fields(out), " "))
}
