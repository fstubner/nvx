//go:build linux

package nvx

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The contained process runs as the person who started nvx, not as root.
//
// The container gets --cap-drop=ALL, which is right, and it ran as root, which
// is not: root without CAP_DAC_OVERRIDE has no privilege over anyone else's
// files, so a project directory owned by the user could not be entered at 0700
// and could not be written at 0755. Measured on Linux with nvx's own flags: the
// runtime reported its script missing in the first case, and EACCES on the file
// it tried to create in the second. Under `npm install` that is every write the
// install makes.
//
// Dropping the capability instead of the privilege would have been the other
// way out, and it is worse: root in the container writing through a bind mount
// leaves root-owned files in the project, which is one of the reasons the
// systemd-nspawn provider was retired.
//
// Linux only. Docker Desktop on macOS and Windows presents the mount through a
// filesystem shim that synthesises permissive ownership, so the containers work
// there either way -- which is why a developer on Windows never saw this, and
// why nothing caught it until the provider was run on a Linux machine.
func TestDockerRunsAsTheInvokingUserOnLinux(t *testing.T) {
	args := dockerRunArgs("node:22", "/some/project", SandboxConfig{}, nil, NetworkLaunchContext{Mode: "offline"})

	want := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	found := ""
	for i, a := range args {
		if a == "--user" && i+1 < len(args) {
			found = args[i+1]
		}
	}
	if found == "" {
		t.Fatalf("the container is launched with no --user, so it runs as root:\n%s", strings.Join(args, " "))
	}
	if found != want {
		t.Fatalf("--user %s, want %s (the invoking user)", found, want)
	}
}
