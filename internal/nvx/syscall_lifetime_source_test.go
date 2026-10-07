package nvx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Memory that Windows reads through an address stored somewhere the garbage
// collector does not look is kept alive with runtime.KeepAlive, never with a
// blank assignment.
//
// `_ = attrBuf` kept nothing alive, because Go drops a blank assignment. The
// launch attributes could be freed before CreateProcess read them, and under
// forced collection 7 of 1000 launches started the command outside the
// AppContainer (measured 2026-10-07). Two shapes carry that risk here: the
// blank assignment itself, and a permission list attached to a security
// descriptor, which holds the list's address as bytes. This reads the source
// for both, so a later edit cannot quietly bring either back.
func TestAddressesHandedToWindowsAreKeptAliveByHand(t *testing.T) {
	blank := regexp.MustCompile(`(?m)^\s*_ = [A-Za-z_][A-Za-z0-9_]*\s*$`)
	files, err := filepath.Glob("*_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		if m := blank.FindString(src); m != "" && strings.Contains(src, "unsafe.Pointer") {
			t.Errorf("%s: %q next to unsafe code. A blank assignment keeps nothing alive; "+
				"use runtime.KeepAlive after the call that reads the memory.", f, strings.TrimSpace(m))
		}
		for _, body := range strings.Split(src, "\nfunc ")[1:] {
			if strings.Contains(body, "procSetSecurityDescriptorDacl.Call(") &&
				!strings.Contains(body, "runtime.KeepAlive(") {
				name, _, _ := strings.Cut(body, "(")
				t.Errorf("%s: %s attaches a permission list to a descriptor without runtime.KeepAlive; "+
					"the descriptor holds the list's address as bytes", f, name)
			}
		}
	}
}
