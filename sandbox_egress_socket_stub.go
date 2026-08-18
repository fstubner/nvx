//go:build !linux

package main

// prepareEgressForNamespace is a no-op off Linux: only the Linux native provider
// puts the sandboxed process in a network namespace, so only it needs the parent's
// egress proxy reachable across that boundary. Windows and macOS reach the proxy
// over ordinary loopback TCP.
func prepareEgressForNamespace(egress *EgressProxy, guestHome string, netCtx *NetworkLaunchContext) error {
	return nil
}
