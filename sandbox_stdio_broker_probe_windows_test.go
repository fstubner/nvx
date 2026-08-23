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

// Can a contained process give a host-created pipe to its OWN child as stdout?
//
// This is the step between "a contained process can open a pipe the parent made"
// (TestAppContainerCanConnectToAParentCreatedNamedPipe) and a fix for async
// piped stdio, which is the limitation that has been quietly stranding a process
// every time a contained `npx vitest` runs: node calls CreateNamedPipeW for
// `spawn(..., {stdio:'pipe'})`, an AppContainer is refused, libuv reports
// EADDRINUSE, and the command blocks forever.
//
// The shape a fix would use is: nvx creates the pipes outside the container and
// the contained node only ever OPENS them, then passes the handle to its child
// as that child's stdout. Opening is proven. Handing the opened handle to a
// grandchild is not, and it is the part that decides whether the design works at
// all -- an AppContainer child creating a process, and that grandchild
// inheriting a handle to an object created outside the container, are two
// separate access checks nobody has measured.
//
// Deliberately does not involve node. If this fails, no amount of preload work
// helps; if it succeeds, what remains is plumbing in JavaScript.
func TestContainedChildCanGiveAHostPipeToItsOwnChild(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	switch os.Getenv("NVX_BROKER_ROLE") {
	case "middle":
		runBrokerMiddleChild()
		os.Exit(0)
	case "leaf":
		runBrokerLeafChild()
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.brokerprobe"
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

	guestHome := tempDir(t)
	workDir := tempDir(t)
	scopeCaps, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	// Both the user identity and the container's package identity, which is the
	// combination the connect probe established is required -- either ACE alone
	// reads as a flat denial.
	pipeName := fmt.Sprintf(`\\.\pipe\nvxbroker%d`, os.Getpid())
	server, err := createNamedPipeWithSDDL(pipeName, "D:(A;;GA;;;WD)(A;;GA;;;"+containerSID+")")
	if err != nil {
		t.Fatalf("the host could not create the pipe: %v", err)
	}
	// Cancel before closing, on every exit path. A pending ConnectNamedPipe
	// makes CloseHandle block forever on a host that refuses AppContainer
	// children, which is every GitHub-hosted Windows runner.
	defer func() {
		procCancelIoEx.Call(uintptr(server), 0)
		syscall.CloseHandle(server)
	}()

	received := make(chan string, 1)
	go func() { received <- readWholePipe(server) }()

	childExe := stageProbeChild(t, guestHome, "broker.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_BROKER_ROLE=middle",
		"NVX_BROKER_PIPE="+pipeName,
		"NVX_BROKER_EXE="+childExe,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestContainedChildCanGiveAHostPipeToItsOwnChild"},
		env, workDir, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	report := readWithTimeout(t, read)
	requireAppContainerLaunch(t, launchErr)
	t.Logf("contained middle process reported: %s", strings.TrimSpace(report))

	procCancelIoEx.Call(uintptr(server), 0)
	select {
	case got := <-received:
		t.Logf("host read from the pipe: %q", got)
		if strings.Contains(got, "HELLO-FROM-GRANDCHILD") {
			t.Log("RESULT: a contained process opened a host-created pipe and handed it to its own " +
				"child as stdout, and the bytes arrived outside the container. The async-stdio fix " +
				"can be built on this: nvx creates the pipes, contained code only opens them.")
			return
		}
		t.Errorf("RESULT: the grandchild's output did not arrive. Report:\n%s", report)
	case <-time.After(10 * time.Second):
		t.Errorf("RESULT: nothing arrived on the pipe. Report:\n%s", report)
	}
}

// runBrokerMiddleChild stands in for a contained node process: it opens the pipe
// nvx made and gives it to its own child as stdout.
func runBrokerMiddleChild() {
	name := os.Getenv("NVX_BROKER_PIPE")
	h, err := openNamedPipe(name)
	if err != nil {
		fmt.Printf("open=FAILED %v\n", err)
		return
	}
	defer syscall.CloseHandle(h)
	fmt.Print("open=OK ")

	// Inheritable, or the grandchild receives nothing -- the same requirement
	// any piped stdio has.
	if err := markHandleInheritable(h); err != nil {
		fmt.Printf("inheritable=FAILED %v\n", err)
		return
	}

	if err := spawnLeafWithStdout(os.Getenv("NVX_BROKER_EXE"), h); err != nil {
		fmt.Printf("spawn=FAILED %v\n", err)
		return
	}
	fmt.Println("spawn=OK")
}

// runBrokerLeafChild is the grandchild. It writes to whatever it was handed as
// stdout, exactly as any ordinary program does.
func runBrokerLeafChild() {
	fmt.Println("HELLO-FROM-GRANDCHILD")
}

// spawnLeafWithStdout starts the leaf with stdout set to an already-open handle.
func spawnLeafWithStdout(exe string, stdout syscall.Handle) error {
	cmdLine, err := syscall.UTF16FromString(buildWindowsCommandLine(exe,
		[]string{"-test.run=TestContainedChildCanGiveAHostPipeToItsOwnChild"}))
	if err != nil {
		return err
	}
	appName, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	// Replaced, not appended: a Windows environment block with the key twice
	// keeps the FIRST value, so appending left the leaf running as the middle
	// role. It then failed to open the already-busy pipe and printed the failure
	// -- into the very pipe under test, which is how the first run of this probe
	// managed to look like a negative result while demonstrating a positive one.
	env := []string{"NVX_BROKER_ROLE=leaf"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NVX_BROKER_ROLE=") {
			env = append(env, kv)
		}
	}
	envBlock, err := buildWindowsEnvironmentBlock(env)
	if err != nil {
		return err
	}

	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = STARTF_USESTDHANDLES
	si.StdOutput = stdout
	si.StdErr = stdout

	var pi processInformation
	ok, _, callErr := procCreateProcessW.Call(
		uintptr(unsafe.Pointer(appName)),
		uintptr(unsafe.Pointer(&cmdLine[0])),
		0, 0,
		1, // bInheritHandles
		uintptr(CREATE_UNICODE_ENVIRONMENT),
		uintptr(unsafe.Pointer(envBlock)),
		0,
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if ok == 0 {
		return fmt.Errorf("CreateProcess(leaf): %w", callErr)
	}
	defer syscall.CloseHandle(pi.hThread)
	defer syscall.CloseHandle(pi.hProcess)
	_, _ = syscall.WaitForSingleObject(pi.hProcess, 15000)
	return nil
}

// readWholePipe accepts one client and reads until the writers are gone.
func readWholePipe(server syscall.Handle) string {
	ret, _, callErr := procConnectNamedPipe.Call(uintptr(server), 0)
	const errPipeConnected = 535
	if ret == 0 {
		if errno, ok := callErr.(syscall.Errno); !ok || uintptr(errno) != errPipeConnected {
			return fmt.Sprintf("ConnectNamedPipe failed: %v", callErr)
		}
	}
	var out []byte
	buf := make([]byte, 4096)
	for {
		var n uint32
		if err := syscall.ReadFile(server, buf, &n, nil); err != nil || n == 0 {
			break
		}
		out = append(out, buf[:n]...)
	}
	return string(out)
}
