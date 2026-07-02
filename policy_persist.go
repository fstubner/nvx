package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// persistNetworkAllowHost appends host:port to the nearest .nvx-policy.json allow_hosts.
func persistNetworkAllowHost(hostPort string) {
	hostPort = strings.TrimSpace(strings.ToLower(hostPort))
	if hostPort == "" {
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	policyPath := filepath.Join(cwd, ".nvx-policy.json")
	var local Policy
	if data, err := os.ReadFile(policyPath); err == nil {
		_ = json.Unmarshal(data, &local)
	}

	for _, existing := range local.Isolation.Network.AllowHosts {
		if strings.EqualFold(strings.TrimSpace(existing), hostPort) {
			return
		}
	}
	local.Isolation.Network.AllowHosts = append(local.Isolation.Network.AllowHosts, hostPort)

	out, err := json.MarshalIndent(local, "", "  ")
	if err != nil {
		return
	}
	out = append(out, '\n')
	_ = os.WriteFile(policyPath, out, 0644)
}
