//go:build !linux

package main

func setupLoopbackNetworkNamespace() error {
	return nil
}

func networkModeRequiresNamespace(mode string) bool {
	return false
}
