//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Can a contained process CONNECT to a named pipe the parent created?
//
// Everything measured so far says a contained process cannot *create* one:
// TestAppContainerCannotCreateNamedPipes and TestContainedProcessCannotCreateANamedPipe
// both call CreateNamedPipeW from inside the container and get
// ERROR_ACCESS_DENIED, which libuv reports as EADDRINUSE. That is why async
// `spawn(..., {stdio: 'pipe'})` hangs, and it is the limitation the whole
// restricted-token proposal existed to work around -- at the cost of the egress
// guarantee, which a separate experiment then showed it could not replace.
//
// Nobody has tested the other direction. Creating a named pipe instance and
// opening an existing one are different access checks: creation is against
// \Device\NamedPipe, which nvx cannot grant per-container, while opening is
// against the pipe's own security descriptor, which the creator chooses. An ACL
// naming the container's package SID could plausibly permit it.
//
// If it can connect, async piped stdio becomes solvable without emulating stream
// semantics: the PARENT creates the pipes, the preload already present in every
// contained node process hands them to libuv, and the container never creates
// anything. That would close the largest remaining Windows limitation.
//
// Four cases, to separate "the ACL did it" from "it works anyway":
//   - default security, which is the control
//   - an ACL naming this container's package SID
//   - an ACL naming ALL APPLICATION PACKAGES
//   - an ACL naming Everyone, the upper bound
//
// A connection alone is not the answer, so the child also writes and reads. A
// pipe that opens and then refuses I/O would be a false positive.
func TestAppContainerCanConnectToAParentCreatedNamedPipe(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	if os.Getenv("NVX_PIPE_CONNECT_CHILD") == "1" {
		runPipeConnectChild()
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.pipeconnprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	containerSID, err := appContainerSidToString(sid)
	if err != nil {
		t.Fatalf("container SID: %v", err)
	}
	t.Logf("container package SID: %s", containerSID)

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	cases := []struct {
		name string
		// sddl is the pipe's security descriptor. Empty means default security,
		// which is what a plain CreateNamedPipeW gets.
		sddl string
	}{
		{"default security (control)", ""},
		{"package SID alone", "D:(A;;GA;;;" + containerSID + ")"},
		{"ALL APPLICATION PACKAGES alone", "D:(A;;GA;;;AC)"},
		{"Everyone alone", "D:(A;;GA;;;WD)"},

		// The combinations are the ones that can actually pass. An
		// AppContainer's access check is satisfied only when the DACL grants
		// BOTH the user identity the process runs as AND the container's package
		// identity; either ACE alone is a denial, which is why the four cases
		// above cannot distinguish "the ACL was wrong" from "the device refuses
		// this outright". The first version of this probe had only those four
		// and would have reported a false negative.
		{"Everyone + ALL APPLICATION PACKAGES", "D:(A;;GA;;;WD)(A;;GA;;;AC)"},
		{"Everyone + this container's package SID", "D:(A;;GA;;;WD)(A;;GA;;;" + containerSID + ")"},
	}

	// Staged once, not per case: it is a copy of this whole test binary, and
	// four copies made the probe slower than the thing it measures.
	childExe := stageProbeChild(t, guestHome, "pipeconn.exe")

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipeName := fmt.Sprintf(`\\.\pipe\nvxconn%d-%d`, os.Getpid(), i)

			server, err := createNamedPipeWithSDDL(pipeName, tc.sddl)
			if err != nil {
				t.Fatalf("the PARENT could not create the pipe, so this case proves nothing: %v", err)
			}
			// Cancel BEFORE closing, on every exit path including a skip.
			//
			// A pending blocking ConnectNamedPipe makes CloseHandle wait for I/O that
			// will never complete. On a host that refuses AppContainer children -- every
			// GitHub-hosted Windows runner -- requireAppContainerLaunch skips below, the
			// deferred close then blocked, and the whole package hit Go's 10-minute
			// timeout: 9m39s in this one subtest. Cancelling only on the success path
			// was the bug.
			defer func() {
				procCancelIoEx.Call(uintptr(server), 0)
				syscall.CloseHandle(server)
			}()

			// Serve the pipe while the child runs: accept, echo, done. Started
			// before the launch because ConnectNamedPipe must be waiting when the
			// child opens the pipe.
			served := make(chan string, 1)
			go func() { served <- servePipeEcho(server) }()

			read, write := makeTestPipe(t)
			defer syscall.CloseHandle(read)
			prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
			const stdOutputHandle = uintptr(0xFFFFFFF5)
			procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

			env := append(scrubEnvironment(guestHome),
				"NVX_PROBE=1",
				"NVX_PIPE_CONNECT_CHILD=1",
				"NVX_PIPE_CONNECT_NAME="+pipeName,
			)
			started := time.Now()
			exitCode, launchErr := launchAppContainerProcess(
				childExe,
				[]string{"-test.run=TestAppContainerCanConnectToAParentCreatedNamedPipe"},
				env, workDir, sid, 0, scopeCaps,
			)

			procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
			syscall.CloseHandle(write)
			out := readWithTimeout(t, read)
			requireAppContainerLaunch(t, launchErr)

			t.Logf("launch took %v, child exit=%d", time.Since(started).Round(time.Millisecond), exitCode)
			t.Logf("child report: %s", strings.TrimSpace(out))
			if !strings.Contains(out, pipeName) {
				t.Fatalf("the child did not report this case's pipe (%s), so this result is not "+
					"evidence about it. Got: %q", pipeName, out)
			}

			// Cancel the pending ConnectNamedPipe. Without this the serve
			// goroutine waits for a client that is never coming -- the control
			// case, by design -- and a blocking-mode pipe is not unblocked by
			// closing its handle, so the subtest never returns. The first run of
			// this probe hung for ten minutes on exactly that.
			//
			// Safe here because the child has already exited: if an exchange was
			// going to happen it has happened.
			procCancelIoEx.Call(uintptr(server), 0)

			select {
			case got := <-served:
				t.Logf("parent side: %s", got)
			case <-time.After(5 * time.Second):
				t.Log("parent side: nothing arrived")
			}

			switch {
			case strings.Contains(out, "roundtrip=OK"):
				t.Logf("RESULT: the contained process opened the pipe AND moved data through it. " +
					"Async piped stdio is reachable this way: the parent creates the pipes, the " +
					"container only opens them.")
			case strings.Contains(out, "open=OK"):
				t.Logf("RESULT: the pipe opened but I/O did not complete -- opening alone is not enough " +
					"to carry stdio, so treat this as a negative until the round trip works.")
			default:
				t.Logf("RESULT: the contained process could not open the pipe.")
			}
		})
	}
}

// runPipeConnectChild opens the pipe by name and tries a round trip.
func runPipeConnectChild() {
	name := os.Getenv("NVX_PIPE_CONNECT_NAME")
	// Identifying itself, so a case that never ran cannot be mistaken for one
	// that ran and was denied. Four identical "Access is denied" lines with
	// implausible timings is what prompted this.
	fmt.Printf("child pid=%d pipe=%s ", os.Getpid(), name)
	h, err := openNamedPipe(name)
	if err != nil {
		fmt.Printf("open=FAILED %v\n", err)
		return
	}
	defer syscall.CloseHandle(h)
	fmt.Print("open=OK ")

	// Opening proves the security descriptor let us in. Moving bytes proves the
	// handle is usable as stdio would need it to be.
	msg := []byte("PING-FROM-CONTAINER")
	var wrote uint32
	if err := syscall.WriteFile(h, msg, &wrote, nil); err != nil {
		fmt.Printf("write=FAILED %v\n", err)
		return
	}
	buf := make([]byte, 64)
	var readN uint32
	if err := syscall.ReadFile(h, buf, &readN, nil); err != nil {
		fmt.Printf("read=FAILED %v\n", err)
		return
	}
	if string(buf[:readN]) == "PONG-FROM-PARENT" {
		fmt.Println("roundtrip=OK")
		return
	}
	fmt.Printf("roundtrip=UNEXPECTED %q\n", string(buf[:readN]))
}

// createNamedPipeWithSDDL creates the server end, optionally with an explicit
// security descriptor. An empty sddl means default security.
func createNamedPipeWithSDDL(name, sddl string) (syscall.Handle, error) {
	const (
		pipeAccessDuplex = 0x00000003
		pipeTypeByte     = 0x00000000
		pipeWait         = 0x00000000
		pipeUnlimited    = 255
	)

	var saPtr uintptr
	if sddl != "" {
		sd, err := securityDescriptorFromSDDL(sddl)
		if err != nil {
			return syscall.InvalidHandle, err
		}
		defer syscall.LocalFree(syscall.Handle(sd))
		sa := testSecAttrs{SecurityDescriptor: sd, InheritHandle: 0}
		sa.Length = uint32(unsafe.Sizeof(sa))
		saPtr = uintptr(unsafe.Pointer(&sa))
	}

	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	h, _, callErr := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(pipeAccessDuplex),
		uintptr(pipeTypeByte|pipeWait),
		uintptr(pipeUnlimited),
		65536, 65536, 0,
		saPtr,
	)
	if h == uintptr(syscall.InvalidHandle) {
		return syscall.InvalidHandle, fmt.Errorf("CreateNamedPipeW(%s): %v", name, callErr)
	}
	return syscall.Handle(h), nil
}

// servePipeEcho waits for a client, reads one message and answers it.
func servePipeEcho(server syscall.Handle) string {
	ret, _, callErr := procConnectNamedPipe.Call(uintptr(server), 0)
	// ERROR_PIPE_CONNECTED means the client got there first, which is a success.
	const errPipeConnected = 535
	if ret == 0 {
		if errno, ok := callErr.(syscall.Errno); !ok || uintptr(errno) != errPipeConnected {
			return fmt.Sprintf("ConnectNamedPipe failed: %v", callErr)
		}
	}

	buf := make([]byte, 64)
	var n uint32
	if err := syscall.ReadFile(server, buf, &n, nil); err != nil {
		return fmt.Sprintf("read failed: %v", err)
	}
	var wrote uint32
	if err := syscall.WriteFile(server, []byte("PONG-FROM-PARENT"), &wrote, nil); err != nil {
		return fmt.Sprintf("received %q, but write back failed: %v", string(buf[:n]), err)
	}
	return fmt.Sprintf("received %q and replied", string(buf[:n]))
}

// openNamedPipe opens an existing pipe by name for read and write.
func openNamedPipe(name string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(
		p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, nil,
		syscall.OPEN_EXISTING,
		0, 0,
	)
}

// securityDescriptorFromSDDL builds a self-relative security descriptor from an
// SDDL string. The caller frees it with LocalFree.
func securityDescriptorFromSDDL(sddl string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		return 0, err
	}
	var sd uintptr
	const sddlRevision1 = 1
	ret, _, callErr := procConvertStringSDToSD.Call(
		uintptr(unsafe.Pointer(p)),
		sddlRevision1,
		uintptr(unsafe.Pointer(&sd)),
		0,
	)
	if ret == 0 {
		return 0, fmt.Errorf("ConvertStringSecurityDescriptorToSecurityDescriptorW(%s): %v", sddl, callErr)
	}
	return sd, nil
}

var (
	procConnectNamedPipe    = modKernel32.NewProc("ConnectNamedPipe")
	procCancelIoEx          = modKernel32.NewProc("CancelIoEx")
	procConvertStringSDToSD = modAdvapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)
