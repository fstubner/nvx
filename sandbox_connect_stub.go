//go:build !darwin

package main

// startSeatbeltConnectRelays exists here so runSeatbeltSandbox, which compiles
// everywhere and refuses at runtime on anything but macOS, still builds. The
// real implementation is in sandbox_connect_darwin.go.
func startSeatbeltConnectRelays(netCtx *NetworkLaunchContext) ([]string, func(), error) {
	return nil, func() {}, nil
}
