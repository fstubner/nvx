//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	procCreateJobObjectW        = modKernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = modKernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObj   = modKernel32.NewProc("AssignProcessToJobObject")
	procOpenProcessForJob       = modKernel32.NewProc("OpenProcess")
	procTerminateProcess        = modKernel32.NewProc("TerminateProcess")
)

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x00002000

	processSetQuota    = 0x0100
	processTerminate   = 0x0001
	processSynchronize = 0x00100000
)

// jobObjectBasicLimitInformation mirrors the Win32 JOBOBJECT_BASIC_LIMIT_INFORMATION
// struct. Field order and types match the C definition; Go's default alignment
// rules produce the same layout the Win32 API expects, so no manual padding is
// needed.
type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// jobObjectExtendedLimitInformation mirrors JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// createReapingJob creates a Windows Job Object configured to kill every
// process assigned to it as soon as the job's last handle closes -- which
// happens automatically when this nvx process exits or is killed, cleanly or
// not. This is what lets a sandboxed child (and any further descendants it
// spawns, e.g. a package's own version-check helper -- job membership is
// inherited by default) be reliably reaped if the process driving the sandbox
// never gets to wait for it: an MCP client that gives up on a slow
// AppContainer setup and kills nvx no longer leaves the already-launched child
// running forever.
func createReapingJob() (syscall.Handle, error) {
	h, _, err := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return 0, fmt.Errorf("CreateJobObjectW: %v", err)
	}
	job := syscall.Handle(h)

	info := jobObjectExtendedLimitInformation{
		BasicLimitInformation: jobObjectBasicLimitInformation{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}
	ret, _, err := procSetInformationJobObject.Call(
		uintptr(job),
		uintptr(jobObjectExtendedLimitInformationClass),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		_ = syscall.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject: %v", err)
	}
	return job, nil
}

// assignToReapingJob adds process to job, so it -- and anything it spawns --
// dies when the job's last handle closes.
func assignToReapingJob(job, process syscall.Handle) error {
	ret, _, err := procAssignProcessToJobObj.Call(uintptr(job), uintptr(process))
	if ret == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %v", err)
	}
	return nil
}

// openProcessForJob opens pid with the access rights AssignProcessToJobObject
// requires (PROCESS_SET_QUOTA | PROCESS_TERMINATE).
func openProcessForJob(pid uint32) (syscall.Handle, error) {
	h, _, err := procOpenProcessForJob.Call(uintptr(processSetQuota|processTerminate), 0, uintptr(pid))
	if h == 0 {
		return 0, fmt.Errorf("OpenProcess: %v", err)
	}
	return syscall.Handle(h), nil
}

// processIsRunning reports whether pid still refers to a live process.
func processIsRunning(pid int) bool {
	h, _, err := procOpenProcessForJob.Call(uintptr(processSynchronize), 0, uintptr(pid))
	if h == 0 {
		_ = err
		return false
	}
	handle := syscall.Handle(h)
	defer syscall.CloseHandle(handle)
	ret, _, _ := procWaitForSingleObject.Call(uintptr(handle), 0)
	const waitTimeout = 0x00000102
	return ret == waitTimeout
}

// superviseProcessTree puts process (and everything it spawns) in a job that
// reaps them, and publishes that job so the --connect tunnel can ask whether a
// peer belongs to this run. Returns the cleanup to defer.
//
// One function rather than the two steps inline, because the publish half had no
// test that could fail: deleting it left the whole suite green while every
// legitimate --connect connection was refused by the built binary -- the feature
// entirely dead, failing closed, and nothing noticing. That is the shape this
// project's own discipline names, a fix that is correct but never delivered. A
// seam a test can hold is what makes it checkable; see
// TestSupervisingTheProcessTreePublishesTheJobForThePeerCheck.
func superviseProcessTree(process syscall.Handle) (cleanup func()) {
	job, err := createReapingJob()
	if err != nil {
		LogWarn("Could not set up process-tree reaping for this sandbox session: %v", err)
		return func() {}
	}
	if err := assignToReapingJob(job, process); err != nil {
		LogWarn("Could not enable process-tree reaping for this sandbox session: %v", err)
		return func() { _ = syscall.CloseHandle(job) }
	}
	// Only after a successful assignment, so membership actually means something,
	// and before the target runs, so no tunnel traffic can arrive while it is
	// still unset.
	setSessionJob(job)
	return func() {
		setSessionJob(0)
		_ = syscall.CloseHandle(job)
	}
}
