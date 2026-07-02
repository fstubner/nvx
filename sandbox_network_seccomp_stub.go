//go:build !linux

package main

func applyLinuxNetworkSeccomp(networkMode string, proxyPort int) error {
	return nil
}
