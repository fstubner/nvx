package main

import (
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

// SandboxLaunchContext carries everything an isolation provider needs to run an
// already-resolved command under confinement. It is the single argument to
// IsolationProvider.Launch so the interface stays stable as new fields are added.
type SandboxLaunchContext struct {
	Config    SandboxConfig
	Policy    Policy
	Egress    *EgressProxy
	Network   NetworkLaunchContext
	PinnedVer string
}

// IsolationProvider is a confinement backend that runs a command inside an OS
// sandbox or container, enforcing filesystem, network, and process boundaries.
// Register one with RegisterIsolationProvider (typically from an init() in the
// provider's own file) — see docs/EXTENDING.md. This is the isolation-side twin
// of RuntimeProvider: nvx selects a provider by name from policy
// (isolation.provider / isolation.filesystem.provider) or the
// --isolation-provider flag.
type IsolationProvider interface {
	// Names returns the canonical name first, then any aliases.
	Names() []string
	// Description is a one-line summary shown by `nvx doctor`.
	Description() string
	// Available reports whether this provider can run on the current host
	// (correct GOOS, required binary present, etc.). Advisory, for diagnostics.
	Available() bool
	// Launch runs the command under isolation and returns its exit code.
	// It must fail closed: if confinement cannot be established, return
	// non-zero and do NOT execute the command.
	Launch(ctx SandboxLaunchContext) int
}

var isolationProviders = map[string]IsolationProvider{}

// RegisterIsolationProvider adds an isolation backend under each of its names.
// A provider with no names is rejected (it could never be selected anyway, and
// an empty Names() would panic the name-listing code).
func RegisterIsolationProvider(p IsolationProvider) {
	if p == nil || len(p.Names()) == 0 {
		return
	}
	for _, n := range p.Names() {
		isolationProviders[strings.ToLower(n)] = p
	}
}

// GetIsolationProvider looks up an isolation backend by name or alias.
func GetIsolationProvider(name string) (IsolationProvider, bool) {
	p, ok := isolationProviders[strings.ToLower(name)]
	return p, ok
}

// IsolationProviderNames returns the distinct canonical names, sorted.
func IsolationProviderNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, p := range isolationProviders {
		n := p.Names()
		if len(n) == 0 {
			continue
		}
		canonical := n[0]
		if !seen[canonical] {
			seen[canonical] = true
			names = append(names, canonical)
		}
	}
	sort.Strings(names)
	return names
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// --- Built-in providers (thin wrappers over the existing launch functions) ---

type nativeIsolationProvider struct{}

func (nativeIsolationProvider) Names() []string { return []string{"native"} }
func (nativeIsolationProvider) Description() string {
	return "OS-native isolation (Windows AppContainer / macOS Seatbelt / Linux Landlock+seccomp+namespaces)"
}
func (nativeIsolationProvider) Available() bool { return true }
func (nativeIsolationProvider) Launch(c SandboxLaunchContext) int {
	return runNativeSandbox(c.Config, c.Policy, c.Egress, c.Network)
}

type dockerIsolationProvider struct{}

func (dockerIsolationProvider) Names() []string { return []string{"docker"} }
func (dockerIsolationProvider) Description() string {
	return "Container isolation via a local Docker daemon"
}
func (dockerIsolationProvider) Available() bool { return commandExists("docker") }
func (dockerIsolationProvider) Launch(c SandboxLaunchContext) int {
	return runDockerSandbox(c.Config, c.Config.NvxHome, c.PinnedVer, c.Egress)
}

type wslcIsolationProvider struct{}

func (wslcIsolationProvider) Names() []string { return []string{"wslc", "wsl-container", "container"} }
func (wslcIsolationProvider) Description() string {
	return "Windows WSL2 utility-VM container isolation"
}
func (wslcIsolationProvider) Available() bool { return runtime.GOOS == "windows" }
func (wslcIsolationProvider) Launch(c SandboxLaunchContext) int {
	return runWslcSandbox(c.Config, c.Config.NvxHome, c.PinnedVer)
}

type wslIsolationProvider struct{}

func (wslIsolationProvider) Names() []string { return []string{"wsl", "wsl-distro"} }
func (wslIsolationProvider) Description() string {
	return "Windows WSL2 distro isolation (weaker: shares the distro kernel)"
}
func (wslIsolationProvider) Available() bool { return runtime.GOOS == "windows" }
func (wslIsolationProvider) Launch(c SandboxLaunchContext) int {
	return runWslSandbox(c.Config)
}

type seatbeltIsolationProvider struct{}

func (seatbeltIsolationProvider) Names() []string { return []string{"sandbox-exec", "seatbelt"} }
func (seatbeltIsolationProvider) Description() string {
	return "macOS Seatbelt profile via sandbox-exec"
}
func (seatbeltIsolationProvider) Available() bool {
	return runtime.GOOS == "darwin" && commandExists("sandbox-exec")
}
func (seatbeltIsolationProvider) Launch(c SandboxLaunchContext) int {
	return runSeatbeltSandbox(c.Config, c.Network)
}

type nspawnIsolationProvider struct{}

func (nspawnIsolationProvider) Names() []string { return []string{"systemd-nspawn", "nspawn"} }
func (nspawnIsolationProvider) Description() string {
	return "Linux systemd-nspawn container (requires root)"
}
func (nspawnIsolationProvider) Available() bool {
	return runtime.GOOS == "linux" && commandExists("systemd-nspawn")
}
func (nspawnIsolationProvider) Launch(c SandboxLaunchContext) int {
	return runNspawnSandbox(c.Config)
}

func init() {
	RegisterIsolationProvider(nativeIsolationProvider{})
	RegisterIsolationProvider(dockerIsolationProvider{})
	RegisterIsolationProvider(wslcIsolationProvider{})
	RegisterIsolationProvider(wslIsolationProvider{})
	RegisterIsolationProvider(seatbeltIsolationProvider{})
	RegisterIsolationProvider(nspawnIsolationProvider{})
}
