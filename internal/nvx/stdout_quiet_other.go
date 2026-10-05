//go:build !windows

package nvx

import "os"

// quietStdout points this process's stdout, and so a child's that is started
// with it, at the null device until restore is called.
func quietStdout() (restore func()) {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return func() {}
	}
	prev := os.Stdout
	os.Stdout = null
	return func() {
		os.Stdout = prev
		_ = null.Close()
	}
}
