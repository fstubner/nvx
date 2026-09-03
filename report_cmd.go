package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// `nvx report` collects, into one file, what someone would otherwise be asked to
// paste piece by piece.
//
// The case it is for: something behaved differently under nvx and the person who
// hit it is not the person who can diagnose it. Answering that needs the version,
// whether the shims are actually intercepting, which policy was in effect, and
// what nvx said at the time -- four commands and a file, or one command.
//
// It writes a file rather than printing, because the useful version of this is
// long, and because a file can be read before it is sent. Nothing is uploaded:
// there is no uploader in nvx and this does not add one.

const reportMaxLogLines = 200

func runReport(args []string) int {
	nvxHome := GetHomeDir()

	outPath := ""
	for _, a := range args {
		if strings.HasPrefix(a, "--out=") {
			outPath = strings.TrimPrefix(a, "--out=")
		}
	}
	if outPath == "" {
		outPath = fmt.Sprintf("nvx-report-%s.txt", time.Now().Format("20060102-150405"))
	}

	var b strings.Builder
	section := func(title string) { fmt.Fprintf(&b, "\n===== %s =====\n", title) }

	fmt.Fprintf(&b, "nvx report generated %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "nvx version: %s\n", appVersion)
	fmt.Fprintf(&b, "os/arch:     %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "nvx home:    %s\n", nvxHome)
	cwd, _ := os.Getwd()
	fmt.Fprintf(&b, "cwd:         %s\n", cwd)
	fmt.Fprintf(&b, "debug capture: %v (NVX_DEBUG)\n", debugLogEnabled())

	section("interception (nvx doctor)")
	b.WriteString(captureDoctor(nvxHome))

	section("policy in effect")
	if policy, err := LoadPolicy(nvxHome); err != nil {
		fmt.Fprintf(&b, "could not load policy: %v\n", err)
	} else {
		fmt.Fprintf(&b, "isolation enabled:      %v\n", policy.Isolation.Enabled)
		fmt.Fprintf(&b, "isolation level:        %s\n", policy.Isolation.Level)
		fmt.Fprintf(&b, "filesystem provider:    %s\n", policy.Isolation.Filesystem.Provider)
		fmt.Fprintf(&b, "network mode:           %s\n", policy.Isolation.Network.Mode)
		fmt.Fprintf(&b, "allow_read_exec:        %v\n", policy.Isolation.Filesystem.AllowReadExec)
		fmt.Fprintf(&b, "environment.allow:      %v\n", policy.Isolation.Environment.Allow)
		fmt.Fprintf(&b, "allow_hosts:            %v\n", policy.Isolation.Network.AllowHosts)
	}

	section(fmt.Sprintf("audit.log (last %d lines)", reportMaxLogLines))
	b.WriteString(tailFile(filepath.Join(nvxHome, "audit.log"), reportMaxLogLines))

	section(fmt.Sprintf("debug.log (last %d lines)", reportMaxLogLines))
	debugPath := debugLogPath(nvxHome)
	if _, err := os.Stat(debugPath); os.IsNotExist(err) {
		b.WriteString("no debug capture on this machine.\n" +
			"Re-run the command that misbehaved with NVX_DEBUG=1 set, then run 'nvx report' again.\n")
	} else {
		b.WriteString(tailFile(debugPath, reportMaxLogLines))
	}

	if err := os.WriteFile(outPath, []byte(b.String()), 0o600); err != nil {
		LogError("Could not write the report: %v", err)
		return 1
	}

	abs, err := filepath.Abs(outPath)
	if err != nil {
		abs = outPath
	}
	LogSuccess("Wrote %s", abs)
	LogWarn("It contains file paths, hostnames and package names from this machine, and if a debug capture was running, whatever nvx printed. Read it before sending it to anyone.")
	if !debugLogEnabled() {
		LogInfo("For the fullest picture, reproduce the problem with NVX_DEBUG=1 set and run this again.")
	}
	return 0
}

// captureDoctor runs the interception checks and returns what they printed.
//
// doctor is redirected for the duration rather than refactored to return text:
// the report wants exactly what a person running `nvx doctor` would see, and any
// second rendering of it is a copy that can drift.
//
// BOTH streams, which is not obvious and was wrong first: the Log* helpers write
// to stderr, but doctor's structure -- the heading and the [OK]/[FAIL] rows --
// goes to stdout. Capturing stderr alone put the advice in the report, left the
// verdicts on the terminal, and dropped the half a reader actually needs.
func captureDoctor(nvxHome string) string {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Sprintf("could not capture doctor output: %v\n", err)
	}
	realStdout, realStderr := os.Stdout, os.Stderr
	os.Stdout = w
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			sb.WriteString(stripANSI(sc.Text()))
			sb.WriteByte('\n')
		}
		done <- sb.String()
	}()

	runDoctor(nvxHome, false)

	os.Stdout, os.Stderr = realStdout, realStderr
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// stripANSI removes colour escapes so the report reads as plain text.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// tailFile returns the last n lines of a file, or a note saying why it cannot.
func tailFile(path string, n int) string {
	f, err := os.Open(path) // #nosec G304 -- a path nvx itself owns under nvxHome
	if err != nil {
		if os.IsNotExist(err) {
			return "(not present)\n"
		}
		return fmt.Sprintf("(could not read: %v)\n", err)
	}
	defer f.Close()

	ring := make([]string, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return fmt.Sprintf("(stopped reading: %v)\n", err)
	}
	if len(ring) == 0 {
		return "(empty)\n"
	}
	return strings.Join(ring, "\n") + "\n"
}
