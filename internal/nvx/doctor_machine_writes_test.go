package nvx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nvx doctor --fix` makes two writes that a throwaway NVX_HOME does not contain:
// the persistent PATH, and the shell profile. Both are machine-wide, and both
// have been performed for real by the test suite.
//
// The PATH one was found first and given a seam. The profile one was not, and
// kept writing. Redirecting HOME does not reliably contain it: on Windows
// profilePathFor asks pwsh for $PROFILE, and where that lands depends on the
// host. A GitHub runner's pwsh follows USERPROFILE, so a redirected home does
// contain it; on a machine whose Documents folder is redirected to OneDrive it
// does not, and the answer is the developer's own profile. A test cannot tell
// which kind of host it is running on, so it stubs.
//
// Tests that did not stub wrote CI's profile. The Windows job's unit-test step
// logged
//
//	✔ Added the shell integration to C:\Users\runneradmin\Documents\PowerShell\Microsoft.PowerShell_profile.ps1
//
// after which every later pwsh step in that job printed "The term 'nvx' is not
// recognized" on startup, loading a line a test had planted.
//
// One caution when reading those logs: the success line is printed after the
// write call returns, so a stubbed run prints it too. Measured -- a stubbed run
// logged the line and created no file. It says which test reaches the write, and
// is never evidence that a write happened.

// stubProfileWrite makes the shell-profile write a no-op for one test.
func stubProfileWrite(t *testing.T) {
	t.Helper()
	restore := addIntegrationToProfile
	addIntegrationToProfile = func(string, string) error { return nil }
	t.Cleanup(func() { addIntegrationToProfile = restore })
}

// stubMachineWideWrites stops both of them, for a test that only wants what
// doctor writes inside the nvx home.
func stubMachineWideWrites(t *testing.T) {
	t.Helper()
	restorePath := repairPersistentPath
	repairPersistentPath = func(string, bool) (bool, error) { return false, nil }
	t.Cleanup(func() { repairPersistentPath = restorePath })
	stubProfileWrite(t)
}

// allowRealProfileWrite marks a test that lets the write run on purpose.
//
// One test has to, or the repair could be deleted rather than fixed and
// everything here would still pass. It does nothing at runtime; its value is
// that the guard below sees it, so the exception is declared in the test that
// takes it instead of being a filename the guard skips. The old guard exempted
// this whole file, and that exemption is exactly how a second unstubbed caller
// could hide next to a stubbed one.
//
// Taking it means accepting responsibility for where the write lands: pin SHELL
// so the path resolves inside the test's own temp home.
func allowRealProfileWrite(t *testing.T) {
	t.Helper()
}

// TestDoctorFixStillWritesTheIntegration keeps the repair alive.
//
// Named for what it checks. An earlier name said it proved nothing was written
// outside the test home, which it does not and cannot -- see below.
//
// HOME and USERPROFILE are redirected and SHELL is pinned to bash, so
// profilePathFor resolves inside the temp tree on every platform. The write runs
// for real here -- the seam is NOT stubbed -- which is what keeps the repair from
// being removed rather than fixed.
//
// What this asserts is exactly that: --fix still repairs. It does NOT assert that
// nothing outside the home was written, and an earlier version of it claimed to.
// The claim was false and measurably so: bypassing the seam entirely, so
// reportShellIntegration called the real write, left this test green, because the
// SHELL pin above means the PowerShell branch never runs and the path resolves
// into the temp home either way. The pin that makes the test safe is the same pin
// that makes it blind to the defect.
func TestDoctorFixStillWritesTheIntegration(t *testing.T) {
	allowRealProfileWrite(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("MSYSTEM", "")
	t.Setenv("NVX_SHELL_INTEGRATION", "")

	// The PATH repair stays stubbed: it is the other machine-wide write and has
	// its own coverage. Only the profile write runs for real here.
	restorePath := repairPersistentPath
	repairPersistentPath = func(string, bool) (bool, error) { return false, nil }
	t.Cleanup(func() { repairPersistentPath = restorePath })

	runDoctor(tempDir(t), true)

	profile := filepath.Join(home, ".bashrc")
	body, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("doctor --fix wrote no profile at %s: %v.\n"+
			"The repair is gone, or the seam that makes it stubbable swallowed it.", profile, err)
	}
	if !strings.Contains(string(body), "nvx env") {
		t.Errorf("the profile in the test home does not load nvx:\n%s", body)
	}
}

// There is deliberately no test asserting where the profile path lands relative
// to a redirected home. It is host-dependent, and an earlier version of this file
// asserted "outside" and failed CI for exactly that reason: on a windows-latest
// runner profilePathFor resolved INSIDE the redirected home
// (...\Temp\Test...\001\Documents\PowerShell\...), because that pwsh follows
// USERPROFILE, while on this developer's machine it resolved to the OneDrive
// Documents folder outside it. Either assertion is false on the other host.
//
// The seam and the caller check below are what hold regardless of which host runs
// them, which is why the fix rests on those rather than on a fact about pwsh.

// profileWriteHelpers are the calls that settle a test's obligation.
var profileWriteHelpers = map[string]bool{
	"stubProfileWrite":      true,
	"stubMachineWideWrites": true,
	"allowRealProfileWrite": true,
}

// TestEveryDoctorFixCallerStubsTheProfileWrite reads the test sources.
//
// A guard on behaviour cannot catch the next test that forgets, because a
// forgotten stub writes a file this package never looks at and nothing fails.
// The failure is invisible by construction, so the check has to be on the source
// rather than on a run -- the same reason the skip-reason allowlist in CI reads
// log text rather than trusting an exit code.
//
// Parsed rather than pattern-matched, after a review found the regex version
// wrong in two ways that were then reproduced:
//
//   - `runDoctor\([^)]*,\s*true\)` cannot cross a nested paren, so
//     runDoctor(tempDir(t), true) -- this suite's ordinary spelling, used on line
//     91 above -- matched zero times. An unstubbed caller written that way passed.
//   - The stub was looked for anywhere in the FILE, so one stubbing test excused
//     every other caller beside it. A second unstubbed caller added to
//     doctor_readonly_test.go passed for that reason.
//
// Both were measured before this rewrite, and both are re-checked by the
// sabotage cases in the repository's review notes rather than asserted here.
//
// Known limit, stated rather than papered over: the second argument has to be
// the literal true. A caller that computes `fix := true` and passes the variable
// is not recognised. Nothing in the package does that today, and pinning it
// would mean evaluating expressions this guard has no business evaluating.
func TestEveryDoctorFixCallerStubsTheProfileWrite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	callers := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		// SkipObjectResolution and a tolerated parse failure, matching
		// TestEveryRefusalReasonIsALiteral: a file that will not parse on its own
		// is the compiler's problem to report, not this guard's, and failing here
		// would turn an unrelated syntax error into a confusing profile-write
		// complaint.
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			fixLines, settled := inspectFixCalls(fset, fn)
			callers += len(fixLines)
			if settled {
				continue
			}
			for _, line := range fixLines {
				t.Errorf("%s:%d: %s calls runDoctor(_, true) without settling the profile write.\n"+
					"On a host whose $PROFILE sits outside the test's home, that appends nvx's "+
					"integration line to the real one, and no assertion in this package would notice.\n"+
					"Call stubProfileWrite(t) or stubMachineWideWrites(t); or allowRealProfileWrite(t) "+
					"if the write is the point, having pinned SHELL so it lands in the test's own home.",
					name, line, fn.Name.Name)
			}
		}
	}

	// Without this the guard passes by finding nothing -- after a rename, or when
	// the working directory is not the package (a `go test -c` binary run
	// elsewhere). A check that cannot fail is worse than no check, because it
	// reads as coverage.
	if callers == 0 {
		t.Fatal("no runDoctor(_, true) callers found in this package.\n" +
			"Either they are gone, or this guard is no longer looking at the sources it thinks it is.")
	}
}

// inspectFixCalls reports the lines in fn that call runDoctor(_, true), and
// whether fn settles its obligation.
//
// Scoped to the function, which is the fix for the file-wide search the previous
// version did.
func inspectFixCalls(fset *token.FileSet, fn *ast.FuncDecl) (lines []int, settled bool) {
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if profileWriteHelpers[ident.Name] {
			settled = true
			return true
		}
		if ident.Name == "runDoctor" && len(call.Args) == 2 {
			if arg, ok := call.Args[1].(*ast.Ident); ok && arg.Name == "true" {
				lines = append(lines, fset.Position(call.Pos()).Line)
			}
		}
		return true
	})
	return lines, settled
}

// TestASubprocessDoctorFixPinsTheShell covers the one way left to write a real
// profile.
//
// A test that runs the BUILT binary cannot use the seam -- the stub lives in this
// process, the write happens in another -- so shell_profile_test.go is protected
// only by pinning SHELL=/bin/bash in the child's environment, which makes the
// profile path resolve under the redirected HOME on every platform. Drop that pin
// on Windows and the child writes the developer's own $PROFILE, with the parent
// asserting nothing about it.
//
// Deliberately coarse: it checks that a file running the binary with --fix also
// sets SHELL, not that the two appear in the same invocation. A finer check would
// need to follow the value into cmd.Env, and the failure it is guarding against
// is someone copying this test and dropping the line, which the coarse form
// catches.
func TestASubprocessDoctorFixPinsTheShell(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		if !strings.Contains(src, "execCommandForTest") || !strings.Contains(src, `"--fix"`) {
			continue
		}
		checked++
		if !strings.Contains(src, "SHELL=") {
			t.Errorf("%s runs the built nvx with --fix but never pins SHELL in the child's environment.\n"+
				"On Windows the child then resolves $PROFILE outside the test's home and writes it.", name)
		}
	}
	if checked == 0 {
		t.Skip("no test runs the built binary with --fix")
	}
}
