//go:build windows

package nvx

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

var (
	modNtdllProbe                = syscall.NewLazyDLL("ntdll.dll")
	probeNtOpenDirectoryObject   = modNtdllProbe.NewProc("NtOpenDirectoryObject")
	probeNtOpenSymbolicLinkObj   = modNtdllProbe.NewProc("NtOpenSymbolicLinkObject")
	probeGetSecurityInfo         = modAdvapi32.NewProc("GetSecurityInfo")
	probeSetSecurityInfo         = modAdvapi32.NewProc("SetSecurityInfo")
	probeDefineDosDeviceW        = modKernel32.NewProc("DefineDosDeviceW")
	probeQueryDosDeviceW2        = modKernel32.NewProc("QueryDosDeviceW")
	probeGetFinalPathNameByHandl = modKernel32.NewProc("GetFinalPathNameByHandleW")
)

type probeUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

type probeObjectAttributes struct {
	Length                   uint32
	RootDirectory            uintptr
	ObjectName               *probeUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

func probeOpenObject(proc *syscall.LazyProc, name string, access uint32) (syscall.Handle, error) {
	u, _ := syscall.UTF16FromString(name)
	us := probeUnicodeString{Length: uint16((len(u) - 1) * 2), MaximumLength: uint16(len(u) * 2), Buffer: &u[0]}
	oa := probeObjectAttributes{ObjectName: &us, Attributes: 0x40}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h syscall.Handle
	st, _, _ := proc.Call(uintptr(unsafe.Pointer(&h)), uintptr(access), uintptr(unsafe.Pointer(&oa)))
	if st != 0 {
		return 0, fmt.Errorf("NTSTATUS 0x%X", st)
	}
	return h, nil
}

// probeAddObjectAce adds a non-inheritable allow ACE for sidStr on an already
// open kernel object and returns a function that restores the previous DACL.
func probeAddObjectAce(t *testing.T, h syscall.Handle, sidStr string, mask uint32) func() {
	const seKernelObject = 6
	var dacl *win32ACL
	var sd *byte
	rc, _, _ := probeGetSecurityInfo.Call(uintptr(h), seKernelObject, daclSecurityInformation, 0, 0,
		uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd)))
	if rc != 0 {
		t.Fatalf("read object DACL: %v", syscall.Errno(rc))
	}
	sid, err := sidFromString(sidStr)
	if err != nil {
		t.Fatal(err)
	}
	sidLen, _, _ := procGetLengthSid.Call(uintptr(unsafe.Pointer(sid)))
	size := int(dacl.AclSize) + int(unsafe.Sizeof(accessAllowedACE{})) + int(sidLen)
	buf := make([]byte, size)
	newACL := unsafe.Pointer(&buf[0])
	procInitializeAcl.Call(uintptr(newACL), uintptr(size), aclRevision)
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *accessAllowedACE
		procGetAce.Call(uintptr(unsafe.Pointer(dacl)), uintptr(i), uintptr(unsafe.Pointer(&ace)))
		procAddAce.Call(uintptr(newACL), aclRevision, uintptr(^uint32(0)), uintptr(unsafe.Pointer(ace)), uintptr(ace.Header.AceSize))
	}
	if r, _, e := procAddAccessAllowedAceEx.Call(uintptr(newACL), aclRevision, 0, uintptr(mask), uintptr(unsafe.Pointer(sid))); r == 0 {
		t.Fatal(e)
	}
	rc, _, _ = probeSetSecurityInfo.Call(uintptr(h), seKernelObject, daclSecurityInformation, 0, 0, uintptr(newACL), 0)
	if rc != 0 {
		t.Fatalf("write object DACL: %v", syscall.Errno(rc))
	}
	return func() {
		probeSetSecurityInfo.Call(uintptr(h), seKernelObject, daclSecurityInformation, 0, 0, uintptr(unsafe.Pointer(dacl)), 0)
		syscall.LocalFree(syscall.Handle(unsafe.Pointer(sd)))
	}
}

// Does a SESSION-LOCAL, UNUSED drive letter -- not C: itself -- let a
// contained process resolve DOS paths?
//
// A prior version of this probe tried to redefine C: itself, inside the
// container's namespace, and separately outside it. Both were refused:
// DefineDosDevice("C:") returns "Access is denied" even unelevated and even
// on the host process, because it collides with a live system drive letter.
// That is a different refusal from the container's own: it is not about the
// AppContainer at all, it is Windows protecting an in-use drive letter from
// being redefined by anyone who is not elevated.
//
// An UNUSED letter has no such protection. DefineDosDevice("Y:") pointing at
// the same underlying volume device succeeds unelevated, confirmed outside
// this test with a standalone probe. The question this test answers: once
// Y: exists and is made enumerable to the capability (by ACE'ing \?? and the
// \??\Y: symlink object, the same technique tried against C: and found
// insufficient there), does GetFinalPathNameByHandle(VOLUME_NAME_DOS) -- which
// is documented to return WHICHEVER currently-enumerable drive letter maps to
// the target volume, not necessarily the one the caller used to open the
// path -- pick up Y: for a handle opened via a plain C:\... path?
//
// No. Measured 2026-09-18, inside a real container holding the capability:
//
//	QueryDosDevice(C:)                    Access is denied
//	QueryDosDevice(Y:)                    \Device\HarddiskVolume3
//	open via Y:\Users\...\nvxNNNNN        OK
//	final(DOS) of a handle opened via C:  Access is denied  -- unchanged
//
// So Y: is a real, working drive letter from inside the container: it can be
// queried and files can be opened through it by name. That rules out "the
// container cannot see local drive letters at all" as the mechanism. But
// GetFinalPathNameByHandle(VOLUME_NAME_DOS) on a handle already opened via
// C:\... still refuses, identically to before Y: existed. It does not fall
// back to Y: as an alternative name for the same volume, which means its
// refusal is not an enumeration-permission check against \?? or against the
// symlink objects this probe can grant access to -- both of those are proven
// readable here. It is checking something else, most likely a privilege
// gate in the mount point manager (\Device\MountPointManager), which is a
// different object with its own access control this technique never reaches.
//
// This is the answer to "is there really no way for this to work": not
// through any ACL this probe can write, on either the real drive letter or
// an unelevated substitute for it. pnpm's realpath.native and bun's internal
// path resolution both call the DOS-name form of this API on a handle to a
// path already reached through the real drive, so a synthetic letter does
// not help either of them -- the call that fails is the same call, on the
// same handle, independent of which letter opened it.
func TestProbeLocalDriveLinkForContainer(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}
	if os.Getenv("NVX_LOCALDRIVE_CHILD") == "1" {
		target := os.Getenv("NVX_PROBE_TARGET")
		p, _ := syscall.UTF16PtrFromString(target)
		h, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
			nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			fmt.Printf("open via C:\\=%v\n", err)
			os.Exit(0)
		}
		buf := make([]uint16, 1024)
		n, _, e := probeGetFinalPathNameByHandl.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			fmt.Printf("final(DOS) of a handle opened via C:\\=ERR %v\n", e)
		} else {
			fmt.Printf("final(DOS) of a handle opened via C:\\=%s\n", syscall.UTF16ToString(buf[:n]))
		}
		for _, letter := range []string{"C:", "Y:"} {
			dev, _ := syscall.UTF16PtrFromString(letter)
			n, _, e = probeQueryDosDeviceW2.Call(uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			if n == 0 {
				fmt.Printf("QueryDosDevice(%s)=ERR %v\n", letter, e)
			} else {
				fmt.Printf("QueryDosDevice(%s)=%s\n", letter, syscall.UTF16ToString(buf[:n]))
			}
		}
		// Direct proof, independent of GetFinalPathNameByHandle: open the same
		// directory through the synthetic letter rather than through a handle
		// the OS chose a path for.
		if rel := os.Getenv("NVX_PROBE_TARGET_REL"); rel != "" {
			yPath := `Y:\` + rel
			yp, _ := syscall.UTF16PtrFromString(yPath)
			yh, yerr := syscall.CreateFile(yp, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
				nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
			if yerr != nil {
				fmt.Printf("open via %s=ERR %v\n", yPath, yerr)
			} else {
				fmt.Printf("open via %s=OK\n", yPath)
				syscall.CloseHandle(yh)
			}
		}
		os.Exit(0)
	}

	runtimeCap, err := runtimeCapabilitySID()
	if err != nil {
		t.Fatal(err)
	}

	// The global target of C:, so the synthetic link points at the same device.
	buf := make([]uint16, 1024)
	dev, _ := syscall.UTF16PtrFromString("C:")
	n, _, e := probeQueryDosDeviceW2.Call(uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		t.Fatalf("QueryDosDevice(C:) outside the container: %v", e)
	}
	target := syscall.UTF16ToString(buf[:n])
	t.Logf("C: -> %s", target)

	// 1. The session's \?? directory: query and traverse for the capability.
	const readControl, writeDac = 0x00020000, 0x00040000
	dir, err := probeOpenObject(probeNtOpenDirectoryObject, `\??`, readControl|writeDac)
	if err != nil {
		t.Fatalf(`open \??: %v`, err)
	}
	defer syscall.CloseHandle(dir)
	restoreDir := probeAddObjectAce(t, dir, runtimeCap, 0x20003)
	defer restoreDir()

	// 2. A session-local, UNUSED letter pointing at the same device as C:.
	const dddRawTargetPath, dddRemoveDefinition, dddExactMatchOnRemove = 0x1, 0x2, 0x4
	tgt, _ := syscall.UTF16PtrFromString(target)
	synth, _ := syscall.UTF16PtrFromString("Y:")
	if r, _, e := probeDefineDosDeviceW.Call(dddRawTargetPath, uintptr(unsafe.Pointer(synth)), uintptr(unsafe.Pointer(tgt))); r == 0 {
		t.Fatalf("DefineDosDevice(Y:): %v", e)
	}
	defer func() {
		r, _, e := probeDefineDosDeviceW.Call(dddRawTargetPath|dddRemoveDefinition|dddExactMatchOnRemove,
			uintptr(unsafe.Pointer(synth)), uintptr(unsafe.Pointer(tgt)))
		t.Logf("removed local Y: ok=%v err=%v", r != 0, e)
	}()

	// 3. The local link itself: query for the capability.
	const symbolicLinkQuery = 0x1
	link, err := probeOpenObject(probeNtOpenSymbolicLinkObj, `\??\Y:`, readControl|writeDac)
	if err != nil {
		t.Fatalf(`open \??\Y: link: %v`, err)
	}
	defer syscall.CloseHandle(link)
	restoreLink := probeAddObjectAce(t, link, runtimeCap, symbolicLinkQuery|readControl)
	defer restoreLink()

	// 4. A contained child holding the capability.
	const probeProfile = "nvx.sandbox.localdriveprobe"
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
	// workDir is a C:\... path; strip the drive letter, not the device name,
	// to get the part usable after Y:\.
	rel := workDir
	if len(rel) >= 2 && rel[1] == ':' {
		rel = rel[2:]
	}
	rel = strings.TrimPrefix(rel, `\`)
	env := append(scrubEnvironment(guestHome), "NVX_PROBE=1", "NVX_LOCALDRIVE_CHILD=1",
		"NVX_PROBE_TARGET="+workDir, "NVX_PROBE_TARGET_REL="+rel)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestProbeLocalDriveLinkForContainer"},
		env, workDir, sid, 0, append(scopeCaps, runtimeCap))
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	out := readProbeOutput(t, read)
	requireAppContainerLaunch(t, launchErr)
	t.Logf("\n%s", strings.TrimSpace(out))
}
