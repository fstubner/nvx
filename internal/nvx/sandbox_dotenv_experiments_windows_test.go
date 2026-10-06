//go:build windows

package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// TestWindowsDotenvProtectionExperiments (NVX_PROBE=1) measures why the deny ACEs
// (TestDenyACEHidesSecretFromAppContainer) and the integrity label
// (TestIntegrityLabelHidesSecretFromAppContainer) did not hide .env from the
// sandbox, and which protection does. Every experiment runs on a throwaway
// project and .env under %TEMP% and launches the same contained child, which
// reports its own token before it reads.
//
// What it found, 2026-10-06, Windows 11 26300:
//   - The reader is an AppContainer process at Low integrity whose token carries
//     the project capability. .env's allow is that capability's entry, inherited
//     from the project folder.
//   - A deny for the package SID and ALL APPLICATION PACKAGES names other
//     identities. A deny for the capability itself does not hold either, so a deny
//     entry cannot be the mechanism. A deny for the user's own SID does stop the read.
//   - What stops the read is .env's permission list not containing the capability
//     (or ALL APPLICATION PACKAGES) at all: protected, with the other entries kept.
//   - An editor that saves by replacing the file brings the inherited entry back.
//   - The integrity label is enforced against a plain Low integrity process and not
//     against the AppContainer child.

// readAllWithTimeout drains the child's pipe. readWithTimeout does one 256-byte
// read, which cuts a token report off mid-line.
func readAllWithTimeout(t *testing.T, read syscall.Handle) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			var n uint32
			if err := syscall.ReadFile(read, buf, &n, nil); err != nil || n == 0 {
				break
			}
			sb.Write(buf[:n])
		}
		done <- sb.String()
	}()
	select {
	case s := <-done:
		return s
	case <-time.After(20 * time.Second):
		return ""
	}
}

// probeChildOutputLine returns the values of every "key=" line in out.
func probeChildOutputLines(out, key string) []string {
	var vals []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, key+"=") {
			vals = append(vals, strings.TrimSpace(strings.TrimPrefix(line, key+"=")))
		}
	}
	return vals
}

type dotenvFixture struct {
	t         *testing.T
	sid       uintptr
	guestHome string
	workDir   string
	secret    string
	normal    string
	caps      []string
	childExe  string
}

func newDotenvFixture(t *testing.T, sid uintptr) *dotenvFixture {
	t.Helper()
	f := &dotenvFixture{t: t, sid: sid, guestHome: tempDir(t), workDir: tempDir(t)}
	f.secret = filepath.Join(f.workDir, ".env")
	f.normal = filepath.Join(f.workDir, "package.json")
	if err := os.WriteFile(f.secret, []byte("API_KEY=super-secret-value-12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.normal, []byte(`{"name":"victim"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	f.caps, _, err = prepareAppContainerFilesystem(sid, "", f.guestHome, f.workDir)
	if err != nil {
		t.Fatalf("filesystem prep: %v", err)
	}
	f.childExe = stageProbeChild(t, f.guestHome, "dotenvrca.exe")
	return f
}

// run launches the contained child and returns everything it printed.
func (f *dotenvFixture) run(extraEnv ...string) string {
	f.t.Helper()
	read, write := makeTestPipe(f.t)
	defer syscall.CloseHandle(read)
	prevOut, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	const stdOutputHandle = uintptr(0xFFFFFFF5)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(write))
	env := append(scrubEnvironment(f.guestHome),
		"NVX_PROBE=1",
		"NVX_SECRET_PROBE_CHILD=1",
		"NVX_PROBE_SECRET="+f.secret,
		"NVX_PROBE_NORMAL="+f.normal,
		"NVX_PROBE_WRITE="+filepath.Join(f.workDir, "node_modules_marker"),
	)
	env = append(env, extraEnv...)
	_, launchErr := launchAppContainerProcess(f.childExe,
		[]string{"-test.run=TestDenyACEHidesSecretFromAppContainer"},
		env, f.workDir, f.sid, 0, f.caps)
	procSetStdHandleTest.Call(stdOutputHandle, uintptr(prevOut))
	syscall.CloseHandle(write)
	got := readAllWithTimeout(f.t, read)
	requireAppContainerLaunch(f.t, launchErr)
	return got
}

func (f *dotenvFixture) secretVerdict(out string) string {
	for _, v := range probeChildOutputLines(out, "SECRET") {
		if strings.HasPrefix(v, "READ:") {
			return "READ"
		}
		return v
	}
	return "NO-RESULT"
}

func (f *dotenvFixture) icacls(args ...string) {
	f.t.Helper()
	out, err := runWinCmd(30*time.Second, "icacls", append([]string{f.secret}, args...)...)
	if err != nil {
		f.t.Fatalf("icacls %v: %v (%s)", args, err, strings.TrimSpace(string(out)))
	}
}

// developerCanRead reports whether this (unsandboxed) process still reads .env.
func (f *dotenvFixture) developerCanRead() bool {
	b, err := os.ReadFile(f.secret)
	return err == nil && strings.HasPrefix(string(b), "API_KEY=")
}

var s1152 = regexp.MustCompile(`^S-1-15-[23]-`)

func TestWindowsDotenvProtectionExperiments(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run (creates a throwaway AppContainer profile)")
	}
	const probeProfile = "nvx.sandbox.dotenvrca"
	sid, err := ensureAppContainerSID(probeProfile)
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)
	pkg, err := appContainerSidToString(sid)
	if err != nil {
		t.Fatal(err)
	}

	// Q1 and Q2: who reads, and through which identity does the allow come.
	t.Run("Q1_Q2_baseline_identity", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		out := f.run()
		t.Logf("child output:\n%s", out)
		t.Logf("SDDL of .env:\n%s", sddlOf(f.secret))
		t.Logf("project capability SID (the one nvx grants the project to): %s", f.caps[0])
		t.Logf("package SID: %s", pkg)
		if got := probeChildOutputLines(out, "TOKEN_APPCONTAINER"); len(got) != 1 || got[0] != "1" {
			t.Errorf("the reading process is not an AppContainer process: %v", got)
		}
		caps := strings.Join(probeChildOutputLines(out, "TOKEN_CAPS"), ",")
		if !strings.Contains(caps, f.caps[0]) {
			t.Errorf("the child's token does not carry the project capability %s: %s", f.caps[0], caps)
		}
		if f.secretVerdict(out) != "READ" {
			t.Errorf("baseline: .env was not readable (%s), nothing to protect", f.secretVerdict(out))
		}
		dacl := sddlOf(f.secret)
		if !strings.Contains(dacl, f.caps[0]) {
			t.Errorf("the project capability is not in .env's permissions, so the allow comes through another SID")
		}
	})

	// Q3a: the deny the existing probe applied names the package SID and ALL
	// APPLICATION PACKAGES. It does not name the capability the allow comes through.
	t.Run("Q3_deny_capability_only", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		f.icacls("/deny", "*"+f.caps[0]+":(R)")
		out := f.run()
		t.Logf("SDDL of .env:\n%s", sddlOf(f.secret))
		t.Logf("child SECRET verdict with the capability SID denied: %s", f.secretVerdict(out))
		// Measured 2026-10-06: the deny names the SID the allow comes through, and the
		// child still reads. The deny is not what is missing from the earlier probe.
		if v := f.secretVerdict(out); v != "READ" {
			t.Errorf("a deny for the project capability now gives %s; Windows honours it, so the earlier deny probe was only naming the wrong SIDs", v)
		}
	})

	// Q3b: deny every sandbox identity the child's token carries (package,
	// capabilities, ALL APPLICATION PACKAGES), found from the token itself.
	t.Run("Q3_deny_every_sandbox_sid_in_token", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		base := f.run()
		var sids []string
		seen := map[string]bool{}
		add := func(s string) {
			s = strings.SplitN(s, "/", 2)[0]
			if s1152.MatchString(s) && !seen[s] {
				seen[s] = true
				sids = append(sids, s)
			}
		}
		for _, c := range strings.Split(strings.Join(probeChildOutputLines(base, "TOKEN_CAPS"), ","), ",") {
			add(c)
		}
		for _, g := range probeChildOutputLines(base, "TOKEN_GROUP") {
			add(g)
		}
		add(strings.Join(probeChildOutputLines(base, "TOKEN_PKGSID"), ""))
		t.Logf("S-1-15 SIDs in the child's token: %v", sids)
		for _, s := range sids {
			f.icacls("/deny", "*"+s+":(R)")
		}
		out := f.run()
		t.Logf("SDDL of .env:\n%s", sddlOf(f.secret))
		t.Logf("child SECRET verdict: %s", f.secretVerdict(out))
		if v := f.secretVerdict(out); v != "READ" {
			t.Errorf("deny for every S-1-15 SID in the token now gives %s; a deny entry works, so update the findings", v)
		}
	})

	// Q3c: control. The deny the earlier probe applied (package SID + ALL
	// APPLICATION PACKAGES) must still leave the file readable.
	t.Run("Q3_control_package_and_AAP_only", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		f.icacls("/deny", "*"+pkg+":(R)")
		f.icacls("/deny", "*S-1-15-2-1:(R)")
		out := f.run()
		t.Logf("child SECRET verdict: %s", f.secretVerdict(out))
		if v := f.secretVerdict(out); v != "READ" {
			t.Errorf("control changed: package + ALL APPLICATION PACKAGES deny now gives %s", v)
		}
	})

	// Q3 variants: which deny entries does Windows honour for this child? A deny for
	// the user's own SID is the control that proves the deny syntax works at all.
	t.Run("Q3_deny_variants", func(t *testing.T) {
		userSID := ""
		{
			f := newDotenvFixture(t, sid)
			userSID = strings.SplitN(probeChildOutputLines(f.run(), "TOKEN_USER")[0], "/", 2)[0]
		}
		// Each case is a list of icacls argument lists, run in order on a fresh
		// .env. "CAP" and "PKG" stand for the project capability and package SID.
		cases := []struct {
			name      string
			steps     [][]string
			wantChild string
		}{
			{"inherited allow, deny capability (F)", [][]string{{"/deny", "*CAP:(F)"}}, "READ"},
			{"inherited allow, deny package SID (F)", [][]string{{"/deny", "*PKG:(F)"}}, "READ"},
			{"inherited allow, deny user SID (R), the control", [][]string{{"/deny", "*" + userSID + ":(R)"}}, "DENIED"},
			{"protect, then deny capability (F): icacls drops the capability's explicit allow", [][]string{{"/inheritance:d"}, {"/deny", "*CAP:(F)"}}, "DENIED"},
			{"deny capability (F), then protect: deny and allow for the same capability both present", [][]string{{"/deny", "*CAP:(F)"}, {"/inheritance:d"}}, "READ"},
			{"protect, then deny package SID (F)", [][]string{{"/inheritance:d"}, {"/deny", "*PKG:(F)"}}, "READ"},
			{"protect, then deny ALL APPLICATION PACKAGES (F)", [][]string{{"/inheritance:d"}, {"/deny", "*S-1-15-2-1:(F)"}}, "READ"},
		}
		for _, c := range cases {
			f := newDotenvFixture(t, sid)
			for _, step := range c.steps {
				args := make([]string, len(step))
				for i, a := range step {
					args[i] = strings.ReplaceAll(strings.ReplaceAll(a, "CAP", f.caps[0]), "PKG", pkg)
				}
				f.icacls(args...)
			}
			out := f.run()
			t.Logf("%s: child SECRET=%s, developer reads .env: %v\n%s", c.name, f.secretVerdict(out), f.developerCanRead(), sddlOf(f.secret))
			if v := f.secretVerdict(out); v != c.wantChild {
				t.Errorf("%s: child verdict %s, previously measured %s", c.name, v, c.wantChild)
			}
		}
	})

	// Q4: protect .env's permission list and leave out the entries that grant the
	// sandbox. The inherited entries are copied in as explicit ones first, so the
	// developer keeps exactly what they had.
	t.Run("Q4_protect_and_drop_sandbox_grant", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		before := sddlOf(f.secret)
		f.icacls("/inheritance:d")
		f.icacls("/remove:g", "*"+f.caps[0])
		after := sddlOf(f.secret)
		t.Logf("SDDL before:\n%s\nSDDL after:\n%s", before, after)
		out := f.run("NVX_PROBE_TAMPER=1")
		t.Logf("child output:\n%s", out)
		if v := f.secretVerdict(out); v != "DENIED" {
			t.Errorf("protected .env without the capability entry: child verdict %s", v)
		}
		if !contains(out, "NORMAL=READ:") || !contains(out, "WRITE=OK") {
			t.Errorf("the rest of the project stopped working: %q", out)
		}
		for _, k := range []string{"TAMPER_WRITE", "TAMPER_RENAME", "TAMPER_DELETE"} {
			t.Logf("%s from inside the sandbox: %v", k, probeChildOutputLines(out, k))
		}
		if _, err := os.Stat(f.secret); err != nil {
			t.Logf("the sandbox removed or moved .env (%v)", err)
		}
	})

	// Q4 continued: the developer's own access, and an editor saving by replacing
	// the file.
	t.Run("Q4_developer_and_editor_replace", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		f.icacls("/inheritance:d")
		f.icacls("/remove:g", "*"+f.caps[0])
		if !f.developerCanRead() {
			t.Fatalf("developer cannot read the protected .env")
		}
		// In-place edit, which is what most editors and `echo >> .env` do.
		if err := os.WriteFile(f.secret, []byte("API_KEY=edited-in-place-1"), 0o600); err != nil {
			t.Fatalf("developer cannot edit in place: %v", err)
		}
		inPlace := f.run()
		t.Logf("after an in-place edit the sandbox verdict is %s; capability in SDDL: %v",
			f.secretVerdict(inPlace), strings.Contains(sddlOf(f.secret), f.caps[0]))
		if v := f.secretVerdict(inPlace); v != "DENIED" {
			t.Errorf("an in-place edit changed the protection: %s", v)
		}

		// Replace-on-save: write a sibling, rename it over .env.
		tmp := f.secret + ".tmp"
		if err := os.WriteFile(tmp, []byte("API_KEY=edited-by-replace-2"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, f.secret); err != nil {
			t.Fatalf("developer cannot replace .env: %v", err)
		}
		replaced := f.run()
		inSDDL := strings.Contains(sddlOf(f.secret), f.caps[0])
		t.Logf("after replace-on-save the sandbox verdict is %s; capability in SDDL: %v",
			f.secretVerdict(replaced), inSDDL)
		if f.secretVerdict(replaced) != "READ" || !inSDDL {
			t.Errorf("replace-on-save kept the protection (verdict %s, capability in SDDL %v); the note about re-applying it is wrong",
				f.secretVerdict(replaced), inSDDL)
		}

		// What nvx would do before each launch: apply the same protection again.
		f.icacls("/inheritance:d")
		f.icacls("/remove:g", "*"+f.caps[0])
		again := f.run()
		t.Logf("after re-applying the protection the sandbox verdict is %s", f.secretVerdict(again))
		if v := f.secretVerdict(again); v != "DENIED" {
			t.Errorf("re-applying the protection did not restore it: %s", v)
		}
		if !f.developerCanRead() {
			t.Errorf("the developer cannot read .env after re-applying")
		}
	})

	// Q4 continued: a project under a folder that grants ALL APPLICATION PACKAGES
	// read access (the profile tree does for some folders) has a second way in. The
	// protected list has to leave that entry out as well.
	t.Run("Q4_all_application_packages_entry_must_go_too", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		if out, err := runWinCmd(30*time.Second, "icacls", f.workDir, "/grant", "*S-1-15-2-1:(OI)(CI)(R)"); err != nil {
			t.Fatalf("grant AAP read on the project: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		f.icacls("/inheritance:d")
		f.icacls("/remove:g", "*"+f.caps[0])
		viaAAP := f.run()
		t.Logf("capability entry gone, ALL APPLICATION PACKAGES read entry still there: SECRET=%s", f.secretVerdict(viaAAP))
		f.icacls("/remove:g", "*S-1-15-2-1")
		both := f.run()
		t.Logf("both entries gone: SECRET=%s, developer reads .env: %v", f.secretVerdict(both), f.developerCanRead())
		if f.secretVerdict(viaAAP) != "READ" {
			t.Errorf("with an AAP read entry left, the child verdict is %s; the note that it must be removed is wrong", f.secretVerdict(viaAAP))
		}
		if f.secretVerdict(both) != "DENIED" {
			t.Errorf("with the capability and AAP entries gone, the child verdict is %s", f.secretVerdict(both))
		}
	})

	// Q4 continued: nvx writes the project grant again from time to time. Inherited
	// entries are pushed down to every child that still inherits, so the question is
	// whether a protected .env is left alone by that push.
	t.Run("Q4_project_regrant_leaves_protected_env_alone", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		f.icacls("/inheritance:d")
		f.icacls("/remove:g", "*"+f.caps[0])
		if err := revokeACL(f.workDir, f.caps[0]); err != nil {
			t.Fatal(err)
		}
		if err := grantSandboxModify(f.caps[0], f.workDir); err != nil {
			t.Fatal(err)
		}
		out := f.run()
		t.Logf("after revoking and re-granting the project directory: SECRET=%s, NORMAL read=%v, capability in .env SDDL: %v",
			f.secretVerdict(out), contains(out, "NORMAL=READ:"), strings.Contains(sddlOf(f.secret), f.caps[0]))
		if v := f.secretVerdict(out); v != "DENIED" {
			t.Errorf("re-granting the project directory exposed a protected .env: %s", v)
		}
		if !contains(out, "NORMAL=READ:") || !contains(out, "WRITE=OK") {
			t.Errorf("the rest of the project stopped working: %q", out)
		}
	})

	// Q5: the integrity label. A process that is not an AppContainer, at Low
	// integrity, is the control: if the label stops it and not the AppContainer
	// child, the label is not consulted for AppContainer processes.
	t.Run("Q5_label_low_integrity_control", func(t *testing.T) {
		f := newDotenvFixture(t, sid)
		if err := setFileLabelSDDL(f.secret, "S:(ML;;NWNR;;;ME)"); err != nil {
			t.Fatal(err)
		}
		t.Logf("SDDL of .env (owner O:, label S:):\n%s", sddlOf(f.secret))
		ac := f.run()
		t.Logf("AppContainer child: SECRET=%s TOKEN_IL=%v TOKEN_MANDATORY_POLICY=%v TOKEN_OWNER=%v",
			f.secretVerdict(ac), probeChildOutputLines(ac, "TOKEN_IL"),
			probeChildOutputLines(ac, "TOKEN_MANDATORY_POLICY"), probeChildOutputLines(ac, "TOKEN_OWNER"))

		low := f.runLowIntegrityChild("NVX_PROBE_APPEND=NORMALAPPEND=" + f.normal)
		t.Logf("Low-integrity non-AppContainer child with the same label:\n%s", low)
		if f.secretVerdict(low) != "DENIED" {
			t.Errorf("control: a Low-integrity process read a Medium NR-labelled file (%s); the label is not enforced for it either",
				f.secretVerdict(low))
		}
		if f.secretVerdict(ac) != "READ" {
			t.Errorf("the AppContainer child is now stopped by the label: %s", f.secretVerdict(ac))
		}

		// Does the label bind the AppContainer child for writes either? The
		// default label on a file is Medium with no-write-up.
		wr := f.run("NVX_PROBE_APPEND=NORMALAPPEND=" + f.normal)
		t.Logf("AppContainer child appending to package.json (default Medium label): %v",
			probeChildOutputLines(wr, "NORMALAPPEND"))
	})
}

// runLowIntegrityChild runs the probe child as a plain Low-integrity process (no
// AppContainer) and returns what it printed.
func (f *dotenvFixture) runLowIntegrityChild(extraEnv ...string) string {
	f.t.Helper()
	var self syscall.Token
	proc, _, _ := procGetCurrentProcess.Call()
	if r, _, e := procOpenProcessToken.Call(proc, uintptr(TOKEN_DUPLICATE|TOKEN_QUERY|TOKEN_ADJUST_DEFAULT|TOKEN_ASSIGN_PRIMARY),
		uintptr(unsafe.Pointer(&self))); r == 0 {
		f.t.Skipf("OpenProcessToken: %v", e)
	}
	defer syscall.CloseHandle(syscall.Handle(self))
	const maximumAllowed, securityImpersonation, tokenPrimary = 0x02000000, 2, 1
	var dup syscall.Token
	if r, _, e := modAdvapi32.NewProc("DuplicateTokenEx").Call(uintptr(self), maximumAllowed, 0,
		securityImpersonation, tokenPrimary, uintptr(unsafe.Pointer(&dup))); r == 0 {
		f.t.Skipf("DuplicateTokenEx: %v", e)
	}
	defer syscall.CloseHandle(syscall.Handle(dup))
	low, err := sidFromString("S-1-16-4096")
	if err != nil {
		f.t.Fatal(err)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(low)))
	type tokenMandatoryLabel struct {
		sid  *byte
		attr uint32
	}
	label := tokenMandatoryLabel{sid: low, attr: 0x20} // SE_GROUP_INTEGRITY
	if r, _, e := modAdvapi32.NewProc("SetTokenInformation").Call(uintptr(dup), 25,
		uintptr(unsafe.Pointer(&label)), unsafe.Sizeof(label)+12); r == 0 { // 12 is GetLengthSid of S-1-16-4096
		f.t.Skipf("SetTokenInformation(IL): %v", e)
	}
	// The child must live where a Low process can run it.
	cmd := exec.Command(f.childExe, "-test.run=TestDenyACEHidesSecretFromAppContainer")
	cmd.Env = append(os.Environ(),
		"NVX_PROBE=1", "NVX_SECRET_PROBE_CHILD=1",
		"NVX_PROBE_SECRET="+f.secret, "NVX_PROBE_NORMAL="+f.normal,
		"NVX_PROBE_WRITE="+filepath.Join(f.workDir, "node_modules_marker"))
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Dir = f.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: dup}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "LAUNCH-ERR " + err.Error() + "\n" + string(out)
	}
	return string(out)
}
