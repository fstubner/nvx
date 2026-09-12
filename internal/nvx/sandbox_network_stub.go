//go:build !linux

package nvx

func networkModeRequiresNamespace(mode string) bool {
	return false
}
