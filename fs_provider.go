package main

import (
	"fmt"
	"os"
	"os/exec"
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
	// Experimental backends require NVX_EXPERIMENTAL=1 to be selected.
	Experimental() bool
	Run(req SandboxRequest) int
}

var filesystemProviders = map[string]FilesystemProvider{
	"native":         nativeFSProvider{},
	"docker":         dockerFSProvider{},
	"sandbox-exec":   seatbeltFSProvider{},
	"seatbelt":       seatbeltFSProvider{},
	"wslc":           wslcFSProvider{},
	"wsl-container":  wslcFSProvider{},
	"container":      wslcFSProvider{},
	"wsl":            wslFSProvider{},
	"wsl-distro":     wslFSProvider{},
	"systemd-nspawn": nspawnFSProvider{},
	"nspawn":         nspawnFSProvider{},
}

func lookupFilesystemProvider(name string) (FilesystemProvider, bool) {
	p, ok := filesystemProviders[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

func experimentalProvidersEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NVX_EXPERIMENTAL"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// native ----------------------------------------------------------------------

type nativeFSProvider struct{}

func (nativeFSProvider) Name() string       { return "native" }
func (nativeFSProvider) Available() error   { return nil }
func (nativeFSProvider) Experimental() bool { return false }
func (nativeFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("native", mode)
}
func (nativeFSProvider) Run(req SandboxRequest) int {
	return runNativeSandbox(req.Config, req.Policy, req.Egress, req.NetCtx)
}

// docker -----------------------------------------------------------------------

type dockerFSProvider struct{}

func (dockerFSProvider) Name() string       { return "docker" }
func (dockerFSProvider) Experimental() bool { return false }
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

func (seatbeltFSProvider) Name() string       { return "sandbox-exec" }
func (seatbeltFSProvider) Experimental() bool { return false }
func (seatbeltFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("seatbelt", mode)
}
func (seatbeltFSProvider) Available() error {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		return fmt.Errorf("/usr/bin/sandbox-exec not found (macOS only)")
	}
	return nil
}
func (seatbeltFSProvider) Run(req SandboxRequest) int {
	return runSeatbeltSandbox(req.Config, req.NetCtx)
}

// experimental backends --------------------------------------------------------

type wslcFSProvider struct{}

func (wslcFSProvider) Name() string       { return "wslc" }
func (wslcFSProvider) Experimental() bool { return true }
func (wslcFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("wslc", mode)
}
func (wslcFSProvider) Available() error {
	if !commandExists("wsl") && !commandExists("wsl.exe") {
		return fmt.Errorf("wsl.exe not found")
	}
	return nil
}
func (wslcFSProvider) Run(req SandboxRequest) int {
	return runWslcSandbox(req.Config, req.Config.NvxHome, req.Pinned)
}

type wslFSProvider struct{}

func (wslFSProvider) Name() string       { return "wsl" }
func (wslFSProvider) Experimental() bool { return true }
func (wslFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("wsl", mode)
}
func (wslFSProvider) Available() error {
	if !commandExists("wsl") && !commandExists("wsl.exe") {
		return fmt.Errorf("wsl.exe not found")
	}
	return nil
}
func (wslFSProvider) Run(req SandboxRequest) int {
	return runWslSandbox(req.Config)
}

type nspawnFSProvider struct{}

func (nspawnFSProvider) Name() string       { return "systemd-nspawn" }
func (nspawnFSProvider) Experimental() bool { return true }
func (nspawnFSProvider) SupportsNetworkMode(mode string) bool {
	return providerSupportsNetworkMode("systemd-nspawn", mode)
}
func (nspawnFSProvider) Available() error {
	if !commandExists("systemd-nspawn") {
		return fmt.Errorf("systemd-nspawn not found")
	}
	return nil
}
func (nspawnFSProvider) Run(req SandboxRequest) int {
	return runNspawnSandbox(req.Config)
}
