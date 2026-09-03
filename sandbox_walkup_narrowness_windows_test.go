//go:build windows

package main

// The walk-up preload answers a denied stat with a directory's stats. Its
// header promises it does that only for the ancestors of the sandbox's own
// working directory and home, and only for a permission error. Both halves
// were stated and neither was tested: an independent audit forced
// isCoveredAncestor to `return true` -- which makes the shim fabricate stats
// for ANY denied path anywhere, including one deliberately hidden from a
// contained process -- and the entire suite, containment probes included,
// stayed green.
//
// This asserts the predicates directly in node, because that is where they
// run. An end-to-end version would need a path that yields EPERM outside both
// chains, which on a normal account means writing a deny ACE somewhere no test
// should be writing.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realNodeForTest returns a node.exe that is not an nvx shim, or skips.
func realNodeForTest(t *testing.T) string {
	t.Helper()
	nvxHome := GetHomeDir()
	rt := runtimeForShim("node")
	ver := getActiveShellVersionFor(nvxHome, rt.Name())
	if ver == "" {
		ver = getGlobalDefaultVersionFor(nvxHome, rt.Name())
	}
	if p := resolvePinnedCommandPath("node", nvxHome, ver, rt); p != "" {
		return p
	}
	p, err := lookPathSkippingNvxShims("node", nvxHome)
	if err != nil {
		t.Skipf("no node available to evaluate the preload: %v", err)
	}
	return p
}

func TestWalkUpShimAnswersOnlyForItsOwnAncestors(t *testing.T) {
	node := realNodeForTest(t)

	// The chains the shim is allowed to answer for are taken from cwd and the
	// HOME/USERPROFILE it is given, so the test picks both and derives the
	// paths it expects to be accepted and refused from them.
	guestHome := tempDir(t)
	workDir := tempDir(t)
	shim, err := writeWalkupShim(guestHome)
	if err != nil {
		t.Fatal(err)
	}

	// A directory that is emphatically not an ancestor of either chain.
	outsider := `C:\Windows\System32`
	if isPathStrictlyUnder(workDir, outsider) || isPathStrictlyUnder(guestHome, outsider) {
		t.Skipf("this machine's temp dirs live under %s, so it is not an outsider here", outsider)
	}

	probe := filepath.Join(workDir, "narrowness.js")
	script := `
const shim = require(process.argv[2]);
const path = require('path');
const answer = {};
for (const p of JSON.parse(process.argv[3])) {
  answer[p] = shim.isCoveredAncestor(p);
}
answer['__enoent_is_not_permission'] = shim.isPermissionError({ code: 'ENOENT' });
answer['__eperm_is_permission'] = shim.isPermissionError({ code: 'EPERM' });
console.log(JSON.stringify(answer));
`
	if err := os.WriteFile(probe, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ancestors of the two chains, which the shim exists to answer for.
	workParent := filepath.Dir(workDir)
	homeParent := filepath.Dir(guestHome)
	// Paths it must refuse: the chain endpoints themselves (not ancestors), a
	// sibling, and somewhere else entirely.
	sibling := filepath.Join(filepath.Dir(workDir), "some-other-project")
	paths := []string{workParent, homeParent, workDir, guestHome, sibling, outsider, `C:\Users\Public`}
	encoded, _ := json.Marshal(paths)

	cmd := exec.Command(node, probe, shim, string(encoded))
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "USERPROFILE="+guestHome, "HOME="+guestHome, "NODE_OPTIONS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evaluating the preload failed: %v\n%s", err, out)
	}
	line := strings.TrimSpace(string(out))
	if i := strings.LastIndex(line, "{"); i > 0 {
		line = line[i:]
	}
	var got map[string]bool
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("could not read the preload's answers: %v\n%s", err, out)
	}

	mustAnswer := []string{workParent, homeParent}
	mustRefuse := []string{workDir, guestHome, sibling, outsider, `C:\Users\Public`}

	for _, p := range mustAnswer {
		if !got[p] {
			t.Errorf("the preload refuses %s, an ancestor of its own working directory or home; "+
				"the walk it exists to unblock would still fail there", p)
		}
	}
	for _, p := range mustRefuse {
		if got[p] {
			t.Errorf("the preload would answer a denied stat for %s, which is not an ancestor of its "+
				"working directory or home. A path deliberately hidden from a contained process would "+
				"be reported as an ordinary directory.", p)
		}
	}
	if got["__enoent_is_not_permission"] {
		t.Error("ENOENT is treated as a permission error; the preload would invent a directory that does not exist")
	}
	if !got["__eperm_is_permission"] {
		t.Error("EPERM is not treated as a permission error; the preload would answer nothing and npx would fail")
	}
}
