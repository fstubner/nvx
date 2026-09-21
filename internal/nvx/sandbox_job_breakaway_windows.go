//go:build windows

package nvx

import (
	"syscall"
	"unsafe"
)

// Whether this process may ask for CREATE_BREAKAWAY_FROM_JOB at all.
//
// The launch passed that flag unconditionally, and the comment beside it said
// why: so a restrictive CI job object would not block CreateProcess. The flag
// has exactly the opposite effect. A process inside a job that lacks
// JOB_OBJECT_LIMIT_BREAKAWAY_OK is refused CreateProcess with
// ERROR_ACCESS_DENIED the moment it asks to break away -- before any image is
// opened, before any process object exists, and before any ACL is consulted.
//
// Measured 2026-09-20, on a host whose interactive terminals ran every
// contained command fine. From a shell inside such a job, with no
// AppContainer involved at all:
//
//	plain CreateProcess                 -> OK
//	with CREATE_BREAKAWAY_FROM_JOB      -> Access is denied.
//
// Every agent harness runs its commands inside a job like that -- Claude
// Code's shell tool, Codex CLI, the sessions whose refusals filled the audit
// log for weeks -- and a Procmon capture of the failure showed no object
// denied and no Process Create event, which is what a refusal at this point
// looks like. Memory, disk, elevation, ACL bloat, leaked profiles and a
// security product were each measured and ruled out first, at some cost.
//
// So the flag is asked for only when the calling process is not in a job, or
// is in one that permits breakaway. A child that stays in the caller's job is
// still reaped: nested jobs are supported since Windows 8, so
// superviseProcessTree's own job assignment works on it unchanged.

var procQueryInformationJobObject = modKernel32.NewProc("QueryInformationJobObject")

const jobObjectLimitBreakawayOK = 0x00000800

// breakawayPermitted reports whether CreateProcess from this process may carry
// CREATE_BREAKAWAY_FROM_JOB without being refused for it.
//
// QueryInformationJobObject with a NULL job handle answers for the job the
// calling process belongs to. A process in no job at all makes the call fail,
// which is the other case where breakaway is harmless, so both failure and a
// permissive job answer true; only a job that forbids it answers false.
func breakawayPermitted() bool {
	var inJob int32
	ret, _, _ := procIsProcessInJob.Call(^uintptr(0), 0, uintptr(unsafe.Pointer(&inJob)))
	if ret == 0 || inJob == 0 {
		return true
	}
	var info jobObjectExtendedLimitInformation
	ret, _, _ = procQueryInformationJobObject.Call(
		0,
		uintptr(jobObjectExtendedLimitInformationClass),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
		0,
	)
	if ret == 0 {
		// Could not read the job's limits. Asking to break away from a job that
		// might forbid it is the failure this file exists to stop, and staying in
		// the job costs nothing, so the safe answer is no.
		return false
	}
	return info.BasicLimitInformation.LimitFlags&jobObjectLimitBreakawayOK != 0
}

// appContainerCreationFlags is the flag set for the contained launch, with
// breakaway included only where it will not be refused.
func appContainerCreationFlags() uintptr {
	flags := uintptr(EXTENDED_STARTUPINFO_PRESENT | CREATE_UNICODE_ENVIRONMENT | syscall.CREATE_NEW_PROCESS_GROUP)
	if breakawayPermitted() {
		flags |= CREATE_BREAKAWAY_FROM_JOB
	}
	return flags
}
