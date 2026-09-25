//go:build linux

package nvx

import (
	"os"
	"path/filepath"
	"testing"
)

// An `ip` or `iptables` earlier in PATH is not the one nvx runs.
//
// Both calls used to set cmd.Env to a PATH of system directories, which reads
// as pinning the program and does not: exec.Command resolves a bare name
// through the calling process's own PATH when the Cmd is built, and cmd.Env
// only reaches the child. A planted `ip` first on the process PATH ran.
//
// The planted programs write a marker and exit 0, so if either runs, the
// marker exists afterwards. The real tools may well fail here (no
// CAP_NET_ADMIN, or not installed); that is not what is being tested, so
// their errors are ignored.
func TestAPlantedLinuxNetworkToolOnPathIsNotTheOneRun(t *testing.T) {
	dir := tempDir(t)
	marker := filepath.Join(dir, "planted-tool-ran")
	for _, name := range []string{"ip", "iptables"} {
		script := "#!/bin/sh\n: > '" + marker + "-" + name + "'\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_ = bringUpLoopback()
	_, _ = runIptables("iptables", "--version")

	for _, name := range []string{"ip", "iptables"} {
		if _, err := os.Stat(marker + "-" + name); err == nil {
			t.Errorf("the %s planted first in PATH was the one nvx ran", name)
		}
	}
}

// And the resolver answers from the system directories only: a name that
// exists nowhere but PATH is an error, not a fallback.
func TestLinuxSystemToolPathNeverFallsBackToPath(t *testing.T) {
	dir := tempDir(t)
	const name = "nvx-test-only-on-path"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if p, err := systemToolPath(name); err == nil {
		t.Errorf("%s exists only on PATH and resolved to %q; want an error", name, p)
	}

	p, err := systemToolPath("sh")
	if err != nil {
		t.Fatalf("sh should resolve from a system directory: %v", err)
	}
	if !filepath.IsAbs(p) || filepath.Dir(p) == dir {
		t.Errorf("sh resolved to %q, want an absolute path in a system directory", p)
	}
}
