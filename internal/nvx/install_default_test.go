package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installsVersion is Node whose install puts a stub runtime on disk, so
// runInstall can be driven without a download.
type installsVersion struct {
	NodeProvider
	version string
}

func (p installsVersion) Install(_ string, nvxHome string) error {
	dir := filepath.Join(nvxHome, "versions", "node", p.version)
	bin := nodeBinaryPath(dir)
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		return err
	}
	return os.WriteFile(bin, []byte("stub"), 0o700) // #nosec G306 -- fixture
}

// notQuiet turns -q off for a test about info lines, which -q hides. Other
// tests in the package leave it on.
func notQuiet(t *testing.T) {
	t.Helper()
	prev := quietFlag
	quietFlag = false
	t.Cleanup(func() { quietFlag = prev })
}

func installWith(t *testing.T, nvxHome, version string) string {
	t.Helper()
	orig := Providers["node"]
	Providers["node"] = installsVersion{version: version}
	notQuiet(t)
	t.Cleanup(func() { Providers["node"] = orig })
	return captureStderrHere(t, func() { runInstall(version, nvxHome) })
}

// The first install on a machine becomes the default.
//
// With no default set, the shim has no version to run, so `node` ran whatever
// unrelated system node was on PATH, or failed with "Could not find real
// executable for node" and no hint. Measured 2026-10-01 on a clean home.
func TestTheFirstInstalledVersionBecomesTheDefault(t *testing.T) {
	nvxHome := tempDir(t)
	out := installWith(t, nvxHome, "v22.1.0")
	if got := getGlobalDefaultVersion(nvxHome); got != "v22.1.0" {
		t.Fatalf("default after the first install = %q, want v22.1.0\n%s", got, out)
	}
	if !strings.Contains(out, "default") {
		t.Errorf("the install did not say it set the default:\n%s", out)
	}
}

// A later install leaves the default alone and says how to switch.
func TestALaterInstallKeepsTheDefaultAndSaysHowToSwitch(t *testing.T) {
	nvxHome := tempDir(t)
	installWith(t, nvxHome, "v20.1.0")
	out := installWith(t, nvxHome, "v22.1.0")
	if got := getGlobalDefaultVersion(nvxHome); got != "v20.1.0" {
		t.Fatalf("a second install moved the default to %q", got)
	}
	for _, want := range []string{"nvx use v22.1.0", "nvx default v22.1.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Installing the version that is already the default has nothing to say about
// switching.
func TestReinstallingTheDefaultSaysNothingAboutSwitching(t *testing.T) {
	nvxHome := tempDir(t)
	installWith(t, nvxHome, "v22.1.0")
	out := installWith(t, nvxHome, "v22.1.0")
	if strings.Contains(out, "nvx use") || strings.Contains(out, "nvx default") {
		t.Errorf("hint printed for the version that is already the default:\n%s", out)
	}
}
