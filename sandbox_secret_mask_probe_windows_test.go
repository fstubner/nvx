//go:build windows

package main

// Opt-in probe (NVX_PROBE=1): can a deny ACE keep a contained process out of .env
// while leaving the rest of the working directory usable?
//
// Measured on 2026-08-18: a postinstall script running inside the sandbox read
// .env from the project directory and printed it, because prepareAppContainerFilesystem
// grants the AppContainer SID (M) on the whole working directory and .env lives
// there. Scrubbing environment VARIABLES does not help -- the secret is a FILE.
//
// npm needs package.json, the lockfile and node_modules; it does not need .env. So
// the question is whether the grant can be carved out rather than made wholesale.
// Windows evaluates an explicit deny ACE before an inherited allow, which should
// make this work with the mechanism nvx already uses -- but "should" is why this
// probe exists rather than a patch.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDenyACEHidesSecretFromAppContainer(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}

	// Child: report what it can and cannot reach.
	if os.Getenv("NVX_SECRET_PROBE_CHILD") == "1" {
		read := func(label, path string) {
			b, err := os.ReadFile(path)
			if err != nil {
				fmt.Printf("%s=DENIED\n", label)
				return
			}
			fmt.Printf("%s=READ:%s\n", label, strings.TrimSpace(string(b)))
		}
		read("SECRET", os.Getenv("NVX_PROBE_SECRET"))
		read("NORMAL", os.Getenv("NVX_PROBE_NORMAL"))
		// npm must still be able to create files in the project (node_modules).
		if err := os.WriteFile(os.Getenv("NVX_PROBE_WRITE"), []byte("x"), 0o600); err != nil {
			fmt.Printf("WRITE=DENIED\n")
		} else {
			fmt.Printf("WRITE=OK\n")
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.secretprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)

	secret := filepath.Join(workDir, ".env")
	normal := filepath.Join(workDir, "package.json")
	if err := os.WriteFile(secret, []byte("API_KEY=super-secret-value-12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(normal, []byte(`{"name":"victim"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// The grant nvx already applies: (M) over the whole working directory.
	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	// The proposed carve-out. Explicit deny on the file itself, which Windows
	// evaluates ahead of the inherited allow from the directory grant.
	sidStr, err := appContainerSidToString(sid)
	if err != nil {
		t.Fatal(err)
	}
	// Denying the container's own SID alone was measured NOT to work: an
	// AppContainer process also carries the well-known ALL APPLICATION PACKAGES
	// group (S-1-15-2-1), and the user profile tree grants that, so the read
	// still succeeded through the surviving allow. Deny both.
	for _, target := range []string{"*" + sidStr + ":(R)", "*S-1-15-2-1:(R)"} {
		out, err := runWinCmd(20*time.Second, "icacls", secret, "/deny", target)
		if err != nil {
			t.Fatalf("deny ACE %s: %v (%s)", target, err, strings.TrimSpace(string(out)))
		}
	}
	t.Logf("applied deny ACEs for %s and ALL APPLICATION PACKAGES on .env", sidStr)

	childExe := stageProbeChild(t, guestHome, "secretprobe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1",
		"NVX_SECRET_PROBE_CHILD=1",
		"NVX_PROBE_SECRET="+secret,
		"NVX_PROBE_NORMAL="+normal,
		"NVX_PROBE_WRITE="+filepath.Join(workDir, "node_modules_marker"),
	)
	exitCode, launchErr := launchAppContainerProcess(
		childExe,
		[]string{"-test.run=TestDenyACEHidesSecretFromAppContainer"},
		env, workDir, sid, 0, scopeCaps,
	)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readWithTimeout(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child exit=%d output=%q", exitCode, got)

	// This pins the CURRENT, UNPROTECTED state rather than the state we want.
	//
	// Deny ACEs do not work here. Measured 2026-08-18, both ways round: denying the
	// container's own SID left .env readable, and additionally denying ALL
	// APPLICATION PACKAGES (S-1-15-2-1) left it readable too. Why is not yet
	// understood -- an AppContainer process runs as the user, and the user's own
	// allow on a user-owned file appears to carry the read regardless of the
	// package-SID deny.
	//
	// So: a contained process CAN read .env, and README.md's claim that "a bad
	// package can't quietly read your .env" is false on Windows. If this test ever
	// fails because the secret became unreadable, that is good news -- someone found
	// a mechanism that works. Update the README and this test together.
	if contains(got, "SECRET=READ:") {
		t.Log("CONFIRMED (unwanted): a contained process reads .env from the project directory; deny ACEs do not prevent it")
	} else if contains(got, "SECRET=DENIED") {
		t.Error("`.env` is now unreadable from the sandbox -- the documented limitation no longer holds, so update README.md and this test")
	} else {
		t.Errorf("inconclusive secret result in %q", got)
	}

	// ...without breaking the directory npm actually needs.
	if !contains(got, "NORMAL=READ:") {
		t.Error("package.json became unreadable; the carve-out is too broad to be usable")
	}
	if !contains(got, "WRITE=OK") {
		t.Error("the contained process can no longer write to the project; npm install would fail")
	}
}

// TestContainedProcessCannotReachTheRealHome answers the question the .env finding
// raises: if a contained process can read the project directory, does the sandbox
// stop it reading anything at all?
//
// The classic credential stores -- ~/.ssh/id_rsa, ~/.aws/credentials, ~/.npmrc, the
// shell profile -- live in the REAL home, which the sandbox redirects away from and
// never grants. If that holds, the guarantee is "the project it is installing into,
// and nothing else", which is materially different from "no protection at all".
func TestContainedProcessCannotReachTheRealHome(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_HOME_PROBE_CHILD") == "1" {
		for _, spec := range strings.Split(os.Getenv("NVX_PROBE_TARGETS"), "|") {
			parts := strings.SplitN(spec, "=", 2)
			if len(parts) != 2 {
				continue
			}
			if _, err := os.ReadFile(parts[1]); err != nil {
				fmt.Printf("%s=DENIED\n", parts[0])
			} else {
				fmt.Printf("%s=READ\n", parts[0])
			}
		}
		os.Exit(0)
	}

	const probeProfile = "nvx.sandbox.homeprobe"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome := tempDir(t)
	workDir := tempDir(t)

	// Stand-ins for the real credential stores, placed in the actual user profile.
	home := os.Getenv("USERPROFILE")
	sshKey := filepath.Join(home, ".nvx-probe-id_rsa")
	awsCreds := filepath.Join(home, ".nvx-probe-aws-credentials")
	for _, p := range []string{sshKey, awsCreds} {
		if err := os.WriteFile(p, []byte("PRIVATE-KEY-MATERIAL"), 0o600); err != nil {
			t.Skipf("cannot stage %s: %v", p, err)
		}
		defer os.Remove(p)
	}
	inProject := filepath.Join(workDir, ".env")
	if err := os.WriteFile(inProject, []byte("API_KEY=x"), 0o600); err != nil {
		t.Fatal(err)
	}

	scopeCaps, _, err := prepareAppContainerFilesystem(sid, "", guestHome, workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}

	// stageProbeChild rather than an inline copy, because it treats a failure to
	// read the test binary as "this host cannot run the probe" instead of as a
	// product defect. Reading it intermittently returns "The handle is invalid"
	// on Windows -- observed once in four full probe runs -- and inline copies
	// turned that into a red suite with a message about a handle, which is
	// exactly the kind of random failure that teaches people to re-run CI rather
	// than read it. Every other probe already used the helper.
	childExe := stageProbeChild(t, guestHome, "homeprobe.exe")

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	targets := "SSHKEY=" + sshKey + "|AWSCREDS=" + awsCreds + "|PROJECTENV=" + inProject
	env := append(scrubEnvironment(guestHome),
		"NVX_PROBE=1", "NVX_HOME_PROBE_CHILD=1",
		"NVX_PROBE_TARGETS="+targets,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestContainedProcessCannotReachTheRealHome"},
		env, workDir, sid, 0, scopeCaps)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readWithTimeout(t, read)
	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output: %q", got)

	for _, key := range []string{"SSHKEY", "AWSCREDS"} {
		if contains(got, key+"=READ") {
			t.Errorf("%s in the real home was READABLE from inside the sandbox; containment provides far less than documented", key)
		} else if !contains(got, key+"=DENIED") {
			t.Errorf("inconclusive result for %s in %q", key, got)
		}
	}
	// The known, documented exposure -- pinned here so the contrast is explicit.
	if !contains(got, "PROJECTENV=READ") {
		t.Log("note: the project .env was NOT readable here, which would contradict the earlier probe")
	}
}
