//go:build !linux

package main

func networkModeRequiresNamespace(mode string) bool {
	return false
}
