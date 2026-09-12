//go:build !linux

package nvx

func applyLinuxNetworkSeccomp(networkMode string, proxyPort int) error {
	return nil
}
