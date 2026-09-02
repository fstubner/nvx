//go:build windows

package main

import "sync"

// runtimeCapabilityName is the identity every nvx sandbox carries for the
// read-only trees nvx itself owns: the runtime it launches, the staged
// supervisor, cmd.exe when a launch needs it, and the parents of the guest home
// and the working directory.
//
// Those were granted to the AppContainer package SID, which was one per machine
// when the grants were designed and became one per project on 2026-08-30 for
// loopback isolation. Nothing revisited the grants, so from then on every
// project wrote its own entry into nvx's shared directories -- eight identities
// on ~/.nvx/versions/node/v24.14.1, thirty-nine on ~/.nvx/sandbox_home -- and
// the check meant to make a second launch free asked for modify rights where
// the grant had written traverse, so it never matched and the writes repeated
// on every launch. Measured 2026-09-02 with timestamps inside one contained
// `npm install` of an already-installed package: 6.0s writing read/execute over
// the node tree, 3.0s in two traverse writes timing out, 0.7s for everything
// else nvx did, and 2.2s for npm.
//
// A capability is the right shape for this. It is derived from a name, so it is
// the same on every launch and on every project, needs no registration, and is
// granted to a path once per machine. Sharing it leaks nothing the package
// grants did not already share: the trees are nvx's own, the rights are
// read/execute on public runtime binaries and traverse+stat on directories the
// sandbox may pass through but not list, and every nvx package on the machine
// already held exactly those. What must stay per project -- write access to the
// guest home and the working directory, and allow_read_exec roots -- stays on
// scopeCapabilitySID.
const runtimeCapabilityName = "nvx.runtime.readonly"

var (
	runtimeCapOnce sync.Once
	runtimeCapSID  string
	runtimeCapErr  error
)

// runtimeCapabilitySID is the SID string for runtimeCapabilityName, derived
// once per process.
func runtimeCapabilitySID() (string, error) {
	runtimeCapOnce.Do(func() {
		runtimeCapSID, runtimeCapErr = deriveCapabilitySIDString(runtimeCapabilityName)
	})
	return runtimeCapSID, runtimeCapErr
}
