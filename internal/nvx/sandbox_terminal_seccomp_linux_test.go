//go:build linux

package nvx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// What a contained process can do to the terminal nvx runs on. See
// applyLinuxTerminalSeccomp.

// terminalTypeMarker is what the target tries to type. The terminal echoes its
// input, so whatever gets through shows in the test's output.
const terminalTypeMarker = "nvx-typed-this\n"

// A contained process cannot type into the terminal nvx runs on, in open mode,
// which has no network filter, and in proxy mode, the default.
//
// The chain runs on a pseudo-terminal that is nvx's controlling terminal, as a
// user's terminal is. Without the filter, measured 2026-10-07 on WSL2 Linux
// 6.18 with dev.tty.legacy_tiocsti at 0, the kernel refused TIOCSTI with EIO and
// TIOCLINUX with ENOTTY. Only the filter answers EPERM. On a kernel that allows
// TIOCSTI the attempt succeeds without it, and the terminal echoes the marker.
func TestContainedProcessCannotTypeIntoTheTerminal(t *testing.T) {
	if b, err := os.ReadFile("/proc/sys/dev/tty/legacy_tiocsti"); err == nil {
		t.Logf("dev.tty.legacy_tiocsti is %s here", strings.TrimSpace(string(b)))
	} else {
		t.Logf("this kernel has no dev.tty.legacy_tiocsti: %v", err)
	}
	for _, network := range []string{"open", "proxy"} {
		t.Run(network, func(t *testing.T) {
			r := startTerminalRun(t, terminalOpts{mode: "tiocsti", terminal: true, network: network})
			got, ok := r.waitFile("tiocsti", 30*time.Second)
			if !ok {
				r.failOrSkip("the contained target never reported")
			}
			if !r.waitExit(10 * time.Second) {
				t.Errorf("the run was still going 10 seconds on\noutput:\n%s", r.out.String())
			}
			t.Logf("from the sandbox: %s", strings.Join(strings.Fields(got), " "))
			res := parseProbeResults(got)
			for _, req := range []string{"TIOCSTI", "TIOCLINUX"} {
				if res[req] != "EPERM" {
					t.Errorf("%s from the sandbox returned %s, want EPERM from nvx's filter\noutput:\n%s", req, res[req], r.out.String())
				}
			}
		})
	}
}

// The /proxy case of the injection test needs a network namespace, which an
// unprivileged Ubuntu 24.04 host refuses to configure (CAP_NET_ADMIN inside an
// unprivileged user namespace). The supervisor then fails closed with "Network
// isolation failed", the target never starts, and that must be a skip, not a
// failure, since the privileged CI step runs it under sudo where it works. The
// /open case has no namespace and skips on the mount-namespace refusal instead.
// A genuine startup failure must still fail. See failOrSkip.
func TestTerminalStartupSkipReasonRecognisesUnprivilegedRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		skip bool
	}{
		{
			"network namespace refused unprivileged",
			"✘ Network isolation failed (fail-closed): bring up loopback (install iproute2): " +
				"exit status 2: RTNETLINK answers: Operation not permitted",
			true,
		},
		{
			"mount namespace refused unprivileged",
			"could not unshare mount namespace: operation not permitted",
			true,
		},
		{
			"a genuine failure is not skipped",
			"panic: the target crashed on startup",
			false,
		},
		{
			"a network error that is not a permission refusal is not skipped",
			"Network isolation failed (fail-closed): bring up loopback: no such device",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, skip := terminalStartupSkipReason(tc.out)
			if skip != tc.skip {
				t.Errorf("terminalStartupSkipReason(%q) skip=%v, want %v", tc.out, skip, tc.skip)
			}
		})
	}
}

// terminalTypeInto is the target. It tries to type into its terminal, and into a
// pseudo-terminal of its own, and writes what each attempt returned.
func terminalTypeInto(dir string) int {
	var b strings.Builder
	fmt.Fprintf(&b, "TIOCSTI=%s\n", errnoName(typeInto(0, terminalTypeMarker)))
	sub := byte(6) // TIOCL_GETSHIFTSTATE, which only reads
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TIOCLINUX, uintptr(unsafe.Pointer(&sub)))
	fmt.Fprintf(&b, "TIOCLINUX=%s\n", errnoName(e))
	fmt.Fprintf(&b, "OWNPTY=%s\n", typeIntoOwnPty())
	if err := terminalWrite(filepath.Join(dir, "tiocsti"), []byte(b.String()), 0o600); err != nil {
		return 1
	}
	return 0
}

// typeInto pushes s into the input of the terminal open on fd, a byte at a time
// as TIOCSTI takes it, and returns the first refusal.
func typeInto(fd uintptr, s string) syscall.Errno {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSTI, uintptr(unsafe.Pointer(&c))); e != 0 {
			return e
		}
	}
	return 0
}

// typeIntoOwnPty opens a pseudo-terminal and types into it. Only the process
// that opened it reads it, so this reaches nothing outside the sandbox.
func typeIntoOwnPty() string {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return "none:" + strings.ReplaceAll(err.Error(), " ", "_")
	}
	defer master.Close()
	var unlock, n int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		return "unlock:" + errnoName(e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		return "name:" + errnoName(e)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return "slave:" + strings.ReplaceAll(err.Error(), " ", "_")
	}
	defer slave.Close()
	return errnoName(typeInto(slave.Fd(), "x"))
}

const terminalFilterChildEnv = "NVX_TEST_TERMINAL_FILTER_CHILD"

// The filter on the real kernel, refusing the two requests and nothing else.
// Installed in a subprocess, since a filter cannot be removed from the process
// that installs it.
//
// The requests go to a pipe. The kernel answers a terminal request on a pipe
// with ENOTTY, so EPERM there comes from the filter, whatever the kernel's own
// TIOCSTI policy and whoever runs the test.
//
// The kernel reads the request as 32 bits, so TIOCSTI with a high bit set is
// still TIOCSTI. A filter comparing all 64 bits lets it through.
func TestTerminalFilterRefusesTypingAndNothingElse(t *testing.T) {
	if os.Getenv(terminalFilterChildEnv) == "1" {
		if err := applyLinuxTerminalSeccomp(); err != nil {
			fmt.Printf("INSTALL_FAILED=%v\n", err)
			os.Exit(0)
		}
		r, w, err := os.Pipe()
		if err != nil {
			fmt.Printf("PIPE_FAILED=%v\n", err)
			os.Exit(0)
		}
		defer r.Close()
		defer w.Close()
		var c byte = 'x'
		var n int32
		var ws [4]uint16
		probe := func(name string, req uintptr, arg unsafe.Pointer) {
			_, _, e := syscall.Syscall(syscall.SYS_IOCTL, r.Fd(), req, uintptr(arg))
			fmt.Printf("%s=%s\n", name, errnoName(e))
		}
		high := uint64(1) << 32
		probe("tiocsti", syscall.TIOCSTI, unsafe.Pointer(&c))
		probe("tiocsti_high_bits", uintptr(high|syscall.TIOCSTI), unsafe.Pointer(&c))
		probe("tioclinux", syscall.TIOCLINUX, unsafe.Pointer(&c))
		probe("fionread", syscall.TIOCINQ, unsafe.Pointer(&n))
		probe("tiocgwinsz", syscall.TIOCGWINSZ, unsafe.Pointer(&ws))
		// The x32 ioctl, 514 in that table.
		if runtime.GOARCH == "amd64" {
			_, _, e := syscall.Syscall(x32SyscallBit|514, r.Fd(), syscall.TIOCSTI, uintptr(unsafe.Pointer(&c)))
			fmt.Printf("x32_ioctl=%s\n", errnoName(e))
		}
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), terminalFilterChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe subprocess failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))
	if msg, bad := got["INSTALL_FAILED"]; bad {
		t.Skipf("seccomp unavailable in this environment: %s", msg)
	}
	want := map[string]string{
		"tiocsti":           "EPERM",
		"tiocsti_high_bits": "EPERM",
		"tioclinux":         "EPERM",
		// ioctl still works, and other terminal requests reach the kernel.
		"fionread":   "ok",
		"tiocgwinsz": "ENOTTY",
	}
	if runtime.GOARCH == "amd64" {
		want["x32_ioctl"] = "EPERM"
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %s", name, got[name], w)
		}
	}
	if t.Failed() {
		t.Logf("probe output:\n%s", out)
	}
}
