package main

import (
	"strings"
	"testing"
)

// The container gets a HOME it can write.
//
// nvx scrubs the environment before launching, so the container had no HOME.
// Tools that need one fall back to the filesystem root: npm put its cache at
// /.npm, and once the container stopped running as root `npm install` failed
// with EACCES creating it -- the first thing anyone would try under this
// provider. Measured on a Linux runner before this was set.
//
// /tmp because nvx already mounts a tmpfs there: writable whatever uid the
// container runs as, and gone when the container is, which is what the native
// providers' ephemeral guest home does.
func TestTheDockerContainerGetsAWritableHome(t *testing.T) {
	args := dockerRunArgs("node:22", "/some/project", SandboxConfig{}, nil, NetworkLaunchContext{Mode: "open"})

	found := ""
	for i, a := range args {
		if a == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "HOME=") {
			found = args[i+1]
		}
	}
	if found != "HOME=/tmp" {
		t.Fatalf("HOME is %q, want HOME=/tmp:\n%s", found, strings.Join(args, " "))
	}
	// And the tmpfs it points at is really mounted, or this is a path nothing
	// can write either.
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--tmpfs /tmp") {
		t.Fatalf("HOME points at /tmp but no tmpfs is mounted there:\n%s", joined)
	}
}
