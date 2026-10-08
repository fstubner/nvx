package nvx

import (
	"os"
	"strings"
	"testing"
)

// `nvx allow-host --remove` is the undo for `nvx allow-host`. Nothing else took a
// host back out. The only way was to edit the policy file, and no page said so.

// It takes the host out of the project's policy file, leaves the other host in,
// and the file is still trusted, because removing a host only narrows it.
func TestAllowHostRemoveTakesOneHostBackOut(t *testing.T) {
	home, policyPath := inTrustProject(t, "{\n  \"blocked_packages\": [\"left-pad\"]\n}\n")
	for _, h := range []string{"a.example.org", "b.example.org"} {
		if code := runAllowHost([]string{h}, home); code != 0 {
			t.Fatalf("nvx allow-host %s exited %d", h, code)
		}
	}

	if code := runAllowHost([]string{"--remove", "a.example.org"}, home); code != 0 {
		t.Fatalf("nvx allow-host --remove exited %d", code)
	}
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "a.example.org") || !strings.Contains(string(data), `"b.example.org:443"`) {
		t.Errorf("the wrong host was taken out:\n%s", data)
	}
	if !strings.Contains(string(data), "left-pad") {
		t.Errorf("the rest of the file was lost:\n%s", data)
	}
	if err := ensureProjectPolicyTrust(home); err != nil {
		t.Errorf("a trusted file stopped being trusted because a host came out of it: %v", err)
	}
	p, err := LoadPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	if containsFold(p.Isolation.Network.AllowHosts, "a.example.org:443") || !containsFold(p.Isolation.Network.AllowHosts, "b.example.org:443") {
		t.Errorf("the allowlist in force is wrong: %v", p.Isolation.Network.AllowHosts)
	}
	if rec := findRecord(readAuditRecords(t, home), "allow_host_removed", ""); rec == nil {
		t.Errorf("the removal is not in the audit log: %v", readAuditRecords(t, home))
	}
}

// The port counts, and a host that is not listed is a no-op that says so.
func TestAllowHostRemoveNeedsTheEntryAsListed(t *testing.T) {
	home, policyPath := inTrustProject(t, "")
	if code := runAllowHost([]string{"a.example.org:8443"}, home); code != 0 {
		t.Fatalf("nvx allow-host exited %d", code)
	}
	before, _ := os.ReadFile(policyPath)

	var code int
	out := captureStderrHere(t, func() { code = runAllowHost([]string{"--remove", "a.example.org"}, home) })
	if code != 0 {
		t.Errorf("removing a host that is not listed exited %d", code)
	}
	if !strings.Contains(out, "nothing to remove") {
		t.Errorf("it does not say there was nothing to remove:\n%s", out)
	}
	if after, _ := os.ReadFile(policyPath); string(after) != string(before) {
		t.Errorf("removing a host that is not listed changed the file:\n%s", after)
	}

	if code := runAllowHost([]string{"--remove", "A.Example.org:8443"}, home); code != 0 {
		t.Fatalf("nvx allow-host --remove exited %d", code)
	}
	if after, _ := os.ReadFile(policyPath); strings.Contains(string(after), "example.org") {
		t.Errorf("the host is still listed:\n%s", after)
	}
}

// Taking a host out of a file nobody trusted does not trust it. Trusting the
// file would trust whatever else it says.
func TestAllowHostRemoveDoesNotTrustAnUntrustedFile(t *testing.T) {
	home, policyPath := inTrustProject(t, `{"isolation":{"network":{"mode":"open","allow_hosts":["a.example.org:443"]}}}`)

	if code := runAllowHost([]string{"--remove", "a.example.org"}, home); code != 0 {
		t.Fatalf("nvx allow-host --remove exited %d", code)
	}
	if data, _ := os.ReadFile(policyPath); strings.Contains(string(data), "a.example.org") {
		t.Errorf("the host is still listed:\n%s", data)
	}
	if err := ensureProjectPolicyTrust(home); err != errUntrustedProjectPolicy {
		t.Errorf("removing a host trusted a file that loosens network.mode: %v", err)
	}
}

// --global does the same to ~/.nvx/policy.json.
func TestAllowHostRemoveGlobal(t *testing.T) {
	home, _ := inTrustProject(t, "")
	if code := runAllowHost([]string{"a.example.org", "--global"}, home); code != 0 {
		t.Fatalf("nvx allow-host --global exited %d", code)
	}
	if code := runAllowHost([]string{"--global", "--remove", "a.example.org"}, home); code != 0 {
		t.Fatalf("nvx allow-host --global --remove exited %d", code)
	}
	p, err := LoadPolicy(home)
	if err != nil {
		t.Fatal(err)
	}
	if containsFold(p.Isolation.Network.AllowHosts, "a.example.org:443") {
		t.Errorf("the host is still allowed: %v", p.Isolation.Network.AllowHosts)
	}
	if len(p.Isolation.Network.DefaultAllow) == 0 {
		t.Error("removing a host dropped the default allowlist")
	}

	// With no global policy file there is nothing to take it out of.
	if err := os.Remove(home + string(os.PathSeparator) + "policy.json"); err != nil {
		t.Fatal(err)
	}
	if code := runAllowHost([]string{"--global", "--remove", "a.example.org"}, home); code != 0 {
		t.Errorf("removing from a policy file that does not exist exited %d", code)
	}
}
