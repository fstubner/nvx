package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A `policy.json` belonging to another tool is not read as an nvx policy.
//
// nvx accepts `.nvx-policy.json` and, for convenience, a bare `policy.json`.
// The bare name is not nvx's: Open Policy Agent, cloud IAM exports, Terraform
// and several linters all write a file called exactly that, and the search
// walks every ancestor of the working directory, so one anywhere above a
// project was picked up. What followed was a trust prompt naming a file the
// user never wrote for nvx, keys warned about as misspelt nvx settings, and a
// hash pinned to a document that has nothing to do with nvx and changes on its
// own schedule.
//
// Deciding by content rather than by name: a bare `policy.json` counts only if
// it carries at least one key nvx knows. The explicit `.nvx-policy.json` is
// always nvx's, whatever is in it.
func TestABarePolicyJSONFromAnotherToolIsNotAnNvxPolicy(t *testing.T) {
	nvxHome := tempDir(t)
	project := filepath.Join(tempDir(t), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	// An OPA bundle manifest: valid JSON, an object, not one nvx key in it.
	foreign := `{"roles":[{"name":"admin","permissions":["read","write"]}],"version":"1.2"}`
	if err := os.WriteFile(filepath.Join(project, "policy.json"), []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := collectProjectPolicyPaths(project, nvxHome); len(got) != 0 {
		t.Fatalf("another tool's policy.json was taken for an nvx policy: %v", got)
	}
}

// The control: a bare policy.json that IS nvx's is still honoured, which is the
// whole reason the bare name is accepted.
func TestABarePolicyJSONWithNvxSettingsIsStillRead(t *testing.T) {
	nvxHome := tempDir(t)
	project := filepath.Join(tempDir(t), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	mine := `{"isolation":{"enabled":true}}`
	path := filepath.Join(project, "policy.json")
	if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	got := collectProjectPolicyPaths(project, nvxHome)
	if len(got) != 1 || got[0] != path {
		t.Fatalf("an nvx policy written under the bare name was skipped: %v", got)
	}
}

// And the explicit name is nvx's regardless of content -- someone who names the
// file .nvx-policy.json means it for nvx, and a file whose every key is
// misspelt still needs the warning that says so.
func TestTheExplicitPolicyNameIsAlwaysRead(t *testing.T) {
	nvxHome := tempDir(t)
	project := filepath.Join(tempDir(t), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, ".nvx-policy.json")
	if err := os.WriteFile(path, []byte(`{"isolatoin":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := collectProjectPolicyPaths(project, nvxHome)
	if len(got) != 1 || got[0] != path {
		t.Fatalf("an explicitly named nvx policy was skipped: %v", got)
	}
}
