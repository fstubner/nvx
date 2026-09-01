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

// superviseDirectChild reaps an UNCONTAINED child -- the --no-sandbox path and
// the "your own code" path -- when nvx goes away.
//
// The sandboxed path has had this since the 38-orphan incident; the direct path
// never did, and the leak simply moved down one level. Measured on the
// development machine 2026-09-01: 92 stranded `node` processes holding 78
// consoles open and 1.6 GB, every one of them a child nvx started directly and
// then stopped waiting on. They came from the test that checks nvx does not
// strand processes, which asserted only that nvx itself left.
//
// A job rather than killing the child from the hangup watchdog, because in the
// reproduction nvx was gone about a second after its client, long before the
// watchdog's first 15-second poll -- so a fix hung off the watchdog would not
// have covered the case actually observed. Kill-on-job-close fires whenever
// nvx's last handle closes, including an os.Exit that runs no defers and a
// TerminateProcess from outside, which is the property needed here.
//
// The tradeoff, stated because it is a behaviour change: a process deliberately
// detached by a command run through nvx now dies with nvx, since the job does
// not permit breakaway. That already holds for everything sandboxed, and it is
// the same bargain -- nvx supervises what it starts, or it leaks it.
//
// Deliberately does NOT publish the job with setSessionJob: that seam answers
// "does this tunnel peer belong to my run", and an uncontained command has no
// tunnel. Reaping is the only thing wanted here.
func superviseDirectChild(pid int) (cleanup func()) {
	noop := func() {}
	job, err := createReapingJob()
	if err != nil {
		LogWarn("Could not set up process-tree reaping for this command: %v", err)
		return noop
	}
	proc, err := openProcessForJob(uint32(pid))
	if err != nil {
		_ = syscall.CloseHandle(job)
		// Silent when the child has already finished: a command that exits
		// quickly is the common case, not a fault, and warning on it would put a
		// line on the terminal for most short invocations.
		if processIsRunning(pid) {
			LogWarn("Could not supervise the command's process tree: %v", err)
		}
		return noop
	}
	defer syscall.CloseHandle(proc)

	if err := assignToReapingJob(job, proc); err != nil {
		_ = syscall.CloseHandle(job)
		if processIsRunning(pid) {
			LogWarn("Could not enable process-tree reaping for this command: %v", err)
		}
		return noop
	}
	return func() { _ = syscall.CloseHandle(job) }
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
