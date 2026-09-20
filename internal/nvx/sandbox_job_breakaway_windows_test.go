//go:build windows

package nvx

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"unsafe"
)

// Inside a job that forbids breakaway, the launch does not ask for it.
//
// This is the failure every agent harness hit for weeks: their shells run
// inside a job object without JOB_OBJECT_LIMIT_BREAKAWAY_OK, and a CreateProcess
// carrying CREATE_BREAKAWAY_FROM_JOB from there is refused with "Access is
// denied" before Windows opens the image or creates a process object -- so it
// looked like an ACL problem and was not. Measured 2026-09-20 with a plain
// cmd.exe, no AppContainer: OK without the flag, denied with it.
//
// The test process cannot put itself into such a job and get out again, and a
// job assigned to it would outlive this test into every other. So the check
// runs in a child: this binary again, created suspended, assigned to a job that
// forbids breakaway, then resumed to report which flags it would launch with.
func TestBreakawayIsNotRequestedInsideAJobThatForbidsIt(t *testing.T) {
	if os.Getenv("NVX_BREAKAWAY_FLAGS_CHILD") == "1" {
		report := os.Getenv("NVX_BREAKAWAY_FLAGS_FILE")
		asked := appContainerCreationFlags()&CREATE_BREAKAWAY_FROM_JOB != 0
		_ = os.WriteFile(report, []byte(strconv.FormatBool(asked)), 0o600)
		os.Exit(0)
	}

	report := filepath.Join(t.TempDir(), "flags.txt")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// KillOnJobClose only: no BREAKAWAY_OK, which is the shape of a harness job.
	job, err := createReapingJob()
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	defer syscall.CloseHandle(job)

	env := append(os.Environ(),
		"NVX_BREAKAWAY_FLAGS_CHILD=1",
		"NVX_BREAKAWAY_FLAGS_FILE="+report,
	)
	exeW, _ := syscall.UTF16PtrFromString(exe)
	cmdW, _ := syscall.UTF16PtrFromString(`"` + exe + `" -test.run=^TestBreakawayIsNotRequestedInsideAJobThatForbidsIt$`)
	envBlock, err := buildWindowsEnvironmentBlock(env)
	if err != nil {
		t.Fatal(err)
	}
	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi syscall.ProcessInformation
	const createSuspended = 0x00000004
	if err := syscall.CreateProcess(exeW, cmdW, nil, nil, false,
		createSuspended|CREATE_UNICODE_ENVIRONMENT|0x08000000,
		envBlock, nil, &si, &pi); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer syscall.CloseHandle(pi.Process)
	defer syscall.CloseHandle(pi.Thread)

	if err := assignToReapingJob(job, syscall.Handle(pi.Process)); err != nil {
		procTerminateProcess.Call(uintptr(pi.Process), 1)
		t.Fatalf("assign child to job: %v", err)
	}
	if r, _, err := modKernel32.NewProc("ResumeThread").Call(uintptr(pi.Thread)); r == ^uintptr(0) {
		t.Fatalf("resume child: %v", err)
	}
	if _, err := syscall.WaitForSingleObject(pi.Process, 30000); err != nil {
		t.Fatalf("wait for child: %v", err)
	}

	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("the child never reported its flags: %v", err)
	}
	if string(got) != "false" {
		t.Fatalf("inside a job that forbids breakaway, the launch would still ask for "+
			"CREATE_BREAKAWAY_FROM_JOB (child reported %q); Windows refuses that with "+
			"Access is denied before creating anything, which is the failure every "+
			"harness shell hit", got)
	}
}
