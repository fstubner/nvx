//go:build windows

package nvx

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A CheckNetIsolation.exe earlier in PATH is not the one nvx runs.
//
// The mechanical test in system_tool_choke_point_test.go proves nothing is
// launched by name. This proves the consequence on a real machine: a
// user-writable directory put first in PATH, holding a program called
// CheckNetIsolation.exe, and nvx's loopback-exemption check run against it.
// Before the fix the planted program ran and its output was believed. Under
// `nvx setup` the same call is made elevated.
//
// The planted program is this test binary, which TestMain turns into a
// stand-in for any tool when NVX_TEST_FAKE_TOOL_OUTPUT is set: it prints that
// and exits. The sentinel is a package SID in the format the real tool uses,
// so if the planted program runs, its SID comes back from the parser.
func TestAPlantedSystemToolOnPathIsNotTheOneRun(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(tempDir(t), "CheckNetIsolation.exe")
	if err := os.Link(self, planted); err != nil {
		// Different volume: copy instead.
		src, err := os.Open(self)
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Create(planted)
		if err != nil {
			src.Close()
			t.Fatal(err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			t.Fatal(err)
		}
		src.Close()
		dst.Close()
	}

	const sentinel = "S-1-15-2-4242424242-1-1-1-1-1-1-1"
	t.Setenv("PATH", filepath.Dir(planted)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NVX_TEST_FAKE_TOOL_OUTPUT",
		"List Loopback Exempted AppContainers\n"+
			"[1] -----------------------------------------------------------------\n"+
			"    Name: planted.by.test\n"+
			"    SID:  "+sentinel+"\n\n")

	sids, err := listLoopbackExemptSIDs()
	if err != nil {
		t.Fatalf("the loopback-exemption check failed outright: %v", err)
	}
	for _, s := range sids {
		if strings.EqualFold(s, sentinel) {
			t.Fatalf("the CheckNetIsolation.exe planted first in PATH was the one nvx ran and believed. "+
				"Elevated, that is an Administrator running a user-writable file. Got SIDs %v", sids)
		}
	}
}

// And the resolver itself lands in the system directory, for a tool that is
// there and errors for one that is not, rather than falling back to PATH.
func TestSystemToolPathIsUnderTheSystemDirectory(t *testing.T) {
	p, err := systemToolPath("cmd.exe")
	if err != nil {
		t.Fatalf("cmd.exe should resolve: %v", err)
	}
	lower := strings.ToLower(p)
	if !strings.Contains(lower, `\system32\`) || !filepath.IsAbs(p) {
		t.Errorf("cmd.exe resolved to %q, want an absolute path under the system directory", p)
	}
	if _, err := systemToolPath("nvx-no-such-tool-please.exe"); err == nil {
		t.Error("a tool that does not exist in the system directory resolved; PATH must not be a fallback")
	}
}
