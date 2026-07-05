package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func runPolicyInit(args []string, nvxHome string) int {
	global := false
	project := false
	force := false
	for _, arg := range args {
		switch arg {
		case "--global":
			global = true
		case "--project":
			project = true
		case "--force", "-f":
			force = true
		}
	}
	if !global && !project {
		project = true
	}

	policy := DefaultPolicy()
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		LogError("Failed to encode policy: %v", err)
		return 1
	}
	data = append(data, '\n')

	if global {
		path := filepath.Join(nvxHome, "policy.json")
		if err := writePolicyFile(path, data, force); err != nil {
			LogError("%v", err)
			return 1
		}
		LogSuccess("Wrote global policy to %s", path)
	}
	if project {
		cwd, err := os.Getwd()
		if err != nil {
			LogError("Failed to resolve working directory: %v", err)
			return 1
		}
		path := filepath.Join(cwd, ".nvx-policy.json")
		if err := writePolicyFile(path, data, force); err != nil {
			LogError("%v", err)
			return 1
		}
		LogSuccess("Wrote project policy to %s", path)
	}
	return 0
}

func writePolicyFile(path string, data []byte, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("policy file already exists at %s (use --force to overwrite)", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create policy directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write policy: %w", err)
	}
	return nil
}
