//go:build windows

package nvx

// Probe (NVX_PROBE=1): can a contained process turn a file handle back into a
// drive-letter path?
//
// pnpm and bun both stop on this. pnpm's second install in a project fails
// with "EPERM realpath 'node_modules'" -- the realpath syscall, so libuv's
// native path, which opens the directory and asks GetFinalPathNameByHandle for
// its DOS name. bun 1.3.1 reports a bare ENOENT and bun 1.4.2 "An internal
// error occurred (EBADF)" on every install, and `bun -e` fails to read its own
// working directory. Node's own fs.realpathSync is a JavaScript walk with
// lstat, which is why npm never touches this.
//
// Measured 2026-09-17 inside a container holding the project capability, on
// the working directory it can read, write and list:
//
//	GetFinalPathNameByHandle VOLUME_NAME_DOS   Access is denied
//	GetFinalPathNameByHandle VOLUME_NAME_GUID  Access is denied
//	GetFinalPathNameByHandle VOLUME_NAME_NT    \Device\HarddiskVolume3\...
//	GetFinalPathNameByHandle VOLUME_NAME_NONE  \Users\...
//	QueryDosDevice C:                          Access is denied
//
// So the handle is fine and the NT name comes back; what is refused is the
// lookup of drive letters, which enumerates the DOS device directories in the
// object manager. The session's own \?? directory grants the user and nobody an
// AppContainer's second access check would accept, and adding the runtime
// capability to it, with query and traverse, changed nothing above: the
// letters live in \GLOBAL??, which grants Everyone and is owned by the system.
// Nothing an unelevated nvx writes can change that, and a change to \GLOBAL??
// does not survive a reboot.
//
// What this rules out is fixing pnpm's re-install and bun's install with
// another file permission. It is kept as the record of that, so the next
// person to see EBADF from bun does not spend a day on ACLs.

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

var (
	probeGetFinalPathNameByHandleW = modKernel32.NewProc("GetFinalPathNameByHandleW")
	probeQueryDosDeviceW           = modKernel32.NewProc("QueryDosDeviceW")
)

func TestProbeFinalPathNameInsideTheContainer(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}
	if os.Getenv("NVX_FINALPATH_CHILD") == "1" {
		for _, target := range []string{os.Getenv("NVX_PROBE_TARGET"), `C:\`} {
			p, _ := syscall.UTF16PtrFromString(target)
			h, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
				nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
			if err != nil {
				fmt.Printf("%s open=%v\n", target, err)
				continue
			}
			for _, f := range []struct {
				name string
				flag uintptr
			}{{"DOS", 0x0}, {"GUID", 0x1}, {"NT", 0x2}, {"NONE", 0x4}} {
				buf := make([]uint16, 1024)
				n, _, e := probeGetFinalPathNameByHandleW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), f.flag)
				if n == 0 {
					fmt.Printf("%s final(%s)=ERR %v\n", target, f.name, e)
				} else {
					fmt.Printf("%s final(%s)=%s\n", target, f.name, syscall.UTF16ToString(buf[:n]))
				}
			}
			syscall.CloseHandle(h)
		}
		buf := make([]uint16, 1024)
		dev, _ := syscall.UTF16PtrFromString("C:")
		n, _, e := probeQueryDosDeviceW.Call(uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 {
			fmt.Printf("QueryDosDevice(C:)=ERR %v\n", e)
		} else {
			fmt.Printf("QueryDosDevice(C:)=%s\n", syscall.UTF16ToString(buf[:n]))
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.finalpathprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}
	childExe := stageProbeChild(t, guestHome, "probe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))
	env := append(scrubEnvironment(guestHome), "NVX_PROBE=1", "NVX_FINALPATH_CHILD=1", "NVX_PROBE_TARGET="+workDir)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestProbeFinalPathNameInsideTheContainer"},
		env, workDir, sid, 0, scopeCaps)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readProbeOutput(t, read)
	requireAppContainerLaunch(t, launchErr)
	t.Logf("\n%s", strings.TrimSpace(out))
}
