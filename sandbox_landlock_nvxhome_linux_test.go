//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// nvxHomeProbePaths are the files a contained process must and must not be able
// to read under ~/.nvx. The sandbox needs the runtime trees to execute anything;
// it needs nothing else in there, and several of the rest are security state.
var nvxHomeProbePaths = []struct {
	key          string
	rel          string
	wantReadable bool
	why          string
}{
	{"runtime_binary", "versions/node/v20.0.0/bin/node", true, "the runtime must be executable or nothing runs"},
	{"shim_dir_entry", "bin/node", true, "PATH inside the sandbox still resolves nested node/npm through the shims"},
	{"tool_home_creds", "tool_home/abc123/.wrangler-token", false, "another tool's persisted credentials"},
	{"grants_file", "grants/deadbeef.json", false, "policy pins, approved egress hosts and trusted tools"},
	{"global_policy", "policy.json", false, "the trust baseline every project policy is compared against"},
	{"bin_cache", "cache/bin-resolve.json", false, "maps command names to absolute paths nvx later executes UNSANDBOXED"},
	{"other_sandbox_home", "sandbox_home/othersession/secret.txt", false, "a concurrent sandbox's guest home"},
}

// TestLandlockDoesNotExposeNvxHomeSecrets pins F26. applyLandlockSandbox granted
// read+exec over the whole of nvxHome, which is not just runtimes: it also holds
// tool_home (credentials a trusted tool persisted), grants/ (the pin store the
// policy-trust boundary depends on), policy.json, and cache/bin-resolve.json.
//
// Landlock is allowlist-only -- there are no deny rules -- so the fix is to grant
// the runtime trees rather than the whole directory.
func TestLandlockDoesNotExposeNvxHomeSecrets(t *testing.T) {
	if os.Getenv("NVX_TEST_NVXHOME_CHILD") == "1" {
		runNvxHomeProbeChild()
		return
	}

	if fd, err := landlockCreateRuleset(landlockAccessFull); err != nil {
		t.Skipf("landlock unavailable on this kernel: %v", err)
	} else {
		_ = syscall.Close(fd)
	}

	nvxHome := tempDir(t)
	for _, p := range nvxHomeProbePaths {
		full := filepath.Join(nvxHome, p.rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("probe-content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Use the REAL production layout: the guest home lives under
	// nvxHome/sandbox_home/<session>, whose parent is deliberately not granted.
	// Landlock rules are path-beneath and do not require rights on ancestors, but
	// a test using an unrelated temp dir would not prove that -- and getting it
	// wrong would break every launch while the narrowing test still passed.
	guestHome := filepath.Join(nvxHome, "sandbox_home", "thissession")
	if err := os.MkdirAll(guestHome, 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLandlockDoesNotExposeNvxHomeSecrets")
	cmd.Env = append(os.Environ(),
		"NVX_TEST_NVXHOME_CHILD=1",
		"NVX_TEST_NVXHOME="+nvxHome,
		"NVX_TEST_GUEST="+guestHome,
		"NVX_TEST_WORK="+tempDir(t),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe child failed: %v\noutput:\n%s", err, out)
	}
	got := parseProbeResults(string(out))
	if msg, bad := got["SETUP_FAILED"]; bad {
		t.Fatalf("sandbox setup failed in child: %s", msg)
	}

	for _, p := range nvxHomeProbePaths {
		want := "denied"
		if p.wantReadable {
			want = "readable"
		}
		if got[p.key] != want {
			t.Errorf("%s (%s): got %q, want %q -- %s", p.key, p.rel, got[p.key], want, p.why)
		}
	}

	// The control plane must also stay read-only, which is what keeps the
	// policy-trust boundary meaningful (see F64/F65 for what a writable
	// ~/.nvx buys an attacker on macOS).
	if got["global_policy_write"] != "denied" {
		t.Errorf("global_policy_write = %q, want denied: a contained process must never rewrite policy.json", got["global_policy_write"])
	}

	// ...while the guest home stays fully writable despite sitting under the
	// now-ungranted sandbox_home. Without this the narrowing would break the
	// sandbox's only writable location.
	if got["guest_home_write"] != "allowed" {
		t.Errorf("guest_home_write = %q, want allowed: the guest home must remain writable", got["guest_home_write"])
	}
}

func runNvxHomeProbeChild() {
	nvxHome := os.Getenv("NVX_TEST_NVXHOME")
	if err := applyLandlockSandbox(os.Getenv("NVX_TEST_GUEST"), os.Getenv("NVX_TEST_WORK"), nvxHome, nil); err != nil {
		fmt.Printf("SETUP_FAILED=%v\n", err)
		return
	}
	for _, p := range nvxHomeProbePaths {
		if _, err := os.ReadFile(filepath.Join(nvxHome, p.rel)); err == nil {
			fmt.Printf("%s=readable\n", p.key)
		} else {
			fmt.Printf("%s=denied\n", p.key)
		}
	}
	f, err := os.OpenFile(filepath.Join(nvxHome, "policy.json"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		_ = f.Close()
		fmt.Println("global_policy_write=allowed")
	} else {
		fmt.Println("global_policy_write=denied")
	}

	probe := filepath.Join(os.Getenv("NVX_TEST_GUEST"), "written-by-contained-process")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err == nil {
		fmt.Println("guest_home_write=allowed")
	} else {
		fmt.Printf("guest_home_write=denied (%v)\n", err)
	}
}
