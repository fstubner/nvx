//go:build !linux && !windows

package main

// prepareEgressSocket is a no-op on the remaining platforms. Linux and Windows
// both cut the contained process off from the network entirely (a loopback-only
// namespace there, an AppContainer with no internetClient capability here), so
// both need the parent's proxy exposed on a UNIX socket that the containment does
// not cover. macOS Seatbelt leaves ordinary loopback TCP reachable, so the
// contained process can dial the proxy's TCP listener directly.
func prepareEgressSocket(egress *EgressProxy, guestHome string, netCtx *NetworkLaunchContext) error {
	return nil
}
