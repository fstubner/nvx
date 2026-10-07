package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installStubNodes puts a stub Node.js for each version on disk and makes def the
// global default ("" for none).
func installStubNodes(t *testing.T, nvxHome, def string, versions ...string) {
	t.Helper()
	for _, v := range versions {
		writeStubBinary(t, nodeBinaryPath(filepath.Join(nvxHome, "versions", "node", v)))
	}
	if def != "" {
		if err := CreateLink(runtimeCurrentLinkPath(nvxHome, "node"), filepath.Join(nvxHome, "versions", "node", def)); err != nil {
			t.Fatal(err)
		}
	}
}

// An open range does not send the shim to the newest version installed.
//
// With the default at 22 and engines.node ">=18", the shim ran v26.10.0, the
// highest installed, because someone had once installed the newest release.
// Measured 2026-10-07. Anything in the range satisfies the project, and the
// default is the one the person already chose.
func TestAnOpenRangeKeepsTheDefaultWhenTheDefaultSatisfiesIt(t *testing.T) {
	nvxHome := tempDir(t)
	installStubNodes(t, nvxHome, "v22.1.0", "v20.1.0", "v22.1.0", "v22.9.0", "v26.1.0")
	node := NodeProvider{}

	for _, tc := range []struct{ query, want, why string }{
		{">=18", "v22.1.0", "the default satisfies an open range"},
		{">=18 <27", "v22.1.0", "two bounds are still open"},
		{"^20 || ^22", "v22.1.0", "alternatives, and the default is one of them"},
		{"^22", "v22.1.0", "a caret range, and the default is in it"},
		{">=24", "v26.1.0", "the default is not in the range, so the newest that is"},
		{"^20", "v20.1.0", "only one version is in the range"},
		// A partial version means the newest installed match, as in nvm.
		{"22", "v22.9.0", "a partial version is not a range"},
		{"v22", "v22.9.0", "a partial version is not a range"},
		{"22.1.0", "v22.1.0", "an exact version"},
	} {
		got := sessionRuntimeVersion(nvxHome, node, projectPin{query: tc.query, source: filepath.Join(nvxHome, "package.json")})
		if got != tc.want {
			t.Errorf("a project asking for %q ran %s, want %s (%s)", tc.query, got, tc.want, tc.why)
		}
	}
}

// Without a default there is nothing to keep, and a default that is gone is none.
func TestAnOpenRangeFallsBackToTheNewestWithoutAUsableDefault(t *testing.T) {
	node := NodeProvider{}
	pin := projectPin{query: ">=18", source: "package.json"}

	noDefault := tempDir(t)
	installStubNodes(t, noDefault, "", "v22.1.0", "v26.1.0")
	if got := sessionRuntimeVersion(noDefault, node, pin); got != "v26.1.0" {
		t.Errorf("with no default the shim ran %s, want the newest installed, v26.1.0", got)
	}

	gone := tempDir(t)
	installStubNodes(t, gone, "v20.1.0", "v20.1.0", "v26.1.0")
	if err := os.RemoveAll(filepath.Join(gone, "versions", "node", "v20.1.0")); err != nil {
		t.Fatal(err)
	}
	if got := sessionRuntimeVersion(gone, node, pin); got != "v26.1.0" {
		t.Errorf("with the default's directory deleted the shim ran %s, want v26.1.0", got)
	}
}

// `nvx use` and the cd hook read the same file and must agree with the shim.
func TestUseAndAutoAgreeWithTheShimOnAnOpenRange(t *testing.T) {
	nvxHome := tempDir(t)
	installStubNodes(t, nvxHome, "v22.1.0", "v22.1.0", "v26.1.0")

	var code int
	out := captureStdout(t, func() {
		_ = captureStderrHere(t, func() { code = runUse(">=18", nvxHome, "bash", true) })
	})
	if code != 0 {
		t.Fatalf("nvx use \">=18\" exited %d", code)
	}
	if !strings.Contains(out, "v22.1.0") || strings.Contains(out, "v26.1.0") {
		t.Errorf("nvx use \">=18\" put the wrong version on PATH:\n%s", out)
	}

	project := tempDir(t)
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"engines":{"node":">=18"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	out = captureStdout(t, func() {
		_ = captureStderrHere(t, func() { runAuto(nvxHome, "bash") })
	})
	if !strings.Contains(out, "v22.1.0") || strings.Contains(out, "v26.1.0") {
		t.Errorf("the cd hook put the wrong version on PATH for engines >=18:\n%s", out)
	}
}
