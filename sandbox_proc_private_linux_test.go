//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// A contained process can read its own /proc, and cannot see the host's.
//
// This is the behavioural half of the fix TestProcIsGrantedOnlyWithAPrivateMount
// pins structurally. That test reads the Landlock rule list and checks the grant
// travels with the private mount -- two statements in two files agreeing. It
// never mounts anything, never launches anything, and would pass unchanged if
// the mount itself stopped working.
//
// What went wrong needed the running thing. Bun reads /proc/self to size its
// stack, every read under /proc inside the sandbox returned EACCES, and the
// symptoms were nothing like a permission error: `bun script.js` aborted, and
// `bun install` reported "JSON document is too deeply nested" and "StackOverflow"
// against a 65-byte package.json. `bun --version` worked throughout, so the
// obvious smoke check was the one case that passed. Node never reads /proc, which
// is why only Bun was affected and why this went unnoticed until someone tried it.
//
// Both halves are asserted together, because either alone is satisfiable by
// something broken:
//
//   - /proc/self must be READABLE. Granting nothing there is what broke Bun.
//   - the host's processes must be INVISIBLE. Granting the host's /proc would
//     have fixed Bun by handing contained code /proc/<pid>/environ for every
//     program the user is running, which is where credentials live -- worse than
//     the bug.
//
// A test that only checked the first would pass on the naive fix that was
// explicitly rejected.
//
// Bun itself is deliberately NOT installed here. The mechanism is the /proc
// mount, not Bun, and requiring an 80MB download would make this a network test
// that CI would end up skipping -- which is how the gap arose in the first place.
// The Bun case is recorded in the CHANGELOG and was reproduced by hand on
// 2026-09-10 against a real bun 1.4.2: script, `bun install` and `--version` all
// clean under the current build.
func TestContainedProcessReadsItsOwnProcAndNotTheHosts(t *testing.T) {
	if os.Getenv("NVX_TEST_PROC_CHILD") == "1" {
		runPrivateProcChild()
		return
	}

	// The same namespaces platformLaunchNative creates, from the same helper, so
	// this exercises the real arrangement rather than a copy of it.
	attr := supervisorSysProcAttr("proxy")
	requireNamespaceSupport(t, attr)

	// A process outside the sandbox that must not be visible from inside it.
	// Started here, in the parent, so it is genuinely a host process rather than
	// one of the child's own.
	host := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := host.Start(); err != nil {
		t.Fatalf("start the host process this test looks for: %v", err)
	}
	defer func() {
		_ = host.Process.Kill()
		_, _ = host.Process.Wait()
	}()

	cmd := exec.Command(os.Args[0], "-test.run=TestContainedProcessReadsItsOwnProcAndNotTheHosts")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_PROC_CHILD=1",
		"NVX_TEST_HOST_PID="+strconv.Itoa(host.Process.Pid),
	)
	cmd.SysProcAttr = attr

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("contained child failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))

	for _, want := range []struct{ key, val, why string }{
		{"mount", "ok", "the sandbox must get a procfs of its own; without it the grant is withheld and Bun cannot run"},
		{"self_status", "readable", "/proc/self/status must be readable -- this is the read Bun makes to size its stack"},
		{"self_maps", "readable", "/proc/self/maps must be readable for the same reason"},
		{"host_pid_visible", "no", "a host process must not appear in the sandbox's /proc; its environ is where credentials live"},
	} {
		if got[want.key] != want.val {
			t.Errorf("%s = %q, want %q -- %s\nfull output:\n%s", want.key, got[want.key], want.val, want.why, out)
		}
	}
}

// runPrivateProcChild executes as PID 1 of a fresh PID namespace, mounts the
// private procfs the supervisor mounts, and reports what it can see.
func runPrivateProcChild() {
	if err := mountPrivateProc(); err != nil {
		fmt.Printf("mount=%v\n", err)
		return
	}
	fmt.Println("mount=ok")

	for _, probe := range []struct{ name, path string }{
		{"self_status", "/proc/self/status"},
		{"self_maps", "/proc/self/maps"},
	} {
		if _, err := os.ReadFile(probe.path); err != nil {
			fmt.Printf("%s=%v\n", probe.name, err)
			continue
		}
		fmt.Printf("%s=readable\n", probe.name)
	}

	// The host process is named by PID. Inside a private procfs on a fresh PID
	// namespace its directory should not exist at all -- and a PID that merely
	// collides with one of our own would be a false pass, which is why the parent
	// starts a process it can name rather than picking an arbitrary number.
	hostPID := os.Getenv("NVX_TEST_HOST_PID")
	if _, err := os.Stat("/proc/" + hostPID); err == nil {
		fmt.Println("host_pid_visible=yes")
	} else {
		fmt.Println("host_pid_visible=no")
	}
}
