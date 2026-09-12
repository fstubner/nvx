package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project policy's expose_ports reaches the merged policy.
//
// MergePolicies had a branch for every network list -- connect_ports,
// allow_hosts, default_allow -- except expose_ports. The field was declared,
// documented, read by the launcher and gated by policyLoosens, and a project
// file that set it did nothing: the merge dropped it, so the launcher never saw
// it and the gate could never fire. The one place it worked was the global
// policy, which is not where a project's dev-server port belongs.
func TestAProjectPolicysExposePortsAreMerged(t *testing.T) {
	global := DefaultPolicy()
	normalizePolicy(&global)
	local := Policy{}
	local.Isolation.Network.ExposePorts = []string{"3000", "5173:5173"}

	merged := MergePolicies(global, local)
	got := strings.Join(merged.Isolation.Network.ExposePorts, ",")
	if !strings.Contains(got, "3000") || !strings.Contains(got, "5173:5173") {
		t.Fatalf("expose_ports from the project policy were dropped by the merge: got %q", got)
	}
}

// And because it is now merged, an unpinned project file that adds one is a
// loosening and is ignored until approved -- the gate policyLoosens already
// had, made reachable.
func TestAnUnpinnedProjectFileCannotPublishAPort(t *testing.T) {
	tmp := tempDir(t)
	projectDir := filepath.Join(tmp, "project")
	nvxHome := filepath.Join(tmp, ".nvx")
	for _, d := range []string{projectDir, nvxHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"isolation": {"network": {"expose_ports": ["3000"]}}}`
	if err := os.WriteFile(filepath.Join(projectDir, ".nvx-policy.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadPolicy(nvxHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range loaded.Isolation.Network.ExposePorts {
		if strings.Contains(p, "3000") {
			t.Fatal("an unpinned project policy published a port onto the host's loopback with no prompt")
		}
	}
}
