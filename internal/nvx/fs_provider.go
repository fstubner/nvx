package nvx

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// SandboxRequest carries everything a FilesystemProvider needs to launch a
// command inside its isolation boundary.
type SandboxRequest struct {
	Config  SandboxConfig
	Policy  Policy
	Runtime RuntimeProvider
	Pinned  string
	Egress  *EgressProxy
	NetCtx  NetworkLaunchContext
}

// FilesystemProvider is an isolation backend (native OS sandbox, Docker, ...).
// Providers declare their capabilities so runSandbox can fail closed before it
// launches anything: an unavailable backend or an unenforceable network mode is
// an error, never a silent downgrade.
type FilesystemProvider interface {
	Name() string
	// Available reports whether this backend can run on the current machine.
	Available() error
	// SupportsNetworkMode reports whether the backend truly enforces the mode.
	SupportsNetworkMode(mode string) bool
	Run(req SandboxRequest) int
}

var filesystemProviders = map[string]FilesystemProvider{
	"native":       nativeFSProvider{},
	"docker":       dockerFSProvider{},
	"sandbox-exec": seatbeltFSProvider{},
	"seatbelt":     seatbeltFSProvider{},
}

// supportedProviderNames lists the canonical name of every registered backend,
// for the message a person sees when they name one that does not exist. Derived
// from the registry rather than written out, because the written-out version
// omitted the macOS provider and nobody noticed until that message became the
// only place the valid names appear.
func supportedProviderNames() string {
	seen := map[string]bool{}
	var names []string
	for _, p := range filesystemProviders {
		if n := p.Name(); !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func lookupFilesystemProvider(name string) (FilesystemProvider, bool) {
	p, ok := filesystemProviders[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// native ----------------------------------------------------------------------

type nativeFSProvider struct{}

func (nativeFSProvider) Name() string     { return "native" }
func (nativeFSProvider) Available() error { return nil }
func (nativeFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("native", mode)
}
func (nativeFSProvider) Run(req SandboxRequest) int {
	return runNativeSandbox(req.Config, req.Policy, req.Egress, req.NetCtx)
}

// docker -----------------------------------------------------------------------

type dockerFSProvider struct{}

func (dockerFSProvider) Name() string { return "docker" }
func (dockerFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("docker", mode)
}
func (dockerFSProvider) Available() error {
	if !commandExists("docker") {
		return fmt.Errorf("the docker CLI was not found on PATH")
	}
	cmd := exec.Command("docker", "info")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the docker daemon is not responding (is Docker running?)")
	}
	return nil
}
func (dockerFSProvider) Run(req SandboxRequest) int {
	return runDockerSandbox(req.Config, req.Config.NvxHome, req.Pinned, req.Egress, req.Runtime, req.NetCtx)
}

// seatbelt (macOS) -------------------------------------------------------------

type seatbeltFSProvider struct{}

func (seatbeltFSProvider) Name() string { return "sandbox-exec" }
func (seatbeltFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("seatbelt", mode)
}

// Available asks about seatbeltExecPath, the same variable the launcher uses.
//
// It had the path written out again, so the one seam that can put nvx on a
// machine with no sandbox-exec -- the real file cannot be removed from a running
// system -- moved the launcher and left this check answering about the real one.
// A test proving nvx refuses rather than running uncontained therefore could not
// reach the provider gate at all: it would report the provider available and only
// the launcher would refuse.
func (seatbeltFSProvider) Available() error {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		return fmt.Errorf("%s not found (macOS only)", seatbeltExecPath)
	}
	return nil
}
func (seatbeltFSProvider) Run(req SandboxRequest) int {
	return runSeatbeltSandbox(req.Config, req.NetCtx)
}
