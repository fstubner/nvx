//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// TestForceForegroundScriptsIsSet pins the workaround that keeps `npm install`
// working for packages with lifecycle scripts.
//
// A process inside an AppContainer cannot create a named pipe, and libuv builds
// piped child stdio on Windows out of named pipes -- so npm's default of piping
// lifecycle-script output made every install of a script-bearing package hang
// forever, inside libuv, before the child existed. Setting this tells npm to let
// scripts inherit stdio instead.
func TestForceForegroundScriptsIsSet(t *testing.T) {
	got := forceForegroundScripts([]string{"PATH=C:\\x"})

	var found bool
	for _, e := range got {
		if e == "npm_config_foreground_scripts=true" {
			found = true
		}
	}
	if !found {
		t.Errorf("npm_config_foreground_scripts was not set; every install of a package with a "+
			"postinstall would hang.\ngot: %v", got)
	}
}

// TestForceForegroundScriptsRespectsAnExplicitSetting keeps the workaround from
// overriding a deliberate choice, whichever way it was made.
func TestForceForegroundScriptsRespectsAnExplicitSetting(t *testing.T) {
	for _, existing := range []string{
		"npm_config_foreground_scripts=false",
		"NPM_CONFIG_FOREGROUND_SCRIPTS=false",
	} {
		got := forceForegroundScripts([]string{"PATH=C:\\x", existing})
		count := 0
		for _, e := range got {
			if strings.HasPrefix(strings.ToLower(e), "npm_config_foreground_scripts=") {
				count++
			}
		}
		if count != 1 {
			t.Errorf("with %q already set, ended up with %d settings; a duplicate makes the "+
				"effective value depend on env ordering", existing, count)
		}
	}
}

// TestContainedProcessCannotCreateANamedPipe records the OS restriction the
// workaround exists for, so the workaround can be removed if it ever lifts.
//
// This is the measurement that explains the bug rather than the symptom: the
// symptom was "npm install hangs", which reads like a network or npm problem and
// sent an acceptance pass four different ways before landing here.
//
// If this test starts failing because the pipe can be created, that is good news:
// piped stdio works inside the container, forceForegroundScripts is no longer
// needed, and the Known limitations entry about piped children can go.
func TestContainedProcessCannotCreateANamedPipe(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_PIPE_PROBE_CHILD") == "1" {
		name := `\\.\pipe\nvxprobe` + fmt.Sprint(os.Getpid())
		ln, err := net.Listen("npipe", name)
		if err != nil {
			// net.Listen has no "npipe" network; fall back to the same thing node
			// does via libuv -- create it through the OS directly.
			h, cerr := createNamedPipeForProbe(name)
			if cerr != nil {
				fmt.Printf("NAMEDPIPE=DENIED %v\n", cerr)
			} else {
				_ = syscall.CloseHandle(h)
				fmt.Printf("NAMEDPIPE=OK\n")
			}
			os.Exit(0)
		}
		_ = ln.Close()
		fmt.Printf("NAMEDPIPE=OK\n")
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.pipeprobe"
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

	childExe := stageProbeChild(t, guestHome, "pipeprobe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome), "NVX_PROBE=1", "NVX_PIPE_PROBE_CHILD=1")
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestContainedProcessCannotCreateANamedPipe"},
		env, workDir, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output: %q", strings.TrimSpace(got))

	if strings.Contains(got, "NAMEDPIPE=OK") {
		t.Error("a contained process created a named pipe -- piped child stdio should now work, so " +
			"forceForegroundScripts and the Known limitations entry about piped children can both go")
	} else if !strings.Contains(got, "NAMEDPIPE=DENIED") {
		t.Errorf("inconclusive result: %q", strings.TrimSpace(got))
	}
}

// createNamedPipeForProbe calls CreateNamedPipeW the way libuv does for child
// stdio, so the probe measures the same operation that hangs an install.
func createNamedPipeForProbe(name string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	const (
		pipeAccessDuplex   = 0x00000003
		pipeTypeByte       = 0x00000000
		pipeUnlimitedInsts = 255
	)
	proc := modKernel32.NewProc("CreateNamedPipeW")
	h, _, callErr := proc.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(pipeAccessDuplex),
		uintptr(pipeTypeByte),
		uintptr(pipeUnlimitedInsts),
		65536, 65536, 0, 0,
	)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return 0, callErr
	}
	return syscall.Handle(h), nil
}
