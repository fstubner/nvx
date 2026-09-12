package nvx

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A refusal still reads as errSandboxDidNotStart.
//
// Every caller that only wants the machine-readable fact -- the command did not
// run contained -- goes through errors.Is, and attaching a reason must not
// change that answer.
func TestARefusalIsStillTheSentinel(t *testing.T) {
	err := refusedToStart("the appcontainer profile was unavailable")
	if !errors.Is(err, errSandboxDidNotStart) {
		t.Fatal("a refusal no longer matches errSandboxDidNotStart, so callers checking for it would treat the run as contained")
	}
	if err.Error() != "the appcontainer profile was unavailable" {
		t.Fatalf("the reason did not survive: %q", err.Error())
	}
}

// Every reason handed to refusedToStart is a literal.
//
// The reason reaches ~/.nvx/audit.log, and this is the rule LogWarn already
// follows for the same destination: a rendered error can carry runtime data --
// a package URL with credentials in it is the case that put a live password in
// that file once -- and a literal chosen at the call site cannot.
//
// Checked by parsing rather than by grep, so a multi-line call or a renamed
// variable cannot slip past.
func TestEveryRefusalReasonIsALiteral(t *testing.T) {
	files := goSourcesForAudit(t)
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatal(rerr)
		}
		// Parsed without build-tag filtering on purpose: the Windows, Linux and
		// macOS launchers each hold refusal sites, and a check that only saw the
		// current platform's would pass while another platform logged a rendered
		// error.
		f, perr := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if perr != nil {
			continue // not parseable on its own; the compiler is the judge of that
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "refusedToStart" || len(call.Args) != 1 {
				return true
			}
			checked++
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: refusedToStart is given something other than a string literal; "+
					"the reason goes to the audit log, where a rendered error can carry a URL with credentials in it",
					fset.Position(call.Pos()))
			}
			return true
		})
	}
	// The count is asserted because a check that found nothing would pass
	// silently, which is how this stops covering anything the day the helper is
	// renamed.
	if checked < 15 {
		t.Errorf("only %d refusal sites found; the launchers hold more than that, so this check has stopped seeing them", checked)
	}
}

// The reasons are distinct enough to tell the paths apart.
//
// The point of the change is that a log full of one string cannot answer "why
// did containment not start", so two sites sharing a reason is worth knowing
// about.
//
// Counted per file, which is per platform, because that is the scope a log has:
// one machine writes one platform's reasons, so the same wording on Windows and
// on macOS confuses nobody. Three launchers can fail to open a path to a host
// service and say so in the same words; two branches of the SAME launcher doing
// it is what this catches. Windows grants its writable roots from two branches
// that fail identically, so two is allowed and three is not.
func TestRefusalReasonsAreMostlyDistinct(t *testing.T) {
	files, _ := filepath.Glob("sandbox_native_*.go")
	fset := token.NewFileSet()
	total := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		f, perr := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if perr != nil {
			continue
		}
		perFile := map[string]int{}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "refusedToStart" || len(call.Args) != 1 {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok {
				perFile[lit.Value]++
				total[lit.Value] = true
			}
			return true
		})
		for reason, n := range perFile {
			if n > 2 {
				t.Errorf("%s: %d sites share the reason %s, so its log cannot tell them apart", name, n, reason)
			}
		}
	}
	if len(total) < 10 {
		t.Fatalf("expected the launchers to name at least ten distinct causes, found %d: %v", len(total), total)
	}
}
