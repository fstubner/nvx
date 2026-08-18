//go:build windows

package main

// Adversarial probe (NVX_PROBE=1): does one sandbox session get access to
// another's guest home, or to a trusted tool's persisted credentials?
//
// Two facts combine. First, the AppContainer profile is deliberately STABLE
// (`stableSandboxProfile`, "so its SID is a durable target for `nvx setup`
// grants") — so every sandbox session on the machine runs as the same SID.
// Second, access is granted with icacls and never revoked: prepareAppContainerFilesystem
// adds an inheritable (M) ACE for that SID on each guest home, and
// grantWorkdirAncestors adds traverse rights on the chain above it.
//
// If both hold as written, then an ACE granted for one session is still present
// during the next, and the SID that satisfies it is the same one. A contained
// install would then be able to read a concurrent session's guest home — and,
// worse, any persistent tool profile ever granted, which is where a trusted tool
// stores the credentials it was granted persistence for (wrangler tokens, gh
// auth). The Linux sandbox excludes exactly this: its Landlock rules grant
// versions/, bin/ and current/ rather than nvxHome, specifically so tool_home and
// other sessions' guest homes stay out of reach.
//
// This settles whether the Windows side has the same property.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOneSandboxSessionCannotReadAnother(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	if os.Getenv("NVX_CROSS_SESSION_CHILD") == "1" {
		for _, spec := range strings.Split(os.Getenv("NVX_PROBE_TARGETS"), "|") {
			parts := strings.SplitN(spec, "=", 2)
			if len(parts) != 2 {
				continue
			}
			if b, err := os.ReadFile(parts[1]); err != nil {
				fmt.Printf("%s=DENIED\n", parts[0])
			} else {
				fmt.Printf("%s=READ:%s\n", parts[0], strings.TrimSpace(string(b)))
			}
		}
		os.Exit(0)
	}

	// One profile for both sessions, which is what production does: the profile
	// is stable by design, so every session shares this SID.
	const probeProfile = "nvx.sandbox.crosssession"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	// A stand-in nvxHome laid out like the real one.
	nvxHome, err := os.MkdirTemp("", "nvxh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(nvxHome)

	victimHome := filepath.Join(nvxHome, "sandbox_home", "aaaaaaaaaaaaaaaa")
	attackerHome := filepath.Join(nvxHome, "sandbox_home", "bbbbbbbbbbbbbbbb")
	toolHome := filepath.Join(nvxHome, "tool_home", "cccccccccccccccc")
	for _, d := range []string{victimHome, attackerHome, toolHome} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	victimSecret := filepath.Join(victimHome, "victim-session-secret")
	toolSecret := filepath.Join(toolHome, ".wrangler-token")
	if err := os.WriteFile(victimSecret, []byte("OTHER-SESSION-DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolSecret, []byte("PERSISTED-TOOL-CREDENTIAL"), 0o600); err != nil {
		t.Fatal(err)
	}

	victimWork := t.TempDir()
	attackerWork := t.TempDir()

	// Session 1: a concurrent sandbox. Session 2: a trusted tool with a
	// persistent profile, granted exactly as ensurePersistentGuestProfile's
	// caller would. Both happened before the attacker's session starts.
	if err := prepareAppContainerFilesystem(sid, victimHome, victimWork); err != nil {
		t.Fatalf("victim session prep: %v", err)
	}
	if err := prepareAppContainerFilesystem(sid, toolHome, victimWork); err != nil {
		t.Fatalf("tool session prep: %v", err)
	}

	// Session 3: an ordinary `npm install` in an unrelated project.
	if err := prepareAppContainerFilesystem(sid, attackerHome, attackerWork); err != nil {
		t.Fatalf("attacker session prep: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(attackerHome, "probe.exe")
	if err := os.WriteFile(childExe, data, 0o700); err != nil {
		t.Fatal(err)
	}

	read, write := makeTestPipe(t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))

	targets := "OTHERSESSION=" + victimSecret + "|TOOLCRED=" + toolSecret
	env := append(scrubEnvironment(attackerHome),
		"NVX_PROBE=1", "NVX_CROSS_SESSION_CHILD=1",
		"NVX_PROBE_TARGETS="+targets,
	)
	_, launchErr := launchAppContainerProcess(childExe,
		[]string{"-test.run=TestOneSandboxSessionCannotReadAnother"},
		env, attackerWork, sid, 0, nil)

	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readProbeOutput(t, read)

	requireAppContainerLaunch(t, launchErr)
	t.Logf("child output:\n%s", got)

	// This pins the CURRENT, UNWANTED state rather than the state we want.
	//
	// Measured 2026-08-18: both are readable. Every session shares the stable
	// AppContainer SID and icacls grants are never revoked, so an ACE added for one
	// session is still present and still satisfied during the next. The persistent
	// tool credential is the worse half: that store exists precisely to hold what a
	// trusted tool authenticated with, and any later `npm install` inherits access.
	//
	// Linux does not have this property -- its Landlock rules grant versions/, bin/
	// and current/ rather than nvxHome, specifically to keep tool_home and other
	// sessions out of reach. This is a Windows-only gap in the same boundary.
	//
	// If this test ever fails because the access is gone, that is good news: update
	// README.md, SECURITY.md, docs/enforcement-matrix.md and this test together.
	if !strings.Contains(got, "OTHERSESSION=READ") {
		t.Error("another session's guest home is no longer readable -- the documented limitation no " +
			"longer holds, so update README.md, SECURITY.md, docs/enforcement-matrix.md and this test")
	}
	if !strings.Contains(got, "TOOLCRED=READ") {
		t.Error("a persistent tool credential is no longer readable -- update the documents named above and this test")
	}
	if !strings.Contains(got, "OTHERSESSION=") || !strings.Contains(got, "TOOLCRED=") {
		t.Errorf("inconclusive result:\n%s", got)
	}
	t.Log("CONFIRMED (unwanted): a sandboxed process reads other sessions' guest homes and persisted tool credentials")
}
