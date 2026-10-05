package nvx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// `nvx use` reports failure when the shell did not change.
//
// Run from a shell without the integration, `nvx use 22` prints the environment
// nothing will evaluate, warns "this shell is unchanged", and exited 0 -- so a
// script doing `nvx use 22 && npm test` carried on with whatever runtime was
// already there. That is the same silent-failure shape `nvx auto` had, fixed
// there earlier: a command that says nothing changed must not also report
// success. Through the integration, which always passes --shell, the switch is
// evaluated and 0 is right.
func TestUseReportsFailureWhenTheShellIsUnchanged(t *testing.T) {
	nvxHome := tempDir(t)
	if err := os.MkdirAll(filepath.Join(nvxHome, "versions", "node", "v22.0.0"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NVX_SHELL_INTEGRATION", "")

	if code := runUse("22", nvxHome, "bash", false); code == 0 {
		t.Error("`nvx use 22` with nothing loading its environment reported success; `nvx use 22 && npm test` would run on the wrong runtime")
	}
	if code := runUse("22", nvxHome, "bash", true); code != 0 {
		t.Errorf("`nvx use 22 --shell=bash` from the integration exited %d; the switch is evaluated there and 0 is right", code)
	}
}

// noSuchReleaseNode is Node whose remote index has nothing for any query, and
// whose install records that it was asked.
type noSuchReleaseNode struct {
	NodeProvider
	installed *bool
}

func (noSuchReleaseNode) ResolveVersion(query string) (string, error) {
	return "", fmt.Errorf("%w matching query: %s", errNoReleaseFound, query)
}

func (n noSuchReleaseNode) Install(string, string) error {
	*n.installed = true
	return nil
}

// `nvx use 99` does not offer to install a version nobody has published. It
// asked "Would you like to download and install it now?" about Node.js 99.
func TestUseDoesNotOfferAVersionThatDoesNotExist(t *testing.T) {
	installed := false
	orig := Providers["node"]
	Providers["node"] = noSuchReleaseNode{installed: &installed}
	t.Cleanup(func() { Providers["node"] = orig })
	t.Setenv("NVX_YES", "1")

	if code := runUse("99", tempDir(t), "bash", true); code == 0 {
		t.Fatal("nvx use 99 succeeded")
	}
	if installed {
		t.Fatal("nvx use 99 went on to install a version that the index does not have")
	}
}
